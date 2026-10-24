package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Service 告警生成/合并/状态机（docs/10-design/04）
type Service struct {
	db       *gorm.DB
	params   *system.Params
	log      *slog.Logger
	notifier *NotificationManager
}

func New(db *gorm.DB, params *system.Params, _ *tradingday.Calendar) *Service {
	return &Service{db: db, params: params}
}

func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

func (s *Service) SetNotificationManager(m *NotificationManager) {
	if s != nil {
		s.notifier = m
	}
}

// OnThreshold 阈值判定结果 → 告警（合并/升级/恢复）
func (s *Service) OnThreshold(ctx context.Context, metric *persistence.CapacityMetric, jr judge.Result, ts time.Time) {
	sourceLine := ""
	if jr.Level == "danger" {
		sourceLine = "danger"
	} else if jr.Level == "warn" {
		sourceLine = "warn"
	}
	silent := s.systemSilenced(metric.SystemCode)
	// 查找活动告警
	var active persistence.AlertRecord
	hasActive := s.db.Where("metric_code = ? AND status IN ('active','processing') AND alert_type='threshold'",
		metric.MetricCode).Order("trigger_at DESC").First(&active).Error == nil

	if sourceLine == "" {
		// 恢复改为按日级 recover_days 在 EvaluateAndEscalate 中判定。
		return
	}
	if !hasActive {
		// 首次命中只创建 P3；是否升级 P2/P1 由日级累积规则决定。
		alertNo := "AL" + ts.Format("20060102150405") + "-" + uuid.NewString()[:6]
		rec := persistence.AlertRecord{
			AlertNo: alertNo, MetricCode: metric.MetricCode, SystemCode: metric.SystemCode,
			Level: "p3", AlertType: "threshold", Status: "active", SourceLine: sourceLine,
			TriggerAt: ts, LastValueAt: &ts,
			FirstValue: jr.Value, LastValue: jr.Value,
			Threshold: jr.Threshold, WaterLevel: jr.WaterLevel,
			DurationMin: intPtr(0), Silent: silent,
			DeadlineAt: s.deadlineFor("p3", ts),
		}
		if err := s.db.Create(&rec).Error; err != nil {
			s.logger().Error("alert create failed", slog.Any("err", err))
			return
		}
		s.recordRealtimeHit(metric, sourceLine, ts)
		s.logger().Info("alert created", slog.String("alertNo", alertNo), slog.String("metric", metric.MetricCode), slog.String("level", "p3"))
		s.notifyAsync("trigger", &rec)
		return
	}
	// 合并刷新（FR-MON-12: 不重复推送）
	// ts 的时间轴随调用方而异: 实时采集传执行时刻, 日汇总/补采传业务日 00:00。
	// 业务日回放可能早于实时触发时刻, 因此持续时间只接受正向增量并保持单调,
	// 否则会把已累计的时长写成负数或让它倒退。
	dur := 0
	if active.DurationMin != nil && *active.DurationMin > 0 {
		dur = *active.DurationMin
	}
	if elapsed := int(ts.Sub(active.TriggerAt).Minutes()); elapsed > dur {
		dur = elapsed
	}
	updates := map[string]any{
		"last_value": jr.Value, "last_value_at": ts,
		"duration_min": dur, "water_level": jr.WaterLevel, "threshold": jr.Threshold,
	}
	if sourceLine == "danger" {
		updates["source_line"] = "danger"
	}
	_ = s.db.Model(&active).Updates(updates).Error
	s.recordRealtimeHit(metric, sourceLine, ts)
	_ = s.EvaluateAndEscalate(ctx, metric, ts)
}

// OnCollectFail 采集失败（重试耗尽）→ P4
func (s *Service) OnCollectFail(ctx context.Context, metric *persistence.CapacityMetric, ds *persistence.DataSource, errMsg string) {
	s.dataAnomaly(ctx, metric, "collect_fail", "采集失败: "+errMsg)
}

