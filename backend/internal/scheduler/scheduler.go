package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/flow"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// Scheduler cron + 交易日模式调度（条目持久化于 collect_flow, 重启后重建）
type Scheduler struct {
	cron        *cron.Cron
	db          *gorm.DB
	engine      *flow.Engine
	cal         *tradingday.Calendar
	params      *system.Params
	log         *slog.Logger
	entries     map[int64]cron.EntryID
	mu          sync.Mutex
	summary     SummaryFunc
	healthCheck HealthCheckFunc
	alerts      *alert.Service
	reportGen   ReportGenFunc
	forecast    ForecastFunc
	reportSubs  ReportSubscriptionFunc
	archive     ArchiveFunc
	startedAt   time.Time
	semaMu      sync.Mutex
	semaCh      chan struct{}
	semaSize    int
}

// SetAlerts 注入告警服务（缺数检测 FR-COL-34）
func (s *Scheduler) SetAlerts(a *alert.Service) { s.alerts = a }

// acquire 全局并发信号量（契约 §6: collect_concurrency 默认 8）
func (s *Scheduler) acquire() bool {
	size := s.params.Int("collect_concurrency", 8)
	if size < 1 {
		size = 1
	}
	s.semaMu.Lock()
	defer s.semaMu.Unlock()
	if s.semaCh == nil || s.semaSize != size {
		s.semaCh = make(chan struct{}, size)
		s.semaSize = size
	}
	select {
	case s.semaCh <- struct{}{}:
		return true
	default:
		return false
	}
}

// release 归还全局并发信号量
func (s *Scheduler) release() {
	s.semaMu.Lock()
	defer s.semaMu.Unlock()
	if s.semaCh != nil {
		<-s.semaCh
	}
}

// SummaryFunc 日汇总实现（由 data 模块经 SetSummary 注入）
type SummaryFunc func(ctx context.Context, db *gorm.DB, log *slog.Logger, params *system.Params, cal *tradingday.Calendar, bizDate time.Time)

// SetSummary 注入日汇总实现
func (s *Scheduler) SetSummary(f SummaryFunc) { s.summary = f }

// HealthCheckFunc 数据源健康检查实现（由 app 经 SetHealthCheck 注入）
type HealthCheckFunc func(ctx context.Context, src *persistence.DataSource) (ok bool, msg string)

// SetHealthCheck 注入健康检查实现
func (s *Scheduler) SetHealthCheck(f HealthCheckFunc) { s.healthCheck = f }

// ReportGenFunc 日报自动生成实现（由 report 模块经 SetReportGen 注入, FR-RPT-01）
type ReportGenFunc func(ctx context.Context, db *gorm.DB, bizDate time.Time) error

// SetReportGen 注入日报生成实现
func (s *Scheduler) SetReportGen(f ReportGenFunc) { s.reportGen = f }

// ForecastFunc 统计预测实现（由 forecast 模块经 SetForecast 注入, 在日汇总与数据质量之后, FR-PRE-01）
type ForecastFunc func(ctx context.Context, db *gorm.DB, runDate time.Time) error

// SetForecast 注入统计预测实现
func (s *Scheduler) SetForecast(f ForecastFunc) { s.forecast = f }

// ReportSubscriptionFunc 报表订阅到期推送实现（由 report 模块经 SetReportSubscriptions 注入）
type ReportSubscriptionFunc func(ctx context.Context, db *gorm.DB, now time.Time) error

// SetReportSubscriptions 注入报表订阅推送实现
func (s *Scheduler) SetReportSubscriptions(f ReportSubscriptionFunc) { s.reportSubs = f }

// ArchiveFunc 超期分区压缩归档实现（由 openapi 模块注入, FR-DAY-06）
type ArchiveFunc func(ctx context.Context, now time.Time) error

// SetArchive 注入归档实现
func (s *Scheduler) SetArchive(f ArchiveFunc) { s.archive = f }

