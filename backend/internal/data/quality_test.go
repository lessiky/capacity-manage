package data

import (
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
)

func TestCalculateQualityPerfectAndFlaggedRanges(t *testing.T) {
	perfect := CalculateQuality(QualityInput{
		RangeDays: 10, ValidDays: 10, TimelyDays: 10,
		TotalPoints: 100, AnomalyPoints: 0, DuplicatePoints: 0,
		LatestBizDate: time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local),
		RangeEnd:      time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local),
	})
	if perfect.Score != 1 || len(perfect.Flags) != 0 {
		t.Fatalf("perfect = %+v", perfect)
	}
	flagged := CalculateQuality(QualityInput{
		RangeDays: 10, ValidDays: 6, TimelyDays: 7,
		TotalPoints: 100, AnomalyPoints: 25, DuplicatePoints: 5,
		LatestBizDate: time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local),
		RangeEnd:      time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local),
	})
	if flagged.Score >= 0.8 {
		t.Fatalf("flagged score = %.4f, want below 0.8", flagged.Score)
	}
	for _, flag := range []string{"missing_days", "late_days", "anomaly_ratio", "duplicates", "stale"} {
		if !containsString(flagged.Flags, flag) {
			t.Fatalf("flags = %v, missing %s", flagged.Flags, flag)
		}
	}
	empty := CalculateQuality(QualityInput{RangeDays: 5, RangeEnd: time.Now()})
	if empty.Score != 0 || !containsString(empty.Flags, "no_data") {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestQualityServiceEvaluatePersistsDailyScore(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.MetricValueDay{}, &persistence.MetricValueRT{}, &persistence.MetricQualityDay{})
	_ = h.DB.Create(&persistence.CapacityMetric{MetricCode: "CPU", SystemCode: "SYS", MetricKind: "normal", Status: "active"}).Error
	for day := 18; day <= 20; day++ {
		v := 80.0
		ts := time.Date(2026, 10, day, 0, 0, 0, 0, time.Local)
		_ = h.DB.Create(&persistence.MetricValueDay{
			MetricCode: "CPU", BizDate: ts, StatValue: &v, Level: "warn",
			PointCount: 1, Source: "collect", UpdatedAt: ts.Add(2 * time.Hour),
		}).Error
		_ = h.DB.Create(&persistence.MetricValueRT{
			MetricCode: "CPU", Ts: ts.Add(1 * time.Hour), CleanValue: &v,
			RawValue: &v, JudgeLevel: "warn",
		}).Error
	}
	svc := NewQualityService(h.DB, system.NewParams(h.DB))
	res, err := svc.Evaluate(context.Background(), "CPU",
		time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local),
		time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if res.Score <= 0 || len(res.Days) != 3 {
		t.Fatalf("result = %+v", res)
	}
	var n int64
	if err := h.DB.Model(&persistence.MetricQualityDay{}).Where("metric_code = ?", "CPU").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("quality rows = %d, want 3", n)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
