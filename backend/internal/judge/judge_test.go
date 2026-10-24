package judge

import (
	"testing"

	"capacity-manage/backend/internal/persistence"
)

func f64(v float64) *float64 { return &v }

func mkMetric(dir string, baseline, warn, danger *float64) *persistence.CapacityMetric {
	return &persistence.CapacityMetric{JudgeDirection: dir, Baseline: baseline, WarnLine: warn, DangerLine: danger}
}

func TestEvaluateHigherWorse(t *testing.T) {
	j := New(nil, nil)
	m := mkMetric("higher_worse", f64(100), f64(80), f64(90))
	cases := []struct {
		v    float64
		want string
	}{
		{50, "normal"}, {79.99, "normal"}, {80, "warn"}, {89.99, "warn"}, {90, "danger"}, {95, "danger"},
	}
	for _, c := range cases {
		res := j.Evaluate(m, f64(c.v))
		if res.Level != c.want {
			t.Fatalf("v=%.4f: got %s want %s", c.v, res.Level, c.want)
		}
	}
	res := j.Evaluate(m, f64(80))
	if res.WaterLevel == nil || *res.WaterLevel != 0.8 {
		t.Fatalf("water level = %v, want 0.8", res.WaterLevel)
	}
	if res.Threshold == nil || *res.Threshold != 80 {
		t.Fatalf("threshold = %v, want 80", res.Threshold)
	}
}

func TestEvaluateLowerWorse(t *testing.T) {
	j := New(nil, nil)
	m := mkMetric("lower_worse", f64(100), f64(20), f64(10))
	cases := []struct {
		v    float64
		want string
	}{
		{50, "normal"}, {20, "warn"}, {19.99, "warn"}, {10, "danger"}, {5, "danger"},
	}
	for _, c := range cases {
		res := j.Evaluate(m, f64(c.v))
		if res.Level != c.want {
			t.Fatalf("v=%.4f: got %s want %s", c.v, res.Level, c.want)
		}
	}
	res := j.Evaluate(m, f64(80))
	if res.WaterLevel == nil || *res.WaterLevel != 0.2 {
		t.Fatalf("water level = %v, want 0.2", res.WaterLevel)
	}
}

func TestEvaluateNilValueOrNilLines(t *testing.T) {
	j := New(nil, nil)
	m := mkMetric("higher_worse", f64(100), f64(80), f64(90))
	if res := j.Evaluate(m, nil); res.Level != "normal" || res.Value != nil {
		t.Fatalf("nil value should be normal with nil value: %+v", res)
	}
	m2 := mkMetric("higher_worse", nil, nil, nil)
	if res := j.Evaluate(m2, f64(1e9)); res.Level != "normal" {
		t.Fatalf("no lines configured should be normal: %+v", res)
	}
}

func TestWaterLevelClamp(t *testing.T) {
	j := New(nil, nil)
	m := mkMetric("higher_worse", f64(100), nil, f64(200))
	res := j.Evaluate(m, f64(250))
	if res.Level != "danger" {
		t.Fatalf("level = %s, want danger", res.Level)
	}
	if res.WaterLevel == nil || *res.WaterLevel != 1 {
		t.Fatalf("water level should clamp to 1: %v", res.WaterLevel)
	}
}