func New(db *gorm.DB, engine *flow.Engine, cal *tradingday.Calendar, params *system.Params, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:    cron.New(cron.WithSeconds()),
		db:      db,
		engine:  engine,
		cal:     cal,
		params:  params,
		log:     log,
		entries: map[int64]cron.EntryID{},
	}
}

// Start 启动: 恢复超时任务 → 加载活动流程 → 启动内置任务 → 运行 cron
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.recoverStaleTasks(ctx); err != nil {
		s.log.Warn("recover stale tasks failed", slog.Any("err", err))
	}
	if err := s.loadFlows(ctx); err != nil {
		return err
	}
	s.registerBuiltinJobs(ctx)
	s.startedAt = time.Now()
	s.cron.Start()
	go s.syncLoop(ctx)
	s.log.Info("scheduler started")
	return nil
}

// Stop 停止
func (s *Scheduler) Stop() {
	if s.cron != nil {
		_ = s.cron.Stop()
	}
}

func (s *Scheduler) recoverStaleTasks(ctx context.Context) error {
	timeout := time.Duration(s.params.Int("flow_timeout_seconds", 300)) * time.Second
	cutoff := time.Now().Add(-timeout - 5*time.Minute)
	res := s.db.Model(&persistence.CollectTaskLog{}).
		Where("status = 'running' AND started_at < ?", cutoff).
		Update("status", "timeout")
	s.log.Info("recovered stale tasks", slog.Int64("count", res.RowsAffected))
	return nil
}

func (s *Scheduler) loadFlows(ctx context.Context) error {
	var flows []persistence.CollectFlow
	if err := s.db.Where("status = 'active' AND enabled = ?", true).Find(&flows).Error; err != nil {
		return err
	}
	for i := range flows {
		if err := s.schedule(&flows[i]); err != nil {
			s.log.Warn("schedule flow failed",
				slog.String("flow", flows[i].FlowCode), slog.Any("err", err))
		}
	}
	s.log.Info("flows loaded", slog.Int("count", len(flows)))
	return nil
}

func (s *Scheduler) schedule(fl *persistence.CollectFlow) error {
	s.mu.Lock()
	if old, ok := s.entries[fl.ID]; ok {
		s.cron.Remove(old)
	}
	s.mu.Unlock()
	var (
		id  cron.EntryID
		err error
	)
	switch fl.ScheduleMode {
	case "trading_day":
		id = s.cron.Schedule(tradingDayTrigger{cal: s.cal, market: fl.TradingMarket, window: fl.WindowTime},
			cron.FuncJob(func() { s.run(fl.ID, "auto") }))
		err = nil
	default:
		spec := ensureSeconds(fl.CronExpr)
		id, err = s.cron.AddFunc(spec, func() { s.run(fl.ID, "auto") })
	}
	if err != nil {
		return fmt.Errorf("cron 调度失败 mode=%s spec=%q: %w", fl.ScheduleMode, fl.CronExpr, err)
	}
	s.mu.Lock()
	s.entries[fl.ID] = id
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) unschedule(flowID int64) {
	s.mu.Lock()
	if id, ok := s.entries[flowID]; ok {
		s.cron.Remove(id)
		delete(s.entries, flowID)
	}
	s.mu.Unlock()
}

func (s *Scheduler) run(flowID int64, source string) {
	var running int64
	_ = s.db.Model(&persistence.CollectTaskLog{}).
		Where("flow_id = ? AND status = 'running'", flowID).Count(&running).Error
	if running > 0 {
		s.log.Info("flow running, skip trigger", slog.Int64("flowId", flowID))
		return
	}
	if !s.acquire() {
		s.log.Info("global concurrency limit reached, skip trigger",
			slog.Int64("flowId", flowID), slog.Int("limit", s.params.Int("collect_concurrency", 8)))
		return
	}
	go func() {
		defer s.release()
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(s.params.Int("flow_timeout_seconds", 300)+30)*time.Second)
		defer cancel()
		_, err := s.engine.Run(ctx, flowID, source, "scheduler")
		if err != nil {
			s.log.Error("flow run failed", slog.Int64("flowId", flowID), slog.Any("err", err))
		}
	}()
}

