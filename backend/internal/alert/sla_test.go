package alert

import (
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
)

func tp(base time.Time, min int) *time.Time {
	v := base.Add(time.Duration(min) * time.Minute)
	return &v
}

func TestComputeSLARates(t *testing.T) {
	targets := map[string]slaTarget{
		"p1": {claim: 30, resolve: 240},
		"p2": {claim: 120, resolve: 1440},
		"p3": {claim: 240, resolve: 1440},
		"p4": {claim: 1440, resolve: 7200},
	}
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	rows := []slaRow{
		// p1-1: 认领 10min 闭环 60min → 时限内
		{Level: "p1", TriggerAt: base, ClaimedAt: tp(base, 10), EndAt: tp(base, 70)},
		// p1-2: 认领 60min 超时 → 时限外
		{Level: "p1", TriggerAt: base, ClaimedAt: tp(base, 60), EndAt: tp(base, 90)},
		// p1-3: 未认领 → 计入应闭环, 不计 MTTA
		{Level: "p1", TriggerAt: base},
		// p2-1: 认领 50min 闭环 1000min(≤1440) → 时限内
		{Level: "p2", TriggerAt: base, ClaimedAt: tp(base, 50), EndAt: tp(base, 1050)},
		// p2-2: 认领 200min 超时 → 时限外
		{Level: "p2", TriggerAt: base, ClaimedAt: tp(base, 200), EndAt: tp(base, 400)},
		// p3-1: 认领 300min(>240) 超时 → 时限外
		{Level: "p3", TriggerAt: base, ClaimedAt: tp(base, 300), EndAt: tp(base, 500)},
		// p4-1: 认领 20min 闭环 60min → 时限内
		{Level: "p4", TriggerAt: base, ClaimedAt: tp(base, 20), EndAt: tp(base, 80)},
	}
	out := ComputeSLA(rows, targets)
	if len(out) != 4 {
		t.Fatalf("levels = %d, want 4", len(out))
	}
	byLv := map[string]SLAByLevel{}
	for _, s := range out {
		byLv[s.Level] = s
	}
	p1 := byLv["p1"]
	if p1.Total != 3 || p1.Closed != 2 || p1.InTime != 1 {
		t.Fatalf("p1 = %+v, want total=3 closed=2 inTime=1", p1)
	}
	if p1.MTTAMin == nil || *p1.MTTAMin != 35 {
		t.Fatalf("p1 mtta = %v, want 35", p1.MTTAMin)
	}
	if p1.MTTRMin == nil || *p1.MTTRMin != 45 {
		t.Fatalf("p1 mttr = %v, want 45", p1.MTTRMin)
	}
	if p1.Rate == nil || *p1.Rate != 33.3 {
		t.Fatalf("p1 rate = %v, want 33.3", p1.Rate)
	}
	p2 := byLv["p2"]
	if p2.Total != 2 || p2.Closed != 2 || p2.InTime != 1 {
		t.Fatalf("p2 = %+v, want total=2 closed=2 inTime=1", p2)
	}
	if p2.MTTAMin == nil || *p2.MTTAMin != 125 {
		t.Fatalf("p2 mtta = %v, want 125", p2.MTTAMin)
	}
	p3 := byLv["p3"]
	if p3.InTime != 0 || p3.Rate == nil || *p3.Rate != 0 {
		t.Fatalf("p3 = %+v, want inTime=0 rate=0", p3)
	}
	p4 := byLv["p4"]
	if p4.Total != 1 || p4.InTime != 1 || p4.Rate == nil || *p4.Rate != 100 {
		t.Fatalf("p4 = %+v, want total=1 inTime=1 rate=100", p4)
	}
}

func TestComputeSLAEmpty(t *testing.T) {
	out := ComputeSLA(nil, map[string]slaTarget{"p1": {claim: 30, resolve: 240}})
	if len(out) != 0 {
		t.Fatalf("expected empty, got %+v", out)
	}
}

func TestBuildTimeline(t *testing.T) {
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	claimed := base.Add(10 * time.Minute)
	recovered := base.Add(2 * time.Hour)
	closed := base.Add(3 * time.Hour)
	v85, v80, wl := 85.0, 80.0, 0.85
	rec := persistence.AlertRecord{
		AlertNo: "ALTEST001", Level: "p1", Status: "closed",
		TriggerAt: base, FirstValue: &v85, Threshold: &v80, WaterLevel: &wl,
		ClaimedBy: "alice", ClaimedAt: &claimed,
		HandleNote:  `[{"time":"2026-10-15T09:20:00+08:00","user":"alice","note":"investigating cause"},{"time":"2026-10-15T10:30:00+08:00","user":"bob","note":"capacity expanded"}]`,
		RecoveredAt: &recovered,
		ClosedBy:    "alice", ClosedAt: &closed, CloseResult: "capacity expanded, back to normal",
	}
	items := BuildTimeline(&rec)
	// trigger + claim + 2 note + recover + close = 6
	if len(items) != 6 {
		t.Fatalf("timeline items = %d, want 6: %+v", len(items), items)
	}
	wantEvents := []string{"trigger", "claim", "note", "note", "recover", "close"}
	for i, w := range wantEvents {
		if items[i].Event != w {
			t.Fatalf("item %d event = %s, want %s", i, items[i].Event, w)
		}
	}
	if items[0].Detail == "" || items[5].Detail == "" {
		t.Fatalf("trigger/close detail missing: %+v", items)
	}
	// 顺序按时间升序
	for i := 1; i < len(items); i++ {
		if items[i].Time < items[i-1].Time {
			t.Fatalf("timeline not sorted: %s after %s", items[i].Time, items[i-1].Time)
		}
	}
}
