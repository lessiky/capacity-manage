package alert

import (
	"testing"
	"time"
)

func hitDay(day int, warn, danger bool, state string) DailyHit {
	return DailyHit{
		BizDate:   time.Date(2026, 10, day, 0, 0, 0, 0, time.Local),
		WarnHit:   warn,
		DangerHit: danger,
		DataState: state,
	}
}

func TestEvaluateEscalationConsecutiveWarn(t *testing.T) {
	policies := []EscalationPolicy{{
		ID: 1, SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3,
	}}
	hits := []DailyHit{
		hitDay(13, true, false, "valid"),
		hitDay(14, true, false, "valid"),
		hitDay(15, true, false, "valid"),
	}
	got := EvaluateEscalation(policies, hits)
	if !got.Matched || got.Level != "p2" || got.PolicyID != 1 {
		t.Fatalf("decision = %+v, want p2 policy 1", got)
	}
	if got.Evidence["hitDays"] != 3 || got.Evidence["windowDays"] != 3 {
		t.Fatalf("evidence = %+v", got.Evidence)
	}
}

func TestEvaluateEscalationConsecutiveBrokenByMissingDay(t *testing.T) {
	policies := []EscalationPolicy{{
		ID: 1, SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 3,
	}}
	hits := []DailyHit{
		hitDay(13, true, false, "valid"),
		hitDay(14, false, false, "missing"),
		hitDay(15, true, false, "valid"),
	}
	if got := EvaluateEscalation(policies, hits); got.Matched {
		t.Fatalf("decision = %+v, missing day must break consecutive rule", got)
	}
}

func TestEvaluateEscalationWindowCountsNonConsecutiveHits(t *testing.T) {
	policies := []EscalationPolicy{{
		ID: 2, SourceLine: "warn", TargetLevel: "p2",
		Mode: "window_count", WindowDays: 7, HitDays: 3,
	}}
	hits := []DailyHit{
		hitDay(11, true, false, "valid"),
		hitDay(12, false, false, "valid"),
		hitDay(13, true, false, "valid"),
		hitDay(14, false, false, "missing"),
		hitDay(15, true, false, "valid"),
	}
	got := EvaluateEscalation(policies, hits)
	if !got.Matched || got.Level != "p2" || got.Evidence["hitDays"] != 3 {
		t.Fatalf("decision = %+v, window should count 3 hits despite missing day", got)
	}
}

func TestEvaluateEscalationDangerHitAlsoCountsAsWarn(t *testing.T) {
	policies := []EscalationPolicy{
		{ID: 10, SourceLine: "warn", TargetLevel: "p2", Mode: "consecutive", ConsecutiveDays: 2},
		{ID: 11, SourceLine: "danger", TargetLevel: "p1", Mode: "consecutive", ConsecutiveDays: 2},
	}
	hits := []DailyHit{
		hitDay(14, true, true, "valid"),
		hitDay(15, true, true, "valid"),
	}
	got := EvaluateEscalation(policies, hits)
	if !got.Matched || got.Level != "p1" || got.PolicyID != 11 {
		t.Fatalf("decision = %+v, danger P1 must win", got)
	}
}

func TestEvaluateEscalationChoosesHighestTarget(t *testing.T) {
	policies := []EscalationPolicy{
		{ID: 20, SourceLine: "warn", TargetLevel: "p2", Mode: "window_count", WindowDays: 7, HitDays: 2},
		{ID: 21, SourceLine: "danger", TargetLevel: "p1", Mode: "consecutive", ConsecutiveDays: 2},
	}
	hits := []DailyHit{
		hitDay(13, true, false, "valid"),
		hitDay(14, true, true, "valid"),
		hitDay(15, true, true, "valid"),
	}
	got := EvaluateEscalation(policies, hits)
	if got.Level != "p1" || got.PolicyID != 21 {
		t.Fatalf("decision = %+v, want highest target p1", got)
	}
}

func TestEvaluateEscalationDeduplicatesSameBusinessDay(t *testing.T) {
	policies := []EscalationPolicy{{
		ID: 30, SourceLine: "warn", TargetLevel: "p2",
		Mode: "consecutive", ConsecutiveDays: 2,
	}}
	hits := []DailyHit{
		hitDay(14, true, false, "valid"),
		hitDay(14, true, false, "valid"),
		hitDay(15, true, false, "valid"),
	}
	got := EvaluateEscalation(policies, hits)
	if !got.Matched || got.Level != "p2" {
		t.Fatalf("decision = %+v, duplicate same-day hits must collapse to one day", got)
	}
}

func TestEvaluateEscalationIgnoresAnomalyAndInvalidState(t *testing.T) {
	policies := []EscalationPolicy{{
		ID: 40, SourceLine: "warn", TargetLevel: "p2",
		Mode: "window_count", WindowDays: 3, HitDays: 2,
	}}
	hits := []DailyHit{
		hitDay(13, true, false, "anomaly"),
		hitDay(14, true, false, "valid"),
		hitDay(15, true, false, "valid"),
	}
	got := EvaluateEscalation(policies, hits)
	if !got.Matched || got.Evidence["hitDays"] != 2 {
		t.Fatalf("decision = %+v, anomaly hit must not count", got)
	}
}
