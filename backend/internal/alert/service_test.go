package alert

import (
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
)

func newAlertHarness(t *testing.T) (*testutil.DBHarness, *Service) {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.AlertRecord{},
		&persistence.BusinessSystem{},
		&persistence.AlertDailyHit{},
		&persistence.AlertEscalationPolicy{},
		&persistence.AlertLevelHistory{},
		&persistence.MetricValueDay{},
	)
	params := system.NewParams(h.DB)
	return h, New(h.DB, params, nil)
}

func metricWithLines(warn, danger float64) *persistence.CapacityMetric {
	w, d := warn, danger
	return &persistence.CapacityMetric{
		MetricCode: "M_ALERT", SystemCode: "SYS_ALERT",
		StatMode: "latest", JudgeDirection: "higher_worse",
		Baseline: func() *float64 { v := 100.0; return &v }(),
		WarnLine: &w, DangerLine: &d, Status: "active",
	}
}

func evalRes(t *testing.T, m *persistence.CapacityMetric, v float64) judge.Result {
	t.Helper()
	j := judge.New(nil, nil)
	res := j.Evaluate(m, &v)
	if res.WaterLevel == nil {
		res.WaterLevel = func() *float64 { x := 0.5; return &x }()
	}
	return res
}

func activeAlerts(t *testing.T, h *testutil.DBHarness) []persistence.AlertRecord {
	t.Helper()
	var recs []persistence.AlertRecord
	if err := h.DB.Where("status IN ('active','processing')").Find(&recs).Error; err != nil {
		t.Fatal(err)
	}
	return recs
}

func durOf(rec persistence.AlertRecord) any {
	if rec.DurationMin == nil {
		return nil
	}
	return *rec.DurationMin
}

func TestOnThresholdCreatesWithDeadline(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	ts := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), ts)
	recs := activeAlerts(t, h)
	if len(recs) != 1 {
		t.Fatalf("active alerts = %d, want 1", len(recs))
	}
	if recs[0].Level != "p3" || recs[0].AlertType != "threshold" {
		t.Fatalf("alert = %+v", recs[0])
	}
	if recs[0].SourceLine != "warn" {
		t.Fatalf("source line = %q, want warn", recs[0].SourceLine)
	}
	// P3 响应时限 240min
	if recs[0].DeadlineAt == nil || !recs[0].DeadlineAt.Equal(ts.Add(240*time.Minute)) {
		t.Fatalf("deadline = %v, want %v", recs[0].DeadlineAt, ts.Add(240*time.Minute))
	}
}

func TestOnThresholdMerges(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), base)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 88), base.Add(10*time.Minute))
	recs := activeAlerts(t, h)
	if len(recs) != 1 {
		t.Fatalf("active alerts = %d, want 1 (merge)", len(recs))
	}
	if recs[0].LastValue == nil || *recs[0].LastValue != 88 {
		t.Fatalf("last value = %v, want 88", recs[0].LastValue)
	}
	if recs[0].DurationMin == nil || *recs[0].DurationMin != 10 {
		t.Fatalf("duration = %v, want 10", recs[0].DurationMin)
	}
}

// 日汇总/补采以业务日 00:00 回放判定, 该时刻早于实时触发时刻,
// 持续时间不得因此倒退。
func TestOnThresholdDurationIgnoresBusinessDayReplay(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	trigger := day.Add(10 * time.Hour)

	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), trigger)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 88), trigger.Add(10*time.Minute))
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 89), day)

	recs := activeAlerts(t, h)
	if len(recs) != 1 {
		t.Fatalf("active alerts = %d, want 1", len(recs))
	}
	if recs[0].DurationMin == nil || *recs[0].DurationMin != 10 {
		t.Fatalf("duration = %v, want 10 (业务日回放不得倒退)", durOf(recs[0]))
	}
}

// 首个判定来自实时点, 之后只有更早的业务日回放时, 持续时长不得为负。
func TestOnThresholdDurationNeverNegative(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	trigger := day.Add(10 * time.Hour)

	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), trigger)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 88), day)

	recs := activeAlerts(t, h)
	if len(recs) != 1 {
		t.Fatalf("active alerts = %d, want 1", len(recs))
	}
	if recs[0].DurationMin == nil || *recs[0].DurationMin < 0 {
		t.Fatalf("duration = %v, want >= 0", durOf(recs[0]))
	}
}

func TestOnThresholdDangerMergesWithoutDuplicateAlert(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), base)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 95), base.Add(5*time.Minute))
	var all []persistence.AlertRecord
	if err := h.DB.Order("id").Find(&all).Error; err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("alerts = %d, want 1 without close-and-recreate", len(all))
	}
	if all[0].Status != "active" || all[0].Level != "p3" {
		t.Fatalf("alert = %+v, want active p3 pending cumulative escalation", all[0])
	}
	if all[0].SourceLine != "danger" {
		t.Fatalf("source line = %q, want danger", all[0].SourceLine)
	}
	// 同指标任意时刻活动告警 ≤1
	if n := len(activeAlerts(t, h)); n != 1 {
		t.Fatalf("active = %d, want 1", n)
	}
}

func TestOnThresholdNormalDoesNotRecoverBeforeRecoverDays(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), base)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 70), base.Add(30*time.Minute))
	var rec persistence.AlertRecord
	if err := h.DB.First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.Status != "active" || rec.RecoveredAt != nil {
		t.Fatalf("alert = %+v, want active until recover_days is satisfied", rec)
	}
}