func (s *Scheduler) syncLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sync()
		}
	}
}

// sync 热同步: 新增/停用的活动流程
func (s *Scheduler) sync() {
	var flows []persistence.CollectFlow
	if err := s.db.Where("status = 'active' AND enabled = ?", true).Find(&flows).Error; err != nil {
		return
	}
	active := map[int64]bool{}
	for i := range flows {
		active[flows[i].ID] = true
	}
	s.mu.Lock()
	for id, entry := range s.entries {
		if !active[id] {
			s.cron.Remove(entry)
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()
	for i := range flows {
		fl := &flows[i]
		s.mu.Lock()
		_, exists := s.entries[fl.ID]
		s.mu.Unlock()
		if !exists {
			if err := s.schedule(fl); err != nil {
				s.log.Warn("sync schedule failed", slog.Int64("flowId", fl.ID), slog.Any("err", err))
			}
		}
	}
}

// registerBuiltinJobs 内置周期任务
func (s *Scheduler) registerBuiltinJobs(ctx context.Context) {
	go s.dailySummaryLoop(ctx)
	go s.dailyReportLoop(ctx)
	go s.dailyForecastLoop(ctx)
	go s.reportSubscriptionLoop(ctx)
	go s.archiveLoop(ctx)
	_, _ = s.cron.AddFunc("0 */10 * * * *", func() { s.periodic(ctx) })
	_, _ = s.cron.AddFunc("0 0 9 * * *", func() { s.missingCheck(ctx) })
}

// archiveLoop 超期分区压缩归档（默认每日 03:30, 参数 archive_time）
func (s *Scheduler) archiveLoop(ctx context.Context) {
	// 初始化 last 为进程启动时间：若在触发时刻之后启动，则当天不补跑，
	// 避免每次重启都立即执行一次带"下线删分区"的归档 sweep；次日按点执行。
	last := time.Now()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.archive == nil {
				continue
			}
			hhmm := s.params.Get("archive_time", "03:30")
			var hh, mm int
			if _, err := fmt.Sscanf(hhmm, "%d:%d", &hh, &mm); err != nil {
				continue
			}
			now := time.Now()
			trigger := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
			if !now.After(trigger) || !last.Before(trigger) {
				continue
			}
			last = trigger
			go func() {
				if err := s.archive(ctx, time.Now()); err != nil {
					s.log.Warn("archive run failed", slog.Any("err", err))
				}
			}()
		}
	}
}

func (s *Scheduler) periodic(ctx context.Context) {
	_ = persistence.EnsurePartitions(ctx, s.db)
	if s.healthCheck == nil {
		return
	}
	var srcs []persistence.DataSource
	_ = s.db.Where("status = 'available'").Find(&srcs).Error
	for i := range srcs {
		ok, msg := s.healthCheck(ctx, &srcs[i])
		if !ok {
			_ = s.db.Model(&srcs[i]).Updates(map[string]any{"health_status": "error", "last_error": msg}).Error
		} else {
			_ = s.db.Model(&srcs[i]).Updates(map[string]any{"health_status": "ok", "last_success_at": time.Now()}).Error
		}
	}
}

func (s *Scheduler) dailySummaryLoop(ctx context.Context) {
	last := time.Time{}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hhmm := s.params.Get("daily_summary_time", "00:10")
			var hh, mm int
			if _, err := fmt.Sscanf(hhmm, "%d:%d", &hh, &mm); err != nil {
				continue
			}
			now := time.Now()
			trigger := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
			if now.After(trigger) && last.Before(trigger) {
				last = trigger
				bizDate := trigger.AddDate(0, 0, -1)
				s.log.Info("daily summary trigger", slog.String("bizDate", bizDate.Format("2006-01-02")))
				if s.summary != nil {
					go s.summary(ctx, s.db, s.log, s.params, s.cal, bizDate)
				}
			}
		}
	}
}

