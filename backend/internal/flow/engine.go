package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/data"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"gorm.io/gorm"
)

// maxTraceRows 清洗留痕单任务最多落库行数（FR-COL-24, 防大结果集膨胀）
const maxTraceRows = 100

// runLogLimit 执行日志截断长度（FR-COL-19 全量记录, 64KB worker 截断后再保守截断）
const runLogLimit = 32000

// CollectorRunner 采集/脚本清洗执行接口（*Collectors 实现, 支持测试注入）
type CollectorRunner interface {
	Collect(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource,
		metric *persistence.CapacityMetric, vars map[string]string, base time.Time) ([]RawRow, *CollectMeta, error)
	CleanByScript(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric,
		rows []CleanRow, ref ScriptRef, vars map[string]string) ([]CleanRow, error)
}

// Engine 采集流程执行引擎（采集→清洗→落地→判定→告警）
//
// BizErr 执行前置检查的业务错误, handler 按 Code 映射为对应业务码(避免误报 500)
type BizErr struct {
	Code int
	Msg  string
}

func (e *BizErr) Error() string { return e.Msg }

func bizErr(code int, format string, args ...any) error {
	return &BizErr{Code: code, Msg: fmt.Errorf(format, args...).Error()}
}

type Engine struct {
	db         *gorm.DB
	store      *data.Store
	judger     *judge.Judge
	alerts     *alert.Service
	collectors CollectorRunner
	cal        *tradingday.Calendar
	params     *system.Params
	log        *slog.Logger
}

func NewEngine(db *gorm.DB, store *data.Store, judger *judge.Judge, alerts *alert.Service,
	collectors CollectorRunner, cal *tradingday.Calendar, params *system.Params, log *slog.Logger) *Engine {
	return &Engine{db: db, store: store, judger: judger, alerts: alerts,
		collectors: collectors, cal: cal, params: params, log: log}
}

// RunResult 执行结果（供任务监控/预览）
type RunResult struct {
	OK            bool       `json:"ok"`
	Status        string     `json:"status"`            // success/failed/timeout
	BizDate       string     `json:"bizDate,omitempty"` // 数据日期(按 bizDateMode 推导, 预览展示)
	ErrMsg        string     `json:"errorMsg"`
	RowsCollected int        `json:"rowsCollected"`
	ParseFailRate float64    `json:"parseFailRate"`
	AnomalyCount  int        `json:"anomalyCount"`
	CleanValue    *float64   `json:"cleanValue"`
	JudgeLevel    string     `json:"judgeLevel"`
	DurationMS    int64      `json:"durationMs"`
	RetryCount    int        `json:"retryCount"`
	MetricCode    string     `json:"metricCode"`
	RawPreview    []any      `json:"rawPreview,omitempty"`
	CleanPreview  []any      `json:"cleanPreview,omitempty"`
	CleanRows     []CleanRow `json:"cleanRows,omitempty"`
	DsOK          []int64    `json:"dsOk,omitempty"`     // 分类采集: 读取成功的数据源 ID
	DsFailed      []int64    `json:"dsFailed,omitempty"` // 分类采集: 读取失败的数据源 ID
	RunLog        string     `json:"runLog,omitempty"`
}

type backfillBizDateKey struct{}

// RunBackfill executes one historical business day. The requested date is
// carried explicitly so prev_day/calendar flows cannot shift it again.
func (e *Engine) RunBackfill(ctx context.Context, flowID int64, bizDate time.Time, operator string) (*RunResult, error) {
	ctx = context.WithValue(ctx, backfillBizDateKey{}, clock.DayStart(bizDate))
	return e.Run(ctx, flowID, "backfill", operator)
}

