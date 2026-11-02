package scheduler

import (
	"testing"
	"time"

	"capacity-manage/backend/internal/testutil"
)

// 触发时间应精确到秒, 不能被 30 秒保护窗口跳过.
func TestTradingDayTriggerUsesSeconds(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	trigger := tradingDayTrigger{cal: h.Calendar(), market: "SH", window: "10:00:30"}
	after := time.Date(2026, 10, 14, 10, 0, 0, 0, time.Local)
	want := time.Date(2026, 10, 14, 10, 0, 30, 0, time.Local)

	got := trigger.Next(after)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