// dailyReportLoop 日报自动生成（默认 00:30, 参数 daily_report_time; 在 00:10 日汇总之后）
func (s *Scheduler) dailyReportLoop(ctx context.Context) {
	last := time.Time{}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hhmm := s.params.Get("daily_report_time", "00:30")
			var hh, mm int
			if _, err := fmt.Sscanf(hhmm, "%d:%d", &hh, &mm); err != nil {
				continue
			}
			now := time.Now()
			trigger := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
			if now.After(trigger) && last.Before(trigger) {
				last = trigger
				bizDate := trigger.AddDate(0, 0, -1)
				if s.reportGen == nil {
					continue
				}
				s.log.Info("daily report trigger", slog.String("bizDate", bizDate.Format("2006-01-02")))
				go s.reportGen(ctx, s.db, bizDate)
			}
		}
	}
}

// dailyForecastLoop 统计预测（默认 00:45, 参数 daily_forecast_time）:
// 仅当该业务日日汇总与数据质量检查已完成时触发, 否则下一 tick 重试。
func (s *Scheduler) dailyForecastLoop(ctx context.Context) {
	last := time.Time{}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.forecast == nil {
				continue
			}
			hhmm := s.params.Get("daily_forecast_time", "00:45")
			var hh, mm int
			if _, err := fmt.Sscanf(hhmm, "%d:%d", &hh, &mm); err != nil {
				continue
			}
			now := time.Now()
			trigger := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
			if !now.After(trigger) || !last.Before(trigger) {
				continue
			}
			bizDate := trigger.AddDate(0, 0, -1)
			if !s.dailySummaryReady(bizDate) {
				continue
			}
			last = trigger
			if !s.forecastShouldStart(bizDate, now) {
				s.log.Info("daily forecast skipped", slog.String("bizDate", bizDate.Format("2006-01-02")))
				continue
			}
			s.log.Info("daily forecast trigger", slog.String("bizDate", bizDate.Format("2006-01-02")))
			go func() {
				if err := s.forecast(ctx, s.db, bizDate); err != nil {
					s.log.Error("daily forecast failed", slog.Any("err", err))
				}
			}()
		}
	}
}

// dailySummaryReady 日汇总(含数据质量)是否已完成, 作为预测触发前置条件
func (s *Scheduler) dailySummaryReady(bizDate time.Time) bool {
	var row persistence.DailySummaryLog
	if err := s.db.Where("biz_date = ?", bizDate).First(&row).Error; err != nil {
		return false
	}
	return row.Status == "success" || row.Status == "partial"
}

// forecastShouldStart avoids duplicate full-volume runs after a process
// restart and prevents a fresh manual batch from competing with the scheduler.
// A running row older than two hours is treated as stale and can be retried.
func (s *Scheduler) forecastShouldStart(bizDate, now time.Time) bool {
	if s == nil || s.db == nil {
		return false
	}
	freshAfter := now.Add(-2 * time.Hour)
	var sameDay persistence.ForecastRun
	if err := s.db.Where("run_date = ?", bizDate).Order("id DESC").First(&sameDay).Error; err == nil {
		if sameDay.Status == "success" {
			return false
		}
		if sameDay.Status == "running" && sameDay.StartedAt != nil && sameDay.StartedAt.After(freshAfter) {
			return false
		}
	}
	var active int64
	if err := s.db.Model(&persistence.ForecastRun{}).
		Where("status = ? AND started_at >= ?", "running", freshAfter).
		Count(&active).Error; err != nil {
		return false
	}
	return active == 0
}

// reportSubscriptionLoop 报表订阅到期推送（每分钟检查一次, 时区/周期由订阅配置决定）
func (s *Scheduler) reportSubscriptionLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.reportSubs == nil {
				continue
			}
			if err := s.reportSubs(ctx, s.db, time.Now()); err != nil {
				s.log.Warn("report subscription dispatch failed", slog.Any("err", err))
			}
		}
	}
}