// Run 执行一次采集流程（source: auto/manual, 含失败重试 FR-COL-31）
func (e *Engine) Run(ctx context.Context, flowID int64, source, operator string) (*RunResult, error) {
	start := time.Now()
	fl, metric, dsList, err := e.loadFlow(flowID)
	if err != nil {
		return &RunResult{OK: false, Status: "failed", ErrMsg: err.Error()}, err
	}
	res := e.execute(ctx, fl, metric, dsList, source, operator, false)
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// RunTimeout 单次触发含重试的最长执行时长（供异步触发构造 context）
func (e *Engine) RunTimeout() time.Duration {
	maxRetry := e.params.Int("collect_retry_max", 3)
	if maxRetry < 0 {
		maxRetry = 0
	}
	attempt := time.Duration(e.params.Int("flow_timeout_seconds", 300)) * time.Second
	interval := time.Duration(e.params.Int("collect_retry_interval_seconds", 60)) * time.Second
	return attempt*time.Duration(maxRetry+1) + interval*time.Duration(maxRetry) + 30*time.Second
}

// RunPreview 预览试跑（不落库/不判定/不告警/不重试, W10 检查点）
func (e *Engine) RunPreview(ctx context.Context, flowID int64) (*RunResult, error) {
	start := time.Now()
	fl, metric, dsList, err := e.loadFlow(flowID)
	if err != nil {
		return &RunResult{OK: false, Status: "failed", ErrMsg: err.Error()}, err
	}
	res := e.execute(ctx, fl, metric, dsList, "manual", "preview", true)
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

func (e *Engine) loadFlow(flowID int64) (*persistence.CollectFlow, *persistence.CapacityMetric, []*persistence.DataSource, error) {
	var fl persistence.CollectFlow
	if err := e.db.First(&fl, flowID).Error; err != nil {
		return nil, nil, nil, bizErr(resp.CodeNotFound, "流程不存在")
	}
	if fl.Status == "archived" {
		return nil, nil, nil, bizErr(resp.CodeConflict, "流程已归档, 不可执行")
	}
	var metric persistence.CapacityMetric
	if err := e.db.Where("metric_code = ?", fl.MetricCode).First(&metric).Error; err != nil {
		return nil, nil, nil, bizErr(resp.CodeNotFound, "指标不存在: %s", fl.MetricCode)
	}
	if metric.Status != "active" {
		return nil, nil, nil, bizErr(resp.CodeConflict, "指标已停用: %s, 请先在指标管理启用", fl.MetricCode)
	}
	// python 采集在平台沙箱执行, 不访问数据源 → 不要求绑定, 存量已绑定的也不加载、不参与健康标记
	if fl.CollectType == "python" {
		return &fl, &metric, nil, nil
	}
	var dsList []*persistence.DataSource
	if fl.DataSourceCategoryID != nil {
		// 分类采集: 分类下全部未停用的日志数据源共用同一处理逻辑
		var rows []persistence.DataSource
		_ = e.db.Where("category_id = ? AND type = 'log' AND status <> 'disabled'", *fl.DataSourceCategoryID).
			Order("id").Find(&rows).Error
		for i := range rows {
			dsList = append(dsList, &rows[i])
		}
		if len(dsList) == 0 {
			return nil, nil, nil, bizErr(resp.CodeConflict, "分类下无可用日志数据源, 请先在分类中添加未停用的日志数据源")
		}
	} else if fl.DataSourceID != nil {
		var ds persistence.DataSource
		if err := e.db.First(&ds, *fl.DataSourceID).Error; err != nil {
			return nil, nil, nil, bizErr(resp.CodeNotFound, "数据源不存在: %d", *fl.DataSourceID)
		}
		if ds.Status == "disabled" {
			return nil, nil, nil, bizErr(resp.CodeConflict, "数据源已停用: %s, 请先在数据源页启用或为流程更换数据源", ds.Name)
		}
		dsList = []*persistence.DataSource{&ds}
	} else {
		return nil, nil, nil, bizErr(resp.CodeBadRequest, "流程未关联数据源或数据源分类")
	}
	return &fl, &metric, dsList, nil
}

// execute 单次触发 = 任务日志 + 重试循环（FR-COL-31: 失败/超时重试, 默认 3 次）
func (e *Engine) execute(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric, dsList []*persistence.DataSource, source, operator string, preview bool) *RunResult {
	maxRetry := 0
	interval := 0 * time.Second
	if !preview {
		maxRetry = e.params.Int("collect_retry_max", 3)
		if maxRetry < 0 {
			maxRetry = 0
		}
		interval = time.Duration(e.params.Int("collect_retry_interval_seconds", 60)) * time.Second
	}

	var taskID int64
	if !preview {
		now := time.Now()
		task := persistence.CollectTaskLog{
			FlowID: fl.ID, FlowCode: fl.FlowCode, MetricCode: fl.MetricCode,
			TriggeredAt: now, StartedAt: &now, Status: "running", Source: source, Operator: operator,
		}
		if err := e.db.Create(&task).Error; err != nil {
			return &RunResult{Status: "failed", ErrMsg: "任务日志创建失败: " + err.Error(), MetricCode: fl.MetricCode}
		}
		taskID = task.ID
	}

	var res *RunResult
	execStart := time.Now()
	for attempt := 0; ; attempt++ {
		res = e.doAttempt(ctx, fl, metric, dsList, source, preview)
		res.MetricCode = fl.MetricCode
		res.RetryCount = attempt
		res.DurationMS = time.Since(execStart).Milliseconds()
		if res.OK {
			if !preview {
				e.finishTask(taskID, res)
			}
			return res
		}
		if attempt >= maxRetry {
			if !preview {
				e.failFinal(ctx, fl, metric, dsList, res)
				e.finishTask(taskID, res)
			}
			return res
		}
		e.log.Warn("flow attempt failed, will retry",
			slog.String("flow", fl.FlowCode), slog.Int("attempt", attempt+1),
			slog.String("err", res.ErrMsg))
		select {
		case <-ctx.Done():
			res.ErrMsg = fmt.Sprintf("执行中止: %v (第 %d 次尝试后)", ctx.Err(), attempt+1)
			if !preview {
				e.failFinal(ctx, fl, metric, dsList, res)
				e.finishTask(taskID, res)
			}
			return res
		case <-time.After(interval):
		}
	}
}

// failFinal 重试耗尽后的收尾: 采集失败告警(P2) + 数据源健康标记（仅一次, 防重复告警）
// 分类采集仅标记读取失败的数据源; 读取正常但后续环节失败时不误伤健康数据源;
// python 采集不使用数据源, 不参与数据源健康标记, 未绑定数据源时告警不带数据源。
func (e *Engine) failFinal(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric, dsList []*persistence.DataSource, res *RunResult) {
	var ds *persistence.DataSource
	if len(dsList) > 0 {
		ds = dsList[0]
	}
	e.alerts.OnCollectFail(ctx, metric, ds, res.ErrMsg)
	if fl.CollectType == "python" || len(dsList) == 0 {
		return
	}
	if len(dsList) == 1 {
		_ = e.db.Model(&persistence.DataSource{}).Where("id = ?", dsList[0].ID).
			Update("health_status", "error").Error
		return
	}
	if len(res.DsFailed) > 0 {
		_ = e.db.Model(&persistence.DataSource{}).Where("id IN ?", res.DsFailed).
			Update("health_status", "error").Error
	}
}

// markDSHealth 成功落库后的健康标记: 单数据源流程沿用原行为; 分类采集按数据源粒度(读取失败者标记 error)
func (e *Engine) markDSHealth(dsList []*persistence.DataSource, okIDs, failedIDs []int64, base time.Time) {
	if len(dsList) == 1 {
		_ = e.db.Model(&persistence.DataSource{}).Where("id = ?", dsList[0].ID).
			Updates(map[string]any{"health_status": "ok", "last_success_at": base}).Error
		return
	}
	if len(okIDs) > 0 {
		_ = e.db.Model(&persistence.DataSource{}).Where("id IN ?", okIDs).
			Updates(map[string]any{"health_status": "ok", "last_success_at": base}).Error
	}
	if len(failedIDs) > 0 {
		_ = e.db.Model(&persistence.DataSource{}).Where("id IN ?", failedIDs).
			Update("health_status", "error").Error
	}
}

// doAttempt 单次执行: 变量解析 → 采集 → 清洗(内置链+脚本) → 落地 → 判定
func (e *Engine) doAttempt(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric, dsList []*persistence.DataSource, source string, preview bool) *RunResult {
	res := &RunResult{Status: "failed"}
	base := time.Now()
	market := fl.TradingMarket
	if market == "" {
		market = "SH"
	}
	vars, err := ResolveVars(ctx, e.cal, market, base, metric.SystemCode, fl.FlowCode, fl.MetricCode, fl.ScheduleMode == "trading_day")
	if err != nil {
		res.ErrMsg = "变量解析失败: " + err.Error()
		return res
	}
	// 数据日期: 与日汇总口径一致(data.PickBizDate)。prev_day=上一自然日, calendar=上一交易日(推导失败回退自然日, 不阻断)
	bizDay := data.PickBizDate(ctx, e.cal, fl, base)
	if forced, ok := ctx.Value(backfillBizDateKey{}).(time.Time); ok && !forced.IsZero() {
		bizDay = clock.DayStart(forced)
	}
	if fl.BizDateMode == "calendar" && !bizDay.Before(clock.DayStart(base)) {
		e.log.Warn("resolve data date failed, fallback natural day", slog.String("flow", fl.FlowCode))
	}
	vars["biz_date"] = bizDay.Format("2006-01-02")
	res.BizDate = vars["biz_date"]
	// 采集（单次执行超时 FR-COL-33: flow_timeout_seconds 默认 300s）
	rCtx, cancel := context.WithTimeout(ctx, time.Duration(e.params.Int("flow_timeout_seconds", 300))*time.Second)
	defer cancel()
	rawRows, meta, err := e.collectors.Collect(rCtx, fl, dsList, metric, vars, base)
	if meta != nil {
		res.RunLog = meta.RunLog
		res.RowsCollected = meta.RowsTotal
		res.DsOK, res.DsFailed = meta.DsOK, meta.DsFailed
		if meta.RowsTotal > 0 {
			res.ParseFailRate = float64(meta.ParseFailCount) / float64(meta.RowsTotal)
		}
	}
	if err != nil {
		res.ErrMsg = "采集失败: " + err.Error()
		return res
	}
	if rCtx.Err() == context.DeadlineExceeded {
		res.Status = "timeout"
		res.ErrMsg = "执行超时"
		return res
	}
	// 清洗: 内置链(类型转换/单位换算) → [Python 脚本清洗 FR-COL-18] → 异常标记 → 去重取值
	var cc CleanConfig
	_ = json.Unmarshal([]byte(fl.CleanConfig), &cc)
	rows := CleanPhase1(rawRows, cc, metric.Unit)
	if cc.Script != nil && cc.Script.ScriptID != 0 {
		rows, err = e.collectors.CleanByScript(rCtx, fl, metric, rows, *cc.Script, vars)
		if err != nil {
			res.ErrMsg = "脚本清洗失败: " + err.Error()
			return res
		}
	}
	cleanRows, final := CleanPhase2(rows, cc, metric.MetricType, metric.StatMode)
	if final.Value != nil {
		res.CleanValue = final.Value
	}
	res.CleanRows = capTraces(cleanRows)
	res.AnomalyCount = 0
	for _, r := range cleanRows {
		if r.IsAnomaly {
			res.AnomalyCount++
		}
	}
	res.RawPreview = previewOfRaw(rawRows)
	res.CleanPreview = []any{final}
	// 异常值占比超限 → P2（仅成功落库路径告警）
	if !preview && res.RowsCollected > 0 && float64(res.AnomalyCount)/float64(res.RowsCollected) > e.params.Float("anomaly_ratio_limit", 0.2) {
		e.alerts.OnAnomalyRatio(ctx, metric, res.AnomalyCount, res.RowsCollected)
	}
	// 落地 + 判定 + 告警（异常值不参与阈值判定 FR-COL-21）
	if !preview && !final.IsAnomaly && final.Value != nil {
		pointTs := businessPointTime(bizDay, base)
		if source == "backfill" {
			// 补采使用业务日固定时间戳，重复补采同一天时 upsert 同一行。
			pointTs = clock.DayStart(bizDay)
		}
		pts := []data.Point{{
			MetricCode: metric.MetricCode, Ts: pointTs,
			RawValue: final.RawValuePtr(), CleanValue: final.Value,
			Source: source,
		}}
		if err := e.store.SavePoints(ctx, pts); err != nil {
			res.ErrMsg = "数据落地失败: " + err.Error()
			return res
		}
		jr := e.judger.Evaluate(metric, final.Value)
		e.store.BackfillJudgeLevel(metric.MetricCode, pointTs, jr.Level)
		res.JudgeLevel = jr.Level
		if jr.Level != "normal" {
			e.alerts.OnThreshold(ctx, metric, jr, base)
		}
		if fl.CollectType != "python" {
			e.markDSHealth(dsList, meta.DsOK, meta.DsFailed, base)
		}
		if source == "manual" {
			dep := &data.SummaryDeps{Judge: e.judger, Cal: e.cal}
			if err := e.store.SummarizeMetricBizDate(ctx, metric, bizDay, dep); err != nil {
				e.log.Warn("manual collection day refresh failed",
					slog.String("metric", metric.MetricCode), slog.Any("err", err))
			}
			if err := e.alerts.EvaluateAndEscalate(ctx, metric, bizDay); err != nil {
				e.log.Warn("manual collection escalation replay failed",
					slog.String("metric", metric.MetricCode), slog.Any("err", err))
			}
		}
	}
	res.OK = true
	res.Status = "success"
	return res
}

// businessPointTime 将落点时间对齐到业务日的同一时刻。
// 上一日/上一交易日采集的数据属于目标业务日, 不能按实际执行日落入日内表。
func businessPointTime(bizDay, base time.Time) time.Time {
	day := clock.DayStart(bizDay)
	if day.Equal(clock.DayStart(base)) {
		return base
	}
	return time.Date(day.Year(), day.Month(), day.Day(),
		base.Hour(), base.Minute(), base.Second(), base.Nanosecond(), base.Location())
}

// finishTask 任务日志收尾 + 逐行清洗留痕（FR-COL-24）
func (e *Engine) finishTask(taskID int64, res *RunResult) {
	now := time.Now()
	updates := map[string]any{
		"status": res.Status, "finished_at": now,
		"duration_ms": res.DurationMS, "rows_collected": res.RowsCollected,
		"parse_fail_count": int(float64(res.ParseFailRate*float64(res.RowsCollected)) + 0.5),
		"parse_fail_rate":  res.ParseFailRate, "anomaly_count": res.AnomalyCount,
		"retry_count": res.RetryCount,
		"error_msg":   truncStr(res.ErrMsg, 2000),
		"run_log":     truncStr(res.RunLog, runLogLimit),
	}
	if err := e.db.Model(&persistence.CollectTaskLog{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
		e.log.Warn("finish task log failed", slog.Int64("task", taskID), slog.Any("err", err))
		return
	}
	if len(res.CleanRows) == 0 {
		return
	}
	traces := make([]persistence.CleaningTrace, 0, len(res.CleanRows))
	for _, r := range res.CleanRows {
		rules, _ := json.Marshal(r.Rules)
		traces = append(traces, persistence.CleaningTrace{
			TaskLogID: taskID, MetricCode: res.MetricCode,
			RawValue:      truncStr(r.Raw, 250),
			CleanValue:    r.Value,
			RulesApplied:  string(rules),
			IsAnomaly:     r.IsAnomaly,
			AnomalyReason: truncStr(r.Reason, 250),
		})
	}
	if err := e.db.Create(&traces).Error; err != nil {
		e.log.Warn("cleaning traces save failed", slog.Int64("task", taskID), slog.Any("err", err))
	}
}

func capTraces(rows []CleanRow) []CleanRow {
	if len(rows) > maxTraceRows {
		out := make([]CleanRow, maxTraceRows)
		copy(out, rows)
		return out
	}
	return rows
}

func previewOfRaw(rows []RawRow) []any {
	out := make([]any, 0, 5)
	for i, r := range rows {
		if i >= 5 {
			break
		}
		out = append(out, r.Raw)
	}
	return out
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
