package scheduler

import (
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
)

func TestDailyJobDueAfterConfiguredTime(t *testing.T) {
	last := time.Date(2026, 10, 29, 8, 59, 0, 0, time.Local)
	now := time.Date(2026, 10, 29, 9, 0, 1, 0, time.Local)

	if !dailyJobDue(last, now, "09:00") {
		t.Fatal("daily job should be due after configured time")
	}
}

func TestDailyJobNotDueBeforeConfiguredTime(t *testing.T) {
	last := time.Date(2026, 10, 29, 8, 58, 0, 0, time.Local)
	now := time.Date(2026, 10, 29, 8, 59, 59, 0, time.Local)

	if dailyJobDue(last, now, "09:00") {
		t.Fatal("daily job should not be due before configured time")
	}
}

func TestDailyJobDoesNotCatchUpAfterStartup(t *testing.T) {
	now := time.Date(2026, 10, 29, 9, 5, 0, 0, time.Local)

	if dailyJobDue(now, now.Add(time.Second), "09:00") {
		t.Fatal("daily job should not catch up when scheduler starts after configured time")
	}
}

func TestDailyJobRejectsInvalidConfiguredTime(t *testing.T) {
	last := time.Date(2026, 10, 29, 8, 0, 0, 0, time.Local)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.Local)

	if dailyJobDue(last, now, "25:00") {
		t.Fatal("daily job should not run with invalid configured time")
	}
}

func TestMissingCheckDueUsesConfiguredSystemParam(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.SysParam{})
	if err := h.DB.Create(&persistence.SysParam{
		Key: "missing_check_time", Value: "10:30", Category: "schedule",
	}).Error; err != nil {
		t.Fatal(err)
	}
	s := &Scheduler{params: system.NewParams(h.DB)}
	last := time.Date(2026, 10, 29, 10, 29, 0, 0, time.Local)
	now := time.Date(2026, 10, 29, 10, 30, 1, 0, time.Local)

	if !s.missingCheckDue(last, now) {
		t.Fatal("missing check should use the configured system parameter time")
	}
}
