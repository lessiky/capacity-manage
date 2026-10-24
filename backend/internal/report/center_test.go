package report

import (
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"
)

func centerHarness(t *testing.T) *testutil.DBHarness {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.BusinessSystem{},
		&persistence.CapacityMetric{},
		&persistence.MetricValueDay{},
		&persistence.AlertRecord{},
		&persistence.ForecastRun{},
		&persistence.ForecastResult{},
	)
	return h
}

func seedCenterMetric(t *testing.T, h *testutil.DBHarness, code, system string, warn, danger float64) {
	t.Helper()
	if err := h.DB.Create(&persistence.BusinessSystem{SystemCode: system, Name: system, Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: code, Name: code, SystemCode: system, Category: "storage",
		MetricKind: "normal", MetricType: "usage", Unit: "%",
		JudgeDirection: "higher_worse", ThresholdMode: "absolute",
		WarnLine: &warn, DangerLine: &danger, Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedCenterValues(t *testing.T, h *testutil.DBHarness, code string, end time.Time, values []float64) {
	t.Helper()
	for i, value := range values {
		day := end.AddDate(0, 0, -len(values)+1+i)
		if err := h.DB.Create(&persistence.MetricValueDay{
			MetricCode: code, BizDate: day, StatValue: &value, WaterLevel: &value,
			Level: "normal", PointCount: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerateWeeklyReportAppliesDataScopeAndGrowth(t *testing.T) {
	h := centerHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedCenterMetric(t, h, "M7_CAP", "SYS_A", 80, 95)
	seedCenterMetric(t, h, "M7_OTHER", "SYS_B", 80, 95)
	seedCenterValues(t, h, "M7_CAP", end, []float64{60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73})
	seedCenterValues(t, h, "M7_OTHER", end, []float64{60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60})

	svc := NewService(h.DB, nil)
	data, err := svc.Generate(context.Background(), persistence.ReportDefinition{
		ReportType: "weekly", PeriodWindow: 7, GroupDimension: "system",
	}, ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -6), End: end}, []string{"SYS_A"})
	if err != nil {
		t.Fatal(err)
	}

	if data.ReportType != "weekly" || len(data.Rows) != 1 {
		t.Fatalf("data=%+v", data)
	}
	if data.Rows[0]["metricCode"] != "M7_CAP" {
		t.Fatalf("scope leaked: %+v", data.Rows)
	}
	if numberValue(data.Rows[0]["growthRate"]) <= 0 {
		t.Fatalf("growthRate=%v", data.Rows[0]["growthRate"])
	}
	if len(data.Columns) < 6 {
		t.Fatalf("columns=%v", data.Columns)
	}

	empty, err := svc.Generate(context.Background(), persistence.ReportDefinition{
		ReportType: "weekly", PeriodWindow: 7,
	}, ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -6), End: end}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Rows) != 0 {
		t.Fatalf("empty scope returned rows: %+v", empty.Rows)
	}
}

func TestGenerateCapacityAnalysisAndAlertSummary(t *testing.T) {
	h := centerHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedCenterMetric(t, h, "M7_CAP", "SYS_A", 80, 95)
	seedCenterValues(t, h, "M7_CAP", end, []float64{70, 85, 90})
	closed := end.Add(-2 * time.Hour)
	if err := h.DB.Create(&persistence.AlertRecord{
		AlertNo: "M7-A1", MetricCode: "M7_CAP", SystemCode: "SYS_A",
		Level: "p1", AlertType: "threshold", Status: "closed",
		TriggerAt: end.Add(-24 * time.Hour), ClaimedAt: ptrTime(end.Add(-20 * time.Hour)),
		ClosedAt: &closed, EscalationCount: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(h.DB, nil)
	analysis, err := svc.Generate(context.Background(), persistence.ReportDefinition{
		ReportType: "capacity_analysis", PeriodWindow: 7,
	}, ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -6), End: end}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Rows) != 1 || numberValue(analysis.Rows[0]["alertCount"]) != 1 {
		t.Fatalf("analysis=%+v", analysis)
	}

	alerts, err := svc.Generate(context.Background(), persistence.ReportDefinition{
		ReportType: "alert_summary", PeriodWindow: 7,
	}, ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -6), End: end}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts.Rows) != 1 || numberValue(alerts.Rows[0]["alertCount"]) != 1 {
		t.Fatalf("alerts=%+v", alerts)
	}
	if numberValue(alerts.Rows[0]["mttaMinutes"]) <= 0 {
		t.Fatalf("mtta=%v", alerts.Rows[0]["mttaMinutes"])
	}
}

func TestGenerateForecastReport(t *testing.T) {
	h := centerHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedCenterMetric(t, h, "M7_FORECAST", "SYS_A", 80, 95)
	run := persistence.ForecastRun{
		RunDate: end, ModelVersion: "stat-v1.0", DataEndDate: end,
		Status: "success", MetricsTotal: 1, MetricsDone: 1,
	}
	if err := h.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	eta := end.AddDate(0, 0, 12)
	risk := 0.86
	quality := 0.96
	if err := h.DB.Create(&persistence.ForecastResult{
		RunID: run.ID, MetricCode: "M7_FORECAST", HorizonDays: 30, ModelType: "linear",
		Status: "success", ETAWarnDate: &eta, RiskScore: &risk, DataQuality: &quality,
		Explanation: "稳定增长", Curve: "[]", InputDigest: "digest",
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(h.DB, nil)
	data, err := svc.Generate(context.Background(), persistence.ReportDefinition{
		ReportType: "forecast", PeriodWindow: 30, IncludeForecast: true,
	}, ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -29), End: end}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 1 || numberValue(data.Rows[0]["riskScore"]) != risk {
		t.Fatalf("forecast=%+v", data)
	}
	if data.Rows[0]["etaWarnDate"] == nil || data.Rows[0]["explanation"] != "稳定增长" {
		t.Fatalf("forecast row=%+v", data.Rows[0])
	}
}

func TestRunCenterPersistsFilesAndExports(t *testing.T) {
	h := centerHarness(t)
	h.Migrate(&persistence.ReportDefinition{}, &persistence.ReportRun{})
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedCenterMetric(t, h, "M7_RUN", "SYS_A", 80, 95)
	seedCenterValues(t, h, "M7_RUN", end, []float64{70, 75, 85})
	def := persistence.ReportDefinition{
		Name: "M7容量分析", ReportType: "capacity_analysis", PeriodWindow: 7,
		SystemScope: "[]", MetricScope: "[]", ChartOptions: "{}", Enabled: true,
	}
	if err := h.DB.Create(&def).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(h.DB, nil)
	run, err := svc.RunCenter(context.Background(), def,
		ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -2), End: end}, "tester", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.MetricsTotal != 1 || run.Files == "" {
		t.Fatalf("run=%+v", run)
	}
	var stored persistence.ReportRun
	if err := h.DB.First(&stored, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "success" {
		t.Fatalf("stored=%+v", stored)
	}

	data, err := svc.Generate(context.Background(), def,
		ReportPeriod{RunDate: end, Start: end.AddDate(0, 0, -2), End: end}, nil)
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := BuildCenterExcel(data)
	if err != nil || len(xlsx) == 0 {
		t.Fatalf("xlsx err=%v len=%d", err, len(xlsx))
	}
	pdf, err := BuildCenterPDF(data)
	if err != nil || len(pdf) == 0 {
		t.Fatalf("pdf err=%v len=%d", err, len(pdf))
	}
}

func TestComputeNextRun(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	next := ComputeNextRun(persistence.ReportSubscription{
		ScheduleType: "daily", ScheduleTime: "08:30", Timezone: "Asia/Shanghai",
	}, now)
	if next == nil || next.Day() != 21 || next.Hour() != 8 || next.Minute() != 30 {
		t.Fatalf("next=%v", next)
	}
	next = ComputeNextRun(persistence.ReportSubscription{
		ScheduleType: "weekly", ScheduleTime: "10:00", Weekday: 2, Timezone: "Asia/Shanghai",
	}, now)
	if next == nil || next.Weekday() != time.Tuesday {
		t.Fatalf("weekly next=%v", next)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
