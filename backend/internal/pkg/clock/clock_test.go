package clock

import (
	"testing"
	"time"
)

// 回归: Truncate(24h) 在非 UTC 时区会把日期错移到前一天, DayStart 必须返回正确本地日期。
func TestDayStart(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 10, 16, 0, 0, 0, 0, loc), "2026-10-16"},
		{time.Date(2026, 10, 16, 8, 30, 15, 0, loc), "2026-10-16"},
		{time.Date(2026, 1, 1, 23, 59, 59, 0, loc), "2026-01-01"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01"},
	}
	for _, tc := range cases {
		got := DayStart(tc.in)
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("DayStart(%v) = %s, want %s", tc.in, got.Format("2006-01-02"), tc.want)
		}
		if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
			t.Errorf("DayStart(%v) 不是 0 点: %v", tc.in, got)
		}
	}
}
