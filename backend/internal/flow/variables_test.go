package flow

import (
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/testutil"
)

func TestResolveVarsAndRender(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()
	base := time.Date(2026, 10, 14, 10, 30, 0, 0, time.Local)

	vars, err := ResolveVars(ctx, cal, "SH", base, "CORE", "F1", "M1", true)
	if err != nil {
		t.Fatal(err)
	}
	if vars["biz_date"] != "2026-10-14" {
		t.Fatalf("biz_date = %s", vars["biz_date"])
	}
	if vars["hour"] != "10" {
		t.Fatalf("hour = %s", vars["hour"])
	}
	if vars["prev_trading_day"] != "2026-10-12" {
		t.Fatalf("prev_trading_day = %s (holiday 10-13 must be skipped)", vars["prev_trading_day"])
	}
	if vars["next_trading_day"] != "2026-10-15" {
		t.Fatalf("next_trading_day = %s", vars["next_trading_day"])
	}

	r := NewResolver(ctx, cal, "SH", base)
	out, err := r.Render("dt='${biz_date}', prev='${trading_day_offset:-1}', next='${trading_day_offset:1}', sys='${system_code}'", vars)
	if err != nil {
		t.Fatal(err)
	}
	want := "dt='2026-10-14', prev='2026-10-12', next='2026-10-15', sys='CORE'"
	if out != want {
		t.Fatalf("render:\n got %s\nwant %s", out, want)
	}

	if _, err := r.Render("x=${unknown_var}", vars); err == nil {
		t.Fatal("expected error for unknown variable")
	}
}

func TestRenderDefaultMarket(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()
	base := time.Date(2026, 10, 14, 8, 0, 0, 0, time.Local)

	vars, err := ResolveVars(ctx, cal, "", base, "CORE", "F1", "M1", false)
	if err != nil {
		t.Fatal(err)
	}
	if vars["prev_trading_day"] != "2026-10-12" {
		t.Fatalf("default market SH not applied: %s", vars["prev_trading_day"])
	}
}