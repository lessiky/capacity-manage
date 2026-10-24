package forecast

import (
	"context"
	"errors"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"
)

func seedForecastMetric(t *testing.T, h *testutil.DBHarness, code string, warn, danger float64) {
	t.Helper()
	if err := h.DB.Create(&persistence.BusinessSystem{
		SystemCode: "SYS_" + code, Name: "Forecast " + code, Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: code, Name: "Forecast " + code, SystemCode: "SYS_" + code,
		MetricKind: "normal", MetricType: "usage", Unit: "%",
		JudgeDirection: "higher_worse", ThresholdMode: "absolute",
		WarnLine: &warn, DangerLine: &danger, Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedForecastDays(t *testing.T, h *testutil.DBHarness, code string, end time.Time, days int, startValue, step float64, quality float64) {
	t.Helper()
	for i := 0; i < days; i++ {
		day := end.AddDate(0, 0, -days+1+i)
		value := startValue + float64(i)*step
		if err := h.DB.Create(&persistence.MetricValueDay{
			MetricCode: code, BizDate: day, StatValue: &value, WaterLevel: &value,
			Level: "normal", PointCount: 1, Source: "collect",
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.DB.Create(&persistence.MetricQualityDay{
			MetricCode: code, BizDate: day, SystemCode: "SYS_" + code,
			Score: quality, Completeness: 1, Timeliness: 1, Freshness: 1,
			ValidPoints: 1, TotalPoints: 1, Flags: `[]`, EvaluatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func forecastHarness(t *testing.T) *testutil.DBHarness {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.BusinessSystem{},
		&persistence.CapacityMetric{},
		&persistence.MetricValueDay{},
		&persistence.MetricQualityDay{},
		&persistence.ForecastRun{},
		&persistence.ForecastResult{},
		&persistence.ForecastEvent{},
		&persistence.ForecastAIAdjustment{},
	)
	return h
}

func TestRunDailyPersistsBothHorizonsAndIsIdempotent(t *testing.T) {
	h := forecastHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_RISING", 80, 95)
	seedForecastDays(t, h, "M7_RISING", end, 100, 50, 0.45, 1)

	svc := NewService(h.DB)
	if err := svc.RunDaily(context.Background(), end); err != nil {
		t.Fatal(err)
	}
	var first persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_RISING").First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RunDaily(context.Background(), end); err != nil {
		t.Fatal(err)
	}

	var runs []persistence.ForecastRun
	if err := h.DB.Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "success" {
		t.Fatalf("runs=%+v", runs)
	}
	var results []persistence.ForecastResult
	if err := h.DB.Where("metric_code = ?", "M7_RISING").Order("horizon_days").Find(&results).Error; err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%d %+v", len(results), results)
	}
	if results[0].HorizonDays != 30 || results[1].HorizonDays != 90 {
		t.Fatalf("horizons=%d,%d", results[0].HorizonDays, results[1].HorizonDays)
	}
	for _, result := range results {
		if result.Status != StatusSuccess {
			t.Fatalf("status=%q explanation=%q", result.Status, result.Explanation)
		}
		if result.InputDigest == "" || result.Curve == "" {
			t.Fatalf("missing reproducible fields: %+v", result)
		}
	}
	if results[0].InputDigest != first.InputDigest {
		t.Fatalf("input digest changed across rerun")
	}
}

func TestRunDailyPersistsExplicitSkipReasons(t *testing.T) {
	h := forecastHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_LOW_QUALITY", 80, 95)
	seedForecastDays(t, h, "M7_LOW_QUALITY", end, 60, 50, 0.2, 0.79)
	seedForecastMetric(t, h, "M7_SHORT", 80, 95)
	seedForecastDays(t, h, "M7_SHORT", end, 10, 50, 0.2, 1)

	svc := NewService(h.DB)
	if err := svc.RunDaily(context.Background(), end); err != nil {
		t.Fatal(err)
	}

	var quality persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_LOW_QUALITY").First(&quality).Error; err != nil {
		t.Fatal(err)
	}
	if quality.Status != StatusInsufficientQuality {
		t.Fatalf("quality status=%q", quality.Status)
	}
	var short persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_SHORT").First(&short).Error; err != nil {
		t.Fatal(err)
	}
	if short.Status != StatusInsufficientData {
		t.Fatalf("short status=%q", short.Status)
	}
	var run persistence.ForecastRun
	if err := h.DB.First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.MetricsSkipped != 2 || run.MetricsDone != 0 {
		t.Fatalf("run counters not updated: %+v", run)
	}
}

func TestRunScopedResultOnlyRecomputesSelectedMetrics(t *testing.T) {
	h := forecastHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_SCOPE_A", 80, 95)
	seedForecastMetric(t, h, "M7_SCOPE_B", 80, 95)
	seedForecastDays(t, h, "M7_SCOPE_A", end, 100, 50, 0.45, 1)
	seedForecastDays(t, h, "M7_SCOPE_B", end, 100, 50, 0.45, 1)

	svc := NewService(h.DB)
	if err := svc.RunDaily(context.Background(), end); err != nil {
		t.Fatal(err)
	}

	var beforeA, beforeB persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_SCOPE_A").First(&beforeA).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_SCOPE_B").First(&beforeB).Error; err != nil {
		t.Fatal(err)
	}

	changed := 500.0
	if err := h.DB.Model(&persistence.MetricValueDay{}).
		Where("metric_code = ? AND biz_date = ?", "M7_SCOPE_A", end).
		Update("stat_value", changed).Error; err != nil {
		t.Fatal(err)
	}

	out, err := svc.RunScopedResult(context.Background(), end, RunFilter{MetricCodes: []string{"M7_SCOPE_A"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.ScopeMetricsTotal != 1 || out.ScopeMetricsDone != 1 {
		t.Fatalf("scope counters=%+v", out)
	}
	if out.Run.MetricsTotal != 2 || out.Run.MetricsDone != 2 {
		t.Fatalf("run counters should cover the whole run: %+v", out.Run)
	}

	var afterA, afterB persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_SCOPE_A").First(&afterA).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_SCOPE_B").First(&afterB).Error; err != nil {
		t.Fatal(err)
	}
	if afterA.ID == beforeA.ID || afterA.InputDigest == beforeA.InputDigest {
		t.Fatalf("selected metric was not recomputed: before=%+v after=%+v", beforeA, afterA)
	}
	if afterB.ID != beforeB.ID || afterB.InputDigest != beforeB.InputDigest {
		t.Fatalf("unselected metric changed: before=%+v after=%+v", beforeB, afterB)
	}
}

func TestRunScopedResultFiltersBySystemAndRejectsNoMatch(t *testing.T) {
	h := forecastHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_SCOPE_SYS_A", 80, 95)
	seedForecastMetric(t, h, "M7_SCOPE_SYS_B", 80, 95)
	seedForecastDays(t, h, "M7_SCOPE_SYS_A", end, 100, 50, 0.45, 1)
	seedForecastDays(t, h, "M7_SCOPE_SYS_B", end, 100, 50, 0.45, 1)

	svc := NewService(h.DB)
	if err := svc.RunDaily(context.Background(), end); err != nil {
		t.Fatal(err)
	}
	out, err := svc.RunScopedResult(context.Background(), end, RunFilter{SystemCodes: []string{"SYS_M7_SCOPE_SYS_A"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.ScopeMetricsTotal != 1 || out.Run.MetricsTotal != 2 {
		t.Fatalf("system scope did not match one metric in a two-metric run: %+v", out)
	}

	if _, err := svc.RunScopedResult(context.Background(), end, RunFilter{MetricCodes: []string{"MISSING_METRIC"}}); !errors.Is(err, ErrNoMatchedMetrics) {
		t.Fatalf("expected ErrNoMatchedMetrics, got %v", err)
	}
}
