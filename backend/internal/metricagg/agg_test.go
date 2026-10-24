package metricagg

import "testing"

func f64(v float64) *float64 { return &v }

func sp(s string) *string { return &s }

func TestMode(t *testing.T) {
	cases := []struct {
		name    string
		modes   []string
		weights []*string
		want    string
	}{
		{"single leaf", []string{"sum"}, nil, "self"},
		{"no weight sum", []string{"sum", "sum"}, nil, "sum"},
		{"no weight first stat mode", []string{"max", "avg"}, nil, "max"},
		{"latest falls back max", []string{"latest", "latest"}, nil, "max"},
		{"any weight wins", []string{"avg", "avg"}, []*string{nil, sp("W")}, "weighted_avg"},
		{"empty weight string ignored", []string{"sum", "sum"}, []*string{sp(""), nil}, "sum"},
	}
	for _, c := range cases {
		if got := Mode(c.modes, c.weights); got != c.want {
			t.Fatalf("%s: Mode = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestAggregate(t *testing.T) {
	if v := Aggregate("sum", []Pair{{Value: f64(1)}, {Value: f64(2)}, {Value: f64(3)}}); v == nil || *v != 6 {
		t.Fatalf("sum = %v", v)
	}
	if v := Aggregate("max", []Pair{{Value: f64(2)}, {Value: f64(9)}, {Value: f64(4)}}); v == nil || *v != 9 {
		t.Fatalf("max = %v", v)
	}
	if v := Aggregate("min", []Pair{{Value: f64(2)}, {Value: f64(9)}, {Value: f64(4)}}); v == nil || *v != 2 {
		t.Fatalf("min = %v", v)
	}
	if v := Aggregate("avg", []Pair{{Value: f64(1)}, {Value: f64(2)}, {Value: f64(6)}}); v == nil || *v != 3 {
		t.Fatalf("avg = %v", v)
	}
	if v := Aggregate("self", []Pair{{Value: f64(42)}}); v == nil || *v != 42 {
		t.Fatalf("self = %v", v)
	}
	// 加权均值: (100*10 + 200*30) / (10+30) = 175
	if v := Aggregate("weighted_avg", []Pair{{Value: f64(100), Weight: f64(10)}, {Value: f64(200), Weight: f64(30)}}); v == nil || *v != 175 {
		t.Fatalf("weighted_avg = %v, want 175", v)
	}
	// 无可用权重 → 退化为简单均值
	if v := Aggregate("weighted_avg", []Pair{{Value: f64(100)}, {Value: f64(200)}}); v == nil || *v != 150 {
		t.Fatalf("weighted_avg fallback = %v, want 150", v)
	}
	// 仅统计权重>0 的子指标: (100*10)/10 = 100
	if v := Aggregate("weighted_avg", []Pair{{Value: f64(100), Weight: f64(10)}, {Value: f64(200), Weight: nil}}); v == nil || *v != 100 {
		t.Fatalf("weighted_avg partial = %v, want 100", v)
	}
	// 权重为 0 不计数
	if v := Aggregate("weighted_avg", []Pair{{Value: f64(100), Weight: f64(0)}, {Value: f64(200), Weight: f64(0)}}); v == nil || *v != 150 {
		t.Fatalf("weighted_avg zero-weight = %v, want 150(fallback)", v)
	}
	// 全空 → nil
	if v := Aggregate("sum", []Pair{{Value: nil}, {Value: nil}}); v != nil {
		t.Fatalf("empty = %v, want nil", v)
	}
	// 部分缺失值跳过
	if v := Aggregate("avg", []Pair{{Value: nil}, {Value: f64(4)}}); v == nil || *v != 4 {
		t.Fatalf("partial = %v, want 4", v)
	}
}