func TestOnCollectFailUsesP4(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	svc.OnCollectFail(context.Background(), m, nil, "connection refused")
	var rec persistence.AlertRecord
	if err := h.DB.First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.Level != "p4" || rec.AlertType != "collect_fail" {
		t.Fatalf("alert = %+v, want p4 collect_fail", rec)
	}
	if rec.DeadlineAt == nil || rec.DeadlineAt.Sub(rec.TriggerAt) != 24*time.Hour {
		t.Fatalf("deadline = %v, want 24h after trigger", rec.DeadlineAt)
	}
}

func TestStateMachineClaimNoteClose(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), time.Now())
	var rec persistence.AlertRecord
	if err := h.DB.First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	// 关闭需必填结果
	if err := svc.Close(&rec, "alice", ""); err == nil {
		t.Fatal("close without result must fail")
	}
	if err := svc.Claim(&rec, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Note(&rec, "alice", "排查中: 日志表空间水位上涨"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(&rec, "alice", "已扩容"); err != nil {
		t.Fatal(err)
	}
	if err := h.DB.First(&rec, rec.ID).Error; err != nil {
		t.Fatal(err)
	}
	if rec.Status != "closed" || rec.ClaimedBy != "alice" || rec.CloseResult != "已扩容" {
		t.Fatalf("final = %+v", rec)
	}
	// closed 后不可再关闭/认领, 仅可备注
	if err := svc.Close(&rec, "alice", "again"); err == nil {
		t.Fatal("re-close must fail")
	}
	if err := svc.Claim(&rec, "bob"); err == nil {
		t.Fatal("claim closed must fail")
	}
	if err := svc.Note(&rec, "bob", "终态备注"); err != nil {
		t.Fatal("note on closed must work")
	}
}

func TestEvaluateAndEscalateUpdatesSameAlertAndWritesHistory(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), start)
	var alert persistence.AlertRecord
	if err := h.DB.First(&alert).Error; err != nil {
		t.Fatal(err)
	}
	policy := persistence.AlertEscalationPolicy{
		Name: "warn-3d", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3, DayBasis: "natural",
		ScopeType: "global", Enabled: true, Priority: 100,
	}
	if err := h.DB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	for day := 13; day <= 15; day++ {
		bizDate := time.Date(2026, 10, day, 0, 0, 0, 0, time.Local)
		value := 85.0
		if err := h.DB.Create(&persistence.MetricValueDay{
			MetricCode: m.MetricCode, BizDate: bizDate,
			StatValue: &value, Level: "warn", PointCount: 1, Source: "collect",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.EvaluateAndEscalate(context.Background(), m,
		time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	var alerts []persistence.AlertRecord
	if err := h.DB.Order("id").Find(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1 (must update original)", len(alerts))
	}
	if alerts[0].ID != alert.ID || alerts[0].Level != "p2" {
		t.Fatalf("alert = %+v, want same id %d upgraded to p2", alerts[0], alert.ID)
	}
	if alerts[0].EscalationCount != 1 || alerts[0].EscalatedAt == nil || alerts[0].EvidenceWindow == nil {
		t.Fatalf("escalation fields not populated: %+v", alerts[0])
	}
	var history []persistence.AlertLevelHistory
	if err := h.DB.Order("id").Find(&history).Error; err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].FromLevel != "p3" || history[0].ToLevel != "p2" {
		t.Fatalf("history = %+v, want p3 -> p2", history)
	}
}

func TestEvaluateAndEscalateRecoversAfterRecoverDays(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), start)
	for day := 14; day <= 15; day++ {
		bizDate := time.Date(2026, 10, day, 0, 0, 0, 0, time.Local)
		value := 70.0
		if err := h.DB.Create(&persistence.MetricValueDay{
			MetricCode: m.MetricCode, BizDate: bizDate,
			StatValue: &value, Level: "normal", PointCount: 1, Source: "collect",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.EvaluateAndEscalate(context.Background(), m,
		time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	var alert persistence.AlertRecord
	if err := h.DB.First(&alert).Error; err != nil {
		t.Fatal(err)
	}
	if alert.Status != "recovered" || alert.RecoveredAt == nil {
		t.Fatalf("alert = %+v, want recovered after 2 normal days", alert)
	}
}

func TestEvaluateAndEscalateRecoversDespiteHistoricalWindowHits(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	policy := persistence.AlertEscalationPolicy{
		Name: "danger-5d-2", SourceLine: "danger", TargetLevel: "p1",
		Mode: "window_count", WindowDays: 5, HitDays: 2, DayBasis: "natural",
		ScopeType: "global", Enabled: true, Priority: 100,
	}
	if err := h.DB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	for day := 13; day <= 16; day++ {
		value, level := 70.0, "normal"
		if day <= 14 {
			value, level = 95, "danger"
		}
		bizDate := time.Date(2026, 10, day, 0, 0, 0, 0, time.Local)
		if err := h.DB.Create(&persistence.MetricValueDay{
			MetricCode: m.MetricCode, BizDate: bizDate,
			StatValue: &value, Level: level, PointCount: 1, Source: "backfill",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	alert := persistence.AlertRecord{
		AlertNo: "AL-M6-RECOVER", MetricCode: m.MetricCode, SystemCode: m.SystemCode,
		Level: "p1", AlertType: "threshold", Status: "active",
		TriggerAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local),
	}
	if err := h.DB.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.EvaluateAndEscalate(context.Background(), m,
		time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	var got persistence.AlertRecord
	if err := h.DB.First(&got, alert.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "recovered" || got.RecoveredAt == nil {
		t.Fatalf("alert = %+v, want recovery to win after 2 normal days even with old window hits", got)
	}
}