// OnAnomalyRatio 异常值占比超限 → P4
func (s *Service) OnAnomalyRatio(ctx context.Context, metric *persistence.CapacityMetric, anomalyCount, total int) {
	s.dataAnomaly(ctx, metric, "anomaly", fmt.Sprintf("异常值占比 %d/%d 超限", anomalyCount, total))
}

// OnMissing 缺数 → P4（同指标同日一条）
func (s *Service) OnMissing(ctx context.Context, metric *persistence.CapacityMetric, bizDate time.Time) {
	// 同指标同业务日一条: last_value_at 记录业务日
	var n int64
	_ = s.db.Model(&persistence.AlertRecord{}).
		Where("metric_code = ? AND alert_type = 'missing' AND last_value_at >= ? AND last_value_at < ? AND level='p4'",
			metric.MetricCode, bizDate, bizDate.AddDate(0, 0, 1)).Count(&n).Error
	if n > 0 {
		return
	}
	ts := time.Now()
	rec := persistence.AlertRecord{
		AlertNo:    "AL" + ts.Format("20060102150405") + "-" + uuid.NewString()[:6],
		MetricCode: metric.MetricCode, SystemCode: metric.SystemCode,
		Level: "p4", AlertType: "missing", Status: "active",
		TriggerAt: ts, LastValueAt: &bizDate, DurationMin: intPtr(0),
		DeadlineAt: s.deadlineFor("p4", ts),
	}
	_ = s.db.Create(&rec).Error
	s.notifyAsync("trigger", &rec)
}

// OnDataSourceError 数据源异常告警（FR-COL-04 连续异常通知, P2）。
// 同一数据源同时仅保留一条活动告警; 恢复前的后续失败仅刷新时间戳, 避免重复通知。
func (s *Service) OnDataSourceError(ctx context.Context, ds *persistence.DataSource, msg string) {
	key := "ds:" + ds.Name
	var n int64
	_ = s.db.Model(&persistence.AlertRecord{}).
		Where("metric_code = ? AND alert_type = ? AND status IN ('active','processing')", key, "ds_error").
		Count(&n).Error
	now := time.Now()
	if n > 0 {
		_ = s.db.Model(&persistence.AlertRecord{}).
			Where("metric_code = ? AND alert_type = ? AND status IN ('active','processing')", key, "ds_error").
			Update("last_value_at", now).Error
		return
	}
	rec := persistence.AlertRecord{
		AlertNo:    "AL" + now.Format("20060102150405") + "-" + uuid.NewString()[:6],
		MetricCode: key, Level: "p4", AlertType: "ds_error", Status: "active",
		TriggerAt: now, LastValueAt: &now, DurationMin: intPtr(0),
		DeadlineAt: s.deadlineFor("p4", now),
	}
	detail, _ := json.Marshal(map[string]string{"dataSource": ds.Name, "dataSourceType": ds.Type, "message": msg})
	detailStr := string(detail)
	rec.Detail = &detailStr
	_ = s.db.Create(&rec).Error
	s.logger().Info("data source error alert", slog.String("dataSource", ds.Name))
	s.notifyAsync("trigger", &rec)
}

// dataAnomaly P4 数据异常告警（1 小时去重窗口）
func (s *Service) dataAnomaly(ctx context.Context, metric *persistence.CapacityMetric, aType, msg string) {
	var n int64
	_ = s.db.Model(&persistence.AlertRecord{}).
		Where("metric_code = ? AND alert_type = ? AND level='p4' AND status IN ('active','processing') AND trigger_at > ?",
			metric.MetricCode, aType, time.Now().Add(-time.Hour)).Count(&n).Error
	if n > 0 {
		return
	}
	ts := time.Now()
	rec := persistence.AlertRecord{
		AlertNo:    "AL" + ts.Format("20060102150405") + "-" + uuid.NewString()[:6],
		MetricCode: metric.MetricCode, SystemCode: metric.SystemCode,
		Level: "p4", AlertType: aType, Status: "active",
		TriggerAt: ts, LastValueAt: &ts, DurationMin: intPtr(0),
		DeadlineAt: s.deadlineFor("p4", ts),
	}
	detail, _ := json.Marshal(map[string]string{"message": msg})
	detailStr := string(detail)
	rec.Detail = &detailStr
	_ = s.db.Create(&rec).Error
	s.logger().Info("data anomaly alert", slog.String("metric", metric.MetricCode), slog.String("type", aType))
	s.notifyAsync("trigger", &rec)
}

