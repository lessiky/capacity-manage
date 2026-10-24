package data

import (
	"context"
	"log/slog"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"gorm.io/gorm"
)

// RunDailySummary 日汇总执行（幂等, 由 scheduler 注入调用）
func RunDailySummary(ctx context.Context, db *gorm.DB, log *slog.Logger, params *system.Params, cal *tradingday.Calendar, bizDate time.Time) {
	now := time.Now()
	log.Info("daily summary start", slog.String("bizDate", bizDate.Format("2006-01-02")))
	_ = db.Exec("DELETE FROM daily_summary_log WHERE biz_date = ?", bizDate).Error
	summary := persistence.DailySummaryLog{BizDate: bizDate, StartedAt: &now, Status: "running"}
	_ = db.Create(&summary).Error

	store := NewStore(db)
	alertSvc := alert.New(db, params, nil)
	dep := &SummaryDeps{Judge: judge.New(params, db), Cal: cal, Alerts: alertSvc}
	done, missing, err := store.SummarizeDay(ctx, bizDate, dep)
	if err == nil {
		replayActiveEscalations(ctx, db, alertSvc, bizDate)
		RunDailyQuality(ctx, db, params, log, bizDate)
	}
	fin := time.Now()
	updates := map[string]any{"finished_at": fin}
	if err != nil {
		updates["status"] = "failed"
		updates["error_msg"] = err.Error()
		log.Error("daily summary failed", slog.Any("err", err))
	} else {
		if missing > 0 {
			updates["status"] = "partial"
		} else {
			updates["status"] = "success"
		}
		log.Info("daily summary done", slog.Int("done", done), slog.Int("missing", missing))
	}
	updates["metrics_done"] = done
	updates["metrics_missing"] = missing
	_ = db.Model(&persistence.DailySummaryLog{}).Where("biz_date = ?", bizDate).Updates(updates).Error
}

// RunDailyQuality recalculates the day-level quality score after the
// authoritative daily summary is materialized.
func RunDailyQuality(ctx context.Context, db *gorm.DB, params *system.Params, log *slog.Logger, bizDate time.Time) {
	var metrics []persistence.CapacityMetric
	if err := db.WithContext(ctx).Where("status = 'active' AND metric_kind = 'normal'").
		Order("metric_code").Find(&metrics).Error; err != nil {
		if log != nil {
			log.Warn("daily quality metric query failed", slog.Any("err", err))
		}
		return
	}
	service := NewQualityService(db, params)
	for i := range metrics {
		if _, err := service.Evaluate(ctx, metrics[i].MetricCode, bizDate, bizDate); err != nil {
			if log != nil {
				log.Warn("daily quality evaluate failed",
					slog.String("metric", metrics[i].MetricCode), slog.Any("err", err))
			}
		}
	}
}

func replayActiveEscalations(ctx context.Context, db *gorm.DB, svc *alert.Service, bizDate time.Time) {
	var codes []string
	if err := db.Model(&persistence.AlertRecord{}).
		Where("status IN ('active','processing') AND alert_type = 'threshold'").
		Distinct().Pluck("metric_code", &codes).Error; err != nil || len(codes) == 0 {
		return
	}
	var metrics []persistence.CapacityMetric
	if err := db.Where("metric_code IN ? AND status = 'active'", codes).Find(&metrics).Error; err != nil {
		return
	}
	for i := range metrics {
		_ = svc.EvaluateAndEscalate(ctx, &metrics[i], bizDate)
	}
}
