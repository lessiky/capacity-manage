package flow

import (
	"context"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/testutil"
)

func TestDateLayout(t *testing.T) {
	cases := map[string]string{
		"YYYYMMDD":   "20060102",
		"YYYY-MM-DD": "2006-01-02",
		"YYYY/MM/DD": "2006/01/02",
		"YYYY.MM.DD": "2006.01.02",
		"YYYYMM":     "200601",
		"":           "2006-01-02",
	}
	for in, want := range cases {
		got, err := DateLayout(in)
		if err != nil {
			t.Fatalf("DateLayout(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("DateLayout(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"DDMM", "YYYYMMDDHH"} {
		if _, err := DateLayout(bad); err == nil {
			t.Fatalf("DateLayout(%q) should fail", bad)
		}
	}
}

func TestFormatDateValue(t *testing.T) {
	got, err := FormatDateValue("2026-10-14", "YYYYMMDD")
	if err != nil || got != "20261014" {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = FormatDateValue("2026-10-14", "YYYY-MM-DD")
	if err != nil || got != "2026-10-14" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := FormatDateValue("2026/10/14", "YYYYMMDD"); err == nil {
		t.Fatal("expected error for non-YYYY-MM-DD input")
	}
}

func TestRenderTemplateDateFormat(t *testing.T) {
	vars := map[string]string{
		"biz_date": "2026-10-14", "prev_trading_day": "2026-10-12",
		"next_trading_day": "2026-10-15", "system_code": "CORE", "hour": "17",
	}
	out, err := RenderTemplate(
		"WHERE dt='${biz_date:YYYYMMDD}' AND prev='${prev_trading_day:YYYY-MM-DD}' AND sys='${system_code}' AND h='${hour}'",
		vars, escapeSQLValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "WHERE dt='20261014' AND prev='2026-10-12' AND sys='CORE' AND h='17'"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestRenderTemplateLenientUnknown(t *testing.T) {
	vars := map[string]string{"biz_date": "2026-10-14", "system_code": "CORE"}
	out, err := RenderTemplate("a=${nope}, b='${biz_date:DDMM}'", vars, nil, nil)
	if err == nil {
		t.Fatal("expected error for bad format")
	}
	if !strings.Contains(out, "${nope}") {
		t.Fatalf("unknown placeholder not preserved: %q", out)
	}
	if _, err := RenderTemplate("x=${system_code:YYYYMMDD}", vars, nil, nil); err == nil {
		t.Fatal("expected error for format arg on non-date var")
	}
}

func TestRenderTemplateOffsetWithFormat(t *testing.T) {
	base := time.Date(2026, 10, 14, 17, 30, 0, 0, time.Local)
	offsetFn := func(n int, format string) (string, bool) {
		if n != -1 && n != 1 {
			return "", false
		}
		d := base.AddDate(0, 0, n)
		s := d.Format("2006-01-02")
		if format != "" {
			var err error
			s, err = FormatDateValue(s, format)
			if err != nil {
				return "", false
			}
		}
		return s, true
	}
	vars := map[string]string{"biz_date": base.Format("2006-01-02")}
	out, err := RenderTemplate("p='${trading_day_offset:-1:YYYYMMDD}', n='${trading_day_offset:1}', z='${trading_day_offset:99}'",
		vars, escapeSQLValue, offsetFn)
	if err != nil {
		t.Fatal(err)
	}
	want := "p='20261013', n='2026-10-15', z='${trading_day_offset:99}'"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestResolverRenderDateFormat(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := h.Calendar()
	ctx := context.Background()
	base := time.Date(2026, 10, 14, 10, 30, 0, 0, time.Local)
	vars, err := ResolveVars(ctx, cal, "SH", base, "CORE", "F1", "M1", true)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(ctx, cal, "SH", base)
	out, err := r.Render("a='${biz_date:YYYYMMDD}' b='${prev_trading_day:YYYY/MM/DD}' c='${trading_day_offset:-1:YYYYMMDD}'", vars)
	if err != nil {
		t.Fatal(err)
	}
	want := "a='20261014' b='2026/10/12' c='20261012'"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}