// systemSilenced 系统维护中且静默配置 → 阈值类告警抑制
func (s *Service) systemSilenced(systemCode string) bool {
	var sys persistence.BusinessSystem
	if err := s.db.Where("system_code = ?", systemCode).First(&sys).Error; err != nil {
		return false
	}
	return sys.Status == "maintenance" && sys.SilentMaintain
}

// ---------- 状态机操作 ----------

// Claim 认领 active → processing
func (s *Service) Claim(rec *persistence.AlertRecord, user string) error {
	if rec.Status != "active" && rec.Status != "processing" {
		return fmt.Errorf("当前状态 %s 不可认领", rec.Status)
	}
	if rec.Status == "processing" {
		return nil
	}
	now := time.Now()
	return s.db.Model(rec).Updates(map[string]any{
		"status": "processing", "claimed_by": user, "claimed_at": now,
	}).Error
}

// Note 追加处理备注（含 closed 终态, FR: 关闭后仅可备注）
func (s *Service) Note(rec *persistence.AlertRecord, user, note string) error {
	items, _ := parseNotes(rec.HandleNote)
	items = append(items, map[string]any{"time": time.Now().Format(time.RFC3339), "user": user, "note": note})
	b, _ := json.Marshal(items)
	return s.db.Model(rec).Update("handle_note", string(b)).Error
}

// Close 关闭（必填处理结果）
func (s *Service) Close(rec *persistence.AlertRecord, user, result string) error {
	if rec.Status == "closed" {
		return fmt.Errorf("告警已关闭, 不可重复操作")
	}
	if result == "" {
		return fmt.Errorf("关闭必须填写处理结果")
	}
	now := time.Now()
	return s.db.Model(rec).Updates(map[string]any{
		"status": "closed", "closed_by": user, "closed_at": now, "close_result": result,
	}).Error
}

func parseNotes(raw string) ([]map[string]any, error) {
	if raw == "" || raw == "null" {
		return []map[string]any{}, nil
	}
	var items []map[string]any
	err := json.Unmarshal([]byte(raw), &items)
	return items, err
}

// deadlineFor SLA 响应时限
func (s *Service) deadlineFor(level string, ts time.Time) *time.Time {
	if s.params == nil {
		return nil
	}
	t := SlaTargets(s.params)[level]
	if t.claim <= 0 {
		return nil
	}
	v := ts.Add(time.Duration(t.claim) * time.Minute)
	return &v
}

func intPtr(n int) *int {
	v := n
	return &v
}

// notifyAsync keeps alert generation non-blocking. The notification manager
// owns retries and delivery logs, so a transient channel outage never delays
// collection or daily-summary work.
func (s *Service) notifyAsync(event string, rec *persistence.AlertRecord) {
	if s == nil || s.notifier == nil || rec == nil {
		return
	}
	event = normalizeNotificationEvent(event)
	recCopy := *rec
	go func() {
		if err := s.notifier.Dispatch(context.Background(), event, &recCopy); err != nil {
			s.logger().Warn("notification dispatch failed",
				slog.String("alertNo", recCopy.AlertNo), slog.String("event", event), slog.Any("err", err))
		}
	}()
}

func normalizeNotificationEvent(event string) string {
	switch event {
	case "trigger":
		return "trigger"
	case "escalate":
		return "escalate"
	case "recovered":
		return "recovered"
	default:
		return event
	}
}
