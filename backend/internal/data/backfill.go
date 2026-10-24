package data

import (
	"context"
	"fmt"
	"strings"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/system"

	"gorm.io/gorm"
)

type BackfillRequest struct {
	FlowID   int64  `json:"flowId" binding:"required"`
	From     string `json:"from" binding:"required"`
	To       string `json:"to" binding:"required"`
	Source   string `json:"source"`
	Reason   string `json:"reason"`
	Operator string `json:"operator"`
}

type BackfillResult struct {
	RunID       int64  `json:"runId"`
	Status      string `json:"status"`
	TotalDays   int    `json:"totalDays"`
	SuccessDays int    `json:"successDays"`
	FailedDays  int    `json:"failedDays"`
	Error       string `json:"error,omitempty"`
}

type backfillRunner func(ctx context.Context, flowID int64, bizDate time.Time) error

type BackfillService struct {
	db     *gorm.DB
	store  *Store
	judge  *judge.Judge
	alerts *alert.Service
	params *system.Params
	runner backfillRunner
}

func NewBackfillService(db *gorm.DB, store *Store, judger *judge.Judge,
	alerts *alert.Service, params *system.Params) *BackfillService {
	return &BackfillService{db: db, store: store, judge: judger, alerts: alerts, params: params}
}

func (s *BackfillService) SetRunner(run func(ctx context.Context, flowID int64, bizDate time.Time) error) {
	if s != nil {
		s.runner = run
	}
}

func (s *BackfillService) Run(ctx context.Context, req BackfillRequest) (BackfillResult, error) {
	from, err := time.ParseInLocation("2006-01-02", req.From, time.Local)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("from 格式须为 YYYY-MM-DD")
	}
	to, err := time.ParseInLocation("2006-01-02", req.To, time.Local)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("to 格式须为 YYYY-MM-DD")
	}
	from, to = clock.DayStart(from), clock.DayStart(to)
	if to.Before(from) {
		return BackfillResult{}, fmt.Errorf("to 不得早于 from")
	}
	if req.FlowID <= 0 {
		return BackfillResult{}, fmt.Errorf("flowId 必填")
	}
	var flow persistence.CollectFlow
	if err := s.db.WithContext(ctx).First(&flow, req.FlowID).Error; err != nil {
		return BackfillResult{}, fmt.Errorf("流程不存在: %w", err)
	}
	if s.runner == nil {
		return BackfillResult{}, fmt.Errorf("补采执行器未配置")
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "backfill"
	}
	now := time.Now()
	run := persistence.DataBackfillRun{
		FlowID: flow.ID, MetricCode: flow.MetricCode, FromDate: from, ToDate: to,
		Source: source, Status: "running", CreatedBy: req.Operator, StartedAt: &now,
	}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return BackfillResult{}, err
	}
	totalDays := daysBetween(from, to)
	result := BackfillResult{RunID: run.ID, Status: "success", TotalDays: totalDays}

	for i := 0; i < totalDays; i++ {
		bizDate := from.AddDate(0, 0, i)
		item := persistence.DataBackfillItem{RunID: run.ID, BizDate: bizDate, Status: "running"}
		if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
			return result, err
		}
		runErr := s.runner(ctx, flow.ID, bizDate)
		if runErr == nil {
			runErr = s.refreshAffected(ctx, &flow, bizDate)
		}
		if runErr != nil {
			item.Status = "failed"
			item.Error = truncateErr(runErr)
			result.FailedDays++
		} else {
			item.Status = "success"
			item.SummaryRefreshed = true
			item.QualityRefreshed = true
			item.AlertReplayed = true
			item.ReportInvalidated = true
			result.SuccessDays++
		}
		_ = s.db.WithContext(ctx).Model(&item).Updates(map[string]any{
			"status": item.Status, "error": item.Error,
			"summary_refreshed": item.SummaryRefreshed, "quality_refreshed": item.QualityRefreshed,
			"alert_replayed": item.AlertReplayed, "report_invalidated": item.ReportInvalidated,
		}).Error
	}

	finished := time.Now()
	if result.FailedDays > 0 {
		if result.SuccessDays == 0 {
			result.Status = "failed"
		} else {
			result.Status = "partial"
		}
		result.Error = fmt.Sprintf("%d 天补采失败", result.FailedDays)
	}
	_ = s.db.WithContext(ctx).Model(&run).Updates(map[string]any{
		"status": result.Status, "total_days": result.TotalDays,
		"success_days": result.SuccessDays, "failed_days": result.FailedDays,
		"error": result.Error, "finished_at": finished,
	}).Error
	return result, nil
}

func (s *BackfillService) refreshAffected(ctx context.Context, flow *persistence.CollectFlow, bizDate time.Time) error {
	var metric persistence.CapacityMetric
	if err := s.db.WithContext(ctx).Where("metric_code = ?", flow.MetricCode).First(&metric).Error; err != nil {
		return err
	}
	dep := &SummaryDeps{Judge: s.judge}
	if s.store != nil {
		if err := s.store.SummarizeMetricBizDate(ctx, &metric, bizDate, dep); err != nil {
			return err
		}
	}

	var day persistence.MetricValueDay
	if err := s.db.WithContext(ctx).Where("metric_code = ? AND biz_date = ?", metric.MetricCode, bizDate).
		First(&day).Error; err == nil && s.alerts != nil {
		jr := judge.Result{Level: day.Level, Value: day.StatValue, WaterLevel: day.WaterLevel}
		if s.judge != nil {
			jr = s.judge.Evaluate(&metric, day.StatValue)
		}
		if jr.Level == "warn" || jr.Level == "danger" {
			s.alerts.OnThreshold(ctx, &metric, jr, clock.DayStart(bizDate))
		}
	}

	if s.alerts != nil {
		if err := alert.UpsertDailyHits(s.db.WithContext(ctx), &metric, bizDate); err != nil {
			return err
		}
		if err := s.alerts.EvaluateAndEscalate(ctx, &metric, bizDate); err != nil {
			return err
		}
	}

	if s.params != nil {
		qualitySvc := NewQualityService(s.db, s.params)
		if _, err := qualitySvc.Evaluate(ctx, metric.MetricCode, bizDate, bizDate); err != nil {
			return err
		}
	}
	return s.db.WithContext(ctx).Model(&persistence.ReportResult{}).
		Where("report_type = 'daily' AND biz_date = ?", bizDate).
		Update("status", "stale").Error
}

func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 1024 {
		return msg[:1024]
	}
	return msg
}

var _ = gorm.ErrRecordNotFound
