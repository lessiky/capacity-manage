package scheduler

import (
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"
)

func TestForecastShouldStartSkipsCompletedAndActiveRuns(t *testing.T) {
	bizDate := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	now := time.Date(2026, 10, 21, 0, 45, 0, 0, time.Local)

	t.Run("successful batch for the business date", func(t *testing.T) {
		h := testutil.NewDBHarness(t)
		h.Migrate(&persistence.ForecastRun{})
		if err := h.DB.Create(&persistence.ForecastRun{
			RunDate: bizDate, Status: "success",
		}).Error; err != nil {
			t.Fatal(err)
		}
		s := New(h.DB, nil, nil, nil, nil)
		if s.forecastShouldStart(bizDate, now) {
			t.Fatal("expected completed daily forecast to be skipped")
		}
	})

	t.Run("fresh running batch", func(t *testing.T) {
		h := testutil.NewDBHarness(t)
		h.Migrate(&persistence.ForecastRun{})
		started := now.Add(-15 * time.Minute)
		if err := h.DB.Create(&persistence.ForecastRun{
			RunDate: bizDate, Status: "running", StartedAt: &started,
		}).Error; err != nil {
			t.Fatal(err)
		}
		s := New(h.DB, nil, nil, nil, nil)
		if s.forecastShouldStart(bizDate, now) {
			t.Fatal("expected active forecast to be skipped")
		}
	})

	t.Run("stale running batch can be retried", func(t *testing.T) {
		h := testutil.NewDBHarness(t)
		h.Migrate(&persistence.ForecastRun{})
		started := now.Add(-3 * time.Hour)
		if err := h.DB.Create(&persistence.ForecastRun{
			RunDate: bizDate, Status: "running", StartedAt: &started,
		}).Error; err != nil {
			t.Fatal(err)
		}
		s := New(h.DB, nil, nil, nil, nil)
		if !s.forecastShouldStart(bizDate, now) {
			t.Fatal("expected stale running forecast to be retryable")
		}
	})
}
