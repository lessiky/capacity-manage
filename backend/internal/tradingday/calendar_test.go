package tradingday_test

import (
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/testutil"
)

func checkDate(t *testing.T, got time.Time, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s := got.Format("2006-01-02"); s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

func checkDateTime(t *testing.T, got time.Time, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s := got.Format("2006-01-02 15:04"); s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

func TestIsTradingDaySeeded(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()

	if ok, err := cal.IsTradingDay(ctx, "SH", time.Date(2026, 10, 13, 10, 0, 0, 0, time.Local)); err != nil || ok {
		t.Fatalf("seeded holiday 2026-10-13: ok=%v err=%v", ok, err)
	}
	if ok, err := cal.IsTradingDay(ctx, "SH", time.Date(2026, 10, 12, 10, 0, 0, 0, time.Local)); err != nil || !ok {
		t.Fatalf("seeded trading day 2026-10-12: ok=%v err=%v", ok, err)
	}
}

func TestIsTradingDayWeekendFallback(t *testing.T) {
	h := testutil.NewCalHarness(t)
	cal := h.Calendar()
	ctx := context.Background()

	sat := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)
	mon := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)
	if ok, err := cal.IsTradingDay(ctx, "SH", sat); err != nil || ok {
		t.Fatalf("weekend fallback for Saturday: ok=%v err=%v", ok, err)
	}
	if ok, err := cal.IsTradingDay(ctx, "SH", mon); err != nil || !ok {
		t.Fatalf("weekend fallback for Monday: ok=%v err=%v", ok, err)
	}
}

func TestNextPrevTradingDay(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()

	// Mon 10-12 -> next is Wed 10-14 (holiday 10-13 skipped)
	got, err := cal.NextTradingDay(ctx, "SH", time.Date(2026, 10, 12, 0, 0, 0, 0, time.Local))
	checkDate(t, got, err, "2026-10-14")
	// Wed 10-14 -> prev is Mon 10-12
	got, err = cal.PrevTradingDay(ctx, "SH", time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local))
	checkDate(t, got, err, "2026-10-12")
	// Fri 10-09 -> next is Mon 10-12 (weekend skipped)
	got, err = cal.NextTradingDay(ctx, "SH", time.Date(2026, 10, 9, 0, 0, 0, 0, time.Local))
	checkDate(t, got, err, "2026-10-12")
}

func TestOffset(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()

	got, err := cal.Offset(ctx, "SH", time.Date(2026, 10, 12, 0, 0, 0, 0, time.Local), -1)
	checkDate(t, got, err, "2026-10-09")
	got, err = cal.Offset(ctx, "SH", time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local), 2)
	checkDate(t, got, err, "2026-10-16")
	got, err = cal.Offset(ctx, "SH", time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local), 0)
	checkDate(t, got, err, "2026-10-14")
}

func TestOffsetUnseededFallbackProbe(t *testing.T) {
	// Empty calendar: derivation falls back to the weekend heuristic.
	h := testutil.NewCalHarness(t)
	cal := h.Calendar()
	ctx := context.Background()

	got, err := cal.NextTradingDay(ctx, "SH", time.Date(2026, 10, 9, 0, 0, 0, 0, time.Local))
	checkDate(t, got, err, "2026-10-12")
	got, err = cal.PrevTradingDay(ctx, "SH", time.Date(2026, 10, 12, 0, 0, 0, 0, time.Local))
	checkDate(t, got, err, "2026-10-09")
}

func TestNextTriggerAfter(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()

	// 10-14 11:00, window 10:00 already passed -> next trading day 10-15 10:00
	got, err := cal.NextTriggerAfter(ctx, "SH", "10:00", time.Date(2026, 10, 14, 11, 0, 0, 0, time.Local))
	checkDateTime(t, got, err, "2026-10-15 10:00")
	// 10-14 09:00, window 10:00 still ahead -> same day
	got, err = cal.NextTriggerAfter(ctx, "SH", "10:00", time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local))
	checkDateTime(t, got, err, "2026-10-14 10:00")
	// Saturday 10-17 -> Monday 10-19 10:00
	got, err = cal.NextTriggerAfter(ctx, "SH", "10:00", time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local))
	checkDateTime(t, got, err, "2026-10-19 10:00")
}

// 调度窗口: 范围 "HH:MM-HH:MM" 取起点触发; 旧版单时刻 "HH:MM" 行为不变
func TestNextTriggerAfterWindowRange(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()
	// 2026-10-14(周三, 交易日) 10:00 之后
	from := time.Date(2026, 10, 14, 10, 0, 0, 0, time.Local)

	// 范围 00:00-09:00: 今日 00:00 已过 → 下一交易日 2026-10-15 00:00
	nxt, err := cal.NextTriggerAfter(ctx, "SH", "00:00-09:00", from)
	checkDateTime(t, nxt, err, "2026-10-15 00:00")

	// 范围 10:30-12:00: 今日 10:30 未到 → 当日 10:30 触发
	nxt, err = cal.NextTriggerAfter(ctx, "SH", "10:30-12:00", from)
	checkDateTime(t, nxt, err, "2026-10-14 10:30")

	// 旧版单时刻 17:30: 行为不变
	nxt, err = cal.NextTriggerAfter(ctx, "SH", "17:30", from)
	checkDateTime(t, nxt, err, "2026-10-14 17:30")

	// 非法窗口 → 报错
	if _, err := cal.NextTriggerAfter(ctx, "SH", "9:61", from); err == nil {
		t.Fatal("expected error for bad minute")
	}
	if _, err := cal.NextTriggerAfter(ctx, "SH", "bad", from); err == nil {
		t.Fatal("expected error for bad format")
	}
}
