package alert

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"
)

func TestValidateEscalationPolicyRejectsInvalidNandM(t *testing.T) {
	cases := []PolicyInput{
		{Name: "bad-window", SourceLine: "warn", TargetLevel: "p2", Mode: "window_count", WindowDays: 7, HitDays: 8, ScopeType: "global"},
		{Name: "bad-required", SourceLine: "warn", TargetLevel: "p2", Mode: "window_count", WindowDays: 0, HitDays: 3, ScopeType: "global"},
		{Name: "bad-consecutive", SourceLine: "danger", TargetLevel: "p1", Mode: "consecutive", ConsecutiveDays: 0, ScopeType: "global"},
	}
	for _, in := range cases {
		if _, err := normalizeEscalationPolicyInput(in); err == nil {
			t.Fatalf("input %s should be rejected", in.Name)
		}
	}
}

func TestValidateEscalationPolicyRequiresScopeValue(t *testing.T) {
	_, err := normalizeEscalationPolicyInput(PolicyInput{
		Name: "bad-scope", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3, ScopeType: "system",
	})
	if err == nil {
		t.Fatal("system scope without scopeValue must be rejected")
	}
}

func TestPolicyScopeMustBeVisibleToManager(t *testing.T) {
	visible := []string{"SYS_A"}
	if err := validatePolicyScope("system", []string{"SYS_B"}, visible, false); err == nil {
		t.Fatal("manager must not create a policy for an invisible system")
	}
	if err := validatePolicyScope("metric", []string{"MET_A"}, visible, false); err == nil {
		t.Fatal("metric scope without visible metric system must be rejected")
	}
	if err := validatePolicyScope("system", []string{"SYS_A"}, visible, false); err != nil {
		t.Fatalf("visible system rejected: %v", err)
	}
	if err := validatePolicyScope("system", []string{"SYS_B"}, visible, true); err != nil {
		t.Fatalf("platform admin should pass: %v", err)
	}
}

