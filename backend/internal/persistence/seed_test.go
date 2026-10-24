package persistence

import (
	"testing"
	"time"
)

// 回归: 曾因 Truncate(24h) 时区错移导致整表日期错位一天（周五被标休市/周日被标交易）。
func TestSeedCalendarRow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	cases := []struct {
		date      string
		wantTrade bool
		wantType  string
	}{
		{"2026-10-15", true, "trading"},  // 周四
		{"2026-10-16", true, "trading"},  // 周五（历史缺陷: 曾被错标为非交易日）
		{"2026-10-17", false, "weekend"}, // 周六
		{"2026-10-18", false, "weekend"}, // 周日（历史缺陷: 曾被错标为交易日）
		{"2026-10-19", true, "trading"},  // 周一
		{"2026-01-01", false, "holiday"}, // 元旦
		{"2026-10-01", false, "holiday"}, // 国庆
		{"2027-02-08", false, "holiday"}, // 春节
	}
	for _, tc := range cases {
		d, err := time.ParseInLocation("2006-01-02", tc.date, loc)
		if err != nil {
			t.Fatal(err)
		}
		got := seedCalendarRow(d)
		if got.IsTrading != tc.wantTrade {
			t.Errorf("%s isTrading=%v, want %v", tc.date, got.IsTrading, tc.wantTrade)
		}
		if got.DayType != tc.wantType {
			t.Errorf("%s dayType=%s, want %s", tc.date, got.DayType, tc.wantType)
		}
		if got.CalDate.Format("2006-01-02") != tc.date {
			t.Errorf("%s CalDate=%s 日期错位", tc.date, got.CalDate.Format("2006-01-02 15:04:05"))
		}
	}
}
