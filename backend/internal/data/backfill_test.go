package data

import (
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
)

type fakeBackfillRunner struct {
	calls []time.Time
}

func (f *fakeBackfillRunner) Run(_ context.Context, _ int64, bizDate time.Time) error {
	f.calls = append(f.calls, bizDate)
	return nil
}

func TestBackfillRunIsIdempotentAndReplaysQualityAlertsReports(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.MetricValueRT{}, &persistence.MetricValueDay{},
		&persistence.AlertRecord{}, &persistence.AlertDailyHit{}, &persistence.AlertEscalationPolicy{},
		&persistence.AlertLevelHistory{}, &persistence.MetricPeakStat{}, &persistence.MetricQualityDay{},
		&persistence.CollectFlow{}, &persistence.DataBackfillRun{}, &persistence.DataBackfillItem{},
		&persistence.ReportResult{})
	params := system.NewParams(h.DB)
	_ = h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "CPU", Name: "CPU", SystemCode: "SYS", MetricKind: "normal",
		StatMode: "latest", JudgeDirection: "higher_worse", Status: "active",
		WarnLine: floatPtrB(80), DangerLine: floatPtrB(90),
	}).Error
	_ = h.DB.Create(&persistence.CollectFlow{ID: 1, FlowCode: "F1", MetricCode: "CPU", Status: "active", Enabled: true}).Error
	reportDate := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	_ = h.DB.Create(&persistence.ReportResult{ReportType: "daily", BizDate: reportDate, Status: "success"}).Error

	store := NewStore(h.DB)
	alertSvc := alert.New(h.DB, params, nil)
	runner := &fakeBackfillRunner{}
	svc := NewBackfillService(h.DB, store, judge.New(params, h.DB), alertSvc, params)
	svc.SetRunner(func(ctx context.Context, flowID int64, bizDate time.Time) error {
		runner.calls = append(runner.calls, bizDate)
		v := 85.0
		return store.SavePoints(ctx, []Point{{MetricCode: "CPU", Ts: bizDate.Add(10 * time.Hour), RawValue: &v, CleanValue: &v, Source: "backfill"}})
	})
	req := BackfillRequest{
		FlowID: 1, From: "2026-10-13", To: "2026-10-15",
		Source: "backfill", Reason: "历史数据修复", Operator: "admin",
	}
	res1, err := svc.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Status != "success" || res1.SuccessDays != 3 || len(runner.calls) != 3 {
		t.Fatalf("first result = %+v calls=%d", res1, len(runner.calls))
	}
	var alerts []persistence.AlertRecord
	_ = h.DB.Where("metric_code = ?", "CPU").Find(&alerts).Error
	if len(alerts) != 1 {
		t.Fatalf("alerts after first run = %d, want 1", len(alerts))
	}
	var day persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "CPU", reportDate).First(&day).Error; err != nil {
		t.Fatal(err)
	}
	if day.Source != "collect" || day.StatValue == nil {
		t.Fatalf("day row = %+v, want recalculated summary", day)
	}
	var qualityN int64
	_ = h.DB.Model(&persistence.MetricQualityDay{}).Count(&qualityN).Error
	if qualityN != 3 {
		t.Fatalf("quality rows = %d, want 3", qualityN)
	}
	var report persistence.ReportResult
	_ = h.DB.Where("report_type = 'daily' AND biz_date = ?", reportDate).First(&report).Error
	if report.Status != "stale" {
		t.Fatalf("report status = %s, want stale", report.Status)
	}

	res2, err := svc.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "success" || res2.SuccessDays != 3 {
		t.Fatalf("second result = %+v", res2)
	}
	alerts = nil
	_ = h.DB.Where("metric_code = ?", "CPU").Find(&alerts).Error
	if len(alerts) != 1 {
		t.Fatalf("alerts after repeat = %d, want idempotent single alert", len(alerts))
	}
}

func TestBackfillRejectsInvalidRange(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.DataBackfillRun{}, &persistence.DataBackfillItem{})
	svc := NewBackfillService(h.DB, NewStore(h.DB), nil, nil, nil)
	_, err := svc.Run(context.Background(), BackfillRequest{FlowID: 1, From: "2026-10-20", To: "2026-10-10", Source: "backfill"})
	if err == nil {
		t.Fatal("to earlier than from must fail")
	}
}

func floatPtrB(v float64) *float64 { return &v }