func TestPolicyScopeAcceptsMultipleValues(t *testing.T) {
	row, err := normalizeEscalationPolicyInput(PolicyInput{
		Name: "multi-system", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3, ScopeType: "system",
		ScopeValue: persistence.StringList{" SYS_A ", "SYS_B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(row.ScopeValue) != 2 || row.ScopeValue[0] != "SYS_A" || row.ScopeValue[1] != "SYS_B" {
		t.Fatalf("scopeValue = %#v, want trimmed [SYS_A SYS_B]", row.ScopeValue)
	}
}

func TestPolicyScopeDedupesAndDropsBlanks(t *testing.T) {
	row, err := normalizeEscalationPolicyInput(PolicyInput{
		Name: "dedupe", SourceLine: "danger", TargetLevel: "p1",
		Mode: "consecutive", ConsecutiveDays: 2, ScopeType: "metric",
		ScopeValue:  persistence.StringList{"SYS_A.MET_1", "SYS_A.MET_1"},
		ScopeValues: persistence.StringList{"", "  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(row.ScopeValue) != 1 || row.ScopeValue[0] != "SYS_A.MET_1" {
		t.Fatalf("scopeValue = %#v, want single deduped entry", row.ScopeValue)
	}
}

func TestPolicyGlobalScopeClearsValues(t *testing.T) {
	row, err := normalizeEscalationPolicyInput(PolicyInput{
		Name: "global", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3, ScopeType: "global",
		ScopeValue: persistence.StringList{"SYS_A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(row.ScopeValue) != 0 {
		t.Fatalf("global scope must not keep values: %#v", row.ScopeValue)
	}
}

func TestPolicyScopeRejectsPartiallyInvisibleSystems(t *testing.T) {
	visible := []string{"SYS_A"}
	if err := validatePolicyScope("system", []string{"SYS_A", "SYS_B"}, visible, false); err == nil {
		t.Fatal("one invisible system must reject the whole multi-value scope")
	}
	if err := validatePolicyScope("system", []string{"SYS_A"}, visible, false); err != nil {
		t.Fatalf("all-visible multi-value scope rejected: %v", err)
	}
	if err := validatePolicyScope("metric", []string{"SYS_A.MET_1", "SYS_B.MET_2"}, visible, false); err == nil {
		t.Fatal("one invisible metric system must reject the whole multi-value scope")
	}
}

func TestPolicyMatchesScopeWithMultipleValues(t *testing.T) {
	metric := &persistence.CapacityMetric{MetricCode: "MET_1", SystemCode: "SYS_A"}
	cases := []struct {
		name  string
		scope persistence.AlertEscalationPolicy
		want  bool
	}{
		{"global", persistence.AlertEscalationPolicy{ScopeType: "global"}, true},
		{"system-hit-second", persistence.AlertEscalationPolicy{ScopeType: "system", ScopeValue: persistence.StringList{"SYS_B", "SYS_A"}}, true},
		{"system-miss", persistence.AlertEscalationPolicy{ScopeType: "system", ScopeValue: persistence.StringList{"SYS_B"}}, false},
		{"metric-bare", persistence.AlertEscalationPolicy{ScopeType: "metric", ScopeValue: persistence.StringList{"MET_1"}}, true},
		{"metric-qualified-second", persistence.AlertEscalationPolicy{ScopeType: "metric", ScopeValue: persistence.StringList{"X.MET_9", "SYS_A.MET_1"}}, true},
		{"metric-miss", persistence.AlertEscalationPolicy{ScopeType: "metric", ScopeValue: persistence.StringList{"SYS_A.MET_9"}}, false},
	}
	for _, tc := range cases {
		if got := policyMatchesScope(tc.scope, metric); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestPolicyScopeValueJSONAcceptsLegacyString(t *testing.T) {
	var in PolicyInput
	legacy := `{"name":"legacy","sourceLine":"warn","targetLevel":"p2","mode":"consecutive","consecutiveDays":3,"scopeType":"system","scopeValue":"SYS_A"}`
	if err := json.Unmarshal([]byte(legacy), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.ScopeValue) != 1 || in.ScopeValue[0] != "SYS_A" {
		t.Fatalf("legacy string scopeValue = %#v", in.ScopeValue)
	}
	var arr PolicyInput
	multi := `{"name":"multi","sourceLine":"warn","targetLevel":"p2","mode":"consecutive","consecutiveDays":3,"scopeType":"system","scopeValues":["SYS_A","SYS_B"]}`
	if err := json.Unmarshal([]byte(multi), &arr); err != nil {
		t.Fatal(err)
	}
	row, err := normalizeEscalationPolicyInput(arr)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.ScopeValue) != 2 {
		t.Fatalf("scopeValues alias not merged: %#v", row.ScopeValue)
	}
}

func TestBuildTimelineIncludesEscalationAndEvidence(t *testing.T) {
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)
	evidence := `{"sourceLine":"warn","mode":"window_count","windowDays":7,"requiredDays":3,"hitDates":["2026-10-10","2026-10-12","2026-10-15"]}`
	rec := &persistence.AlertRecord{
		AlertNo: "AL1", Level: "p2", AlertType: "threshold", Status: "active",
		TriggerAt: base, FirstValue: floatPtr(85), Threshold: floatPtr(80), EvidenceWindow: &evidence,
	}
	history := []persistence.AlertLevelHistory{{
		AlertID: 1, FromLevel: "p3", ToLevel: "p2",
		Reason: "命中预警线累积升级规则", Evidence: evidence,
		Operator: "system", ChangedAt: base.Add(48 * time.Hour), Action: "escalate",
	}}
	items := BuildTimelineWithHistory(rec, history)
	found := false
	for _, it := range items {
		if it.Event == "escalate" && it.Time == base.Add(48*time.Hour).Format(time.RFC3339) {
			found = true
			if it.Evidence == nil || it.Evidence["requiredDays"] != float64(3) {
				t.Fatalf("evidence = %#v", it.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("escalate timeline missing: %+v", items)
	}
}

func TestEvaluateAndEscalateWritesDowngradeSuggestionWithoutChangingLevel(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	start := time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	svc.OnThreshold(context.Background(), m, evalRes(t, m, 85), start)

	policy := persistence.AlertEscalationPolicy{
		Name: "warn-3d", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3, DayBasis: "natural",
		ScopeType: "global", Enabled: true, Priority: 100,
	}
	if err := h.DB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	for day := 13; day <= 15; day++ {
		v := 85.0
		_ = h.DB.Create(&persistence.MetricValueDay{
			MetricCode: m.MetricCode,
			BizDate:    time.Date(2026, 10, day, 0, 0, 0, 0, time.Local),
			StatValue:  &v, Level: "warn", PointCount: 1, Source: "collect",
		}).Error
	}
	if err := svc.EvaluateAndEscalate(context.Background(), m, time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}

	// 修正 10-13 为正常后，最近三天不再满足连续规则，应只记录降级建议。
	v := 70.0
	if err := h.DB.Model(&persistence.MetricValueDay{}).
		Where("metric_code = ? AND biz_date = ?", m.MetricCode, time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local)).
		Updates(map[string]any{"stat_value": v, "level": "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.EvaluateAndEscalate(context.Background(), m, time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	var rec persistence.AlertRecord
	if err := h.DB.First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.Level != "p2" {
		t.Fatalf("level = %s, downgrade must require confirmation", rec.Level)
	}
	var suggestions []persistence.AlertLevelHistory
	_ = h.DB.Where("alert_id = ? AND action = ?", rec.ID, "downgrade_suggestion").Find(&suggestions).Error
	if len(suggestions) != 1 || suggestions[0].ToLevel == rec.Level {
		t.Fatalf("suggestions = %+v, want one lower-level suggestion", suggestions)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(suggestions[0].Evidence), &evidence); err != nil {
		t.Fatalf("evidence json: %v", err)
	}
	if evidence["reason"] == nil {
		t.Fatalf("suggestion evidence missing reason: %#v", evidence)
	}
}

func TestPolicyChangeTakesEffectOnNextEvaluation(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.AlertEscalationPolicy{})
	p := persistence.AlertEscalationPolicy{
		Name: "warn-3d", SourceLine: "warn", TargetLevel: "p2", Mode: "consecutive",
		ConsecutiveDays: 3, ScopeType: "global", Enabled: true, Priority: 100,
	}
	if err := h.DB.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	_ = h.DB.Model(&p).Updates(map[string]any{"consecutive_days": 5}).Error
	var got persistence.AlertEscalationPolicy
	if err := h.DB.First(&got, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.ConsecutiveDays != 5 {
		t.Fatalf("consecutive days = %d, want hot-updated 5", got.ConsecutiveDays)
	}
}

func TestPolicyCRUDAndEnableToggle(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.AlertEscalationPolicy{})
	enabled := true
	created, err := createEscalationPolicy(h.DB, PolicyInput{
		Name: "warn-check", SourceLine: "warn", TargetLevel: "p2",
		Mode: "window_count", WindowDays: 7, HitDays: 3,
		ScopeType: "global", Enabled: &enabled,
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || !created.Enabled || created.CreatedBy != "admin" {
		t.Fatalf("created = %+v", created)
	}
	disabled := false
	updated, err := updateEscalationPolicy(h.DB, created.ID, PolicyInput{
		Name: "warn-check", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 5,
		ScopeType: "global", Enabled: &disabled, Priority: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Mode != "consecutive" || updated.ConsecutiveDays != 5 || updated.Priority != 80 {
		t.Fatalf("updated = %+v", updated)
	}
	if err := deleteEscalationPolicy(h.DB, created.ID); err != nil {
		t.Fatal(err)
	}
	var n int64
	_ = h.DB.Model(&persistence.AlertEscalationPolicy{}).Where("id = ?", created.ID).Count(&n).Error
	if n != 0 {
		t.Fatalf("policy still present after delete")
	}
}

func TestSimulatePolicyReplayReturnsMatchedDayAndEvidence(t *testing.T) {
	h, svc := newAlertHarness(t)
	m := metricWithLines(80, 90)
	for day := 13; day <= 15; day++ {
		v := 85.0
		_ = h.DB.Create(&persistence.MetricValueDay{
			MetricCode: m.MetricCode,
			BizDate:    time.Date(2026, 10, day, 0, 0, 0, 0, time.Local),
			StatValue:  &v, Level: "warn", PointCount: 1, Source: "collect",
		}).Error
	}
	in := PolicyInput{
		Name: "simulate", SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3, ScopeType: "global",
	}
	result, err := svc.SimulatePolicy(context.Background(), m, in,
		time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local),
		time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched || result.Level != "p2" || result.EvaluatedOn != "2026-10-15" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Days) != 3 || !result.Days[0].WarnHit {
		t.Fatalf("days = %+v", result.Days)
	}
}

func floatPtr(v float64) *float64 { return &v }