// Snapshot 调度器状态（平台自检 FR-ADM-08 基础版）
func (s *Scheduler) Snapshot() map[string]any {
	s.mu.Lock()
	scheduled := len(s.entries)
	s.mu.Unlock()
	var running, todayTotal int64
	_ = s.db.Model(&persistence.CollectTaskLog{}).Where("status = 'running'").Count(&running).Error
	_ = s.db.Model(&persistence.CollectTaskLog{}).
		Where("triggered_at >= ?", clock.DayStart(time.Now())).Count(&todayTotal).Error
	return map[string]any{
		"startedAt":      s.startedAt,
		"scheduledFlows": scheduled,
		"runningTasks":   running,
		"tasksToday":     todayTotal,
	}
}

// missingCheck 缺数检测（FR-COL-34 基础版）:
// 数据日期=上一交易日 的流程按对应市场前一交易日, 其余按前一自然日;
// 期望业务日无日内数据点 → P2 缺数告警（同指标同日一条, OnMissing 内置去重）
func (s *Scheduler) missingCheck(ctx context.Context) {
	if s.alerts == nil {
		return
	}
	var flows []persistence.CollectFlow
	if err := s.db.Where("status = 'active' AND enabled = ?", true).Find(&flows).Error; err != nil {
		return
	}
	now := time.Now()
	for i := range flows {
		fl := &flows[i]
		// 新建流程(期望业务日之后才建档)无历史数据, 不判定缺数
		var expect time.Time
		var err error
		if fl.BizDateMode == "calendar" {
			market := fl.TradingMarket
			if market == "" {
				market = "SH"
			}
			expect, err = s.cal.PrevTradingDay(ctx, market, now)
			if err != nil {
				s.log.Warn("missing check: prev trading day failed",
					slog.String("flow", fl.FlowCode), slog.Any("err", err))
				continue
			}
		} else {
			expect = clock.DayStart(now).AddDate(0, 0, -1)
		}
		if fl.CreatedAt.After(expect) {
			continue
		}
		// 业务系统下线 → 冻结, 不判定
		var metric persistence.CapacityMetric
		if err := s.db.Where("metric_code = ?", fl.MetricCode).First(&metric).Error; err != nil {
			continue
		}
		var sys persistence.BusinessSystem
		if err := s.db.Where("system_code = ?", metric.SystemCode).First(&sys).Error; err == nil && sys.Status == "offline" {
			continue
		}
		var n int64
		err = s.db.Model(&persistence.MetricValueRT{}).
			Where("metric_code = ? AND ts >= ? AND ts < ?", fl.MetricCode, expect, expect.AddDate(0, 0, 1)).
			Count(&n).Error
		if err != nil || n > 0 {
			continue
		}
		s.log.Warn("missing data detected",
			slog.String("flow", fl.FlowCode), slog.String("metric", fl.MetricCode),
			slog.String("bizDate", expect.Format("2006-01-02")))
		s.alerts.OnMissing(ctx, &metric, expect)
	}
}

// tradingDayTrigger 交易日触发器: Next = 下一(市场)交易日窗口时点
type tradingDayTrigger struct {
	cal    *tradingday.Calendar
	market string
	window string
}

func (t tradingDayTrigger) Next(after time.Time) time.Time {
	market := t.market
	if market == "" {
		market = "SH"
	}
	window := t.window
	if window == "" {
		window = "17:30"
	}
	nxt, err := t.cal.NextTriggerAfter(context.Background(), market, window, after.Add(30*time.Second))
	if err != nil {
		return time.Now().Add(10 * time.Minute)
	}
	return nxt
}

// ensureSeconds cron 表达式: 5 段(分钟级) → 6 段(秒级)
func ensureSeconds(expr string) string {
	parts := strings.Fields(expr)
	if len(parts) == 5 {
		return "0 " + expr
	}
	return expr
}
