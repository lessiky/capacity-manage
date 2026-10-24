package openapi

import "testing"

func TestEvaluateExpressionArithmetic(t *testing.T) {
	values := map[string]float64{"A5_CPU": 80, "A5_MEM": 60}
	cases := map[string]float64{
		"(A5_CPU + A5_MEM) / 2":  70,
		"A5_CPU - A5_MEM":        20,
		"A5_CPU * 2":             160,
		"-A5_MEM + 100":          40,
		"A5_CPU / (A5_MEM / 30)": 40,
		"1.5 * A5_MEM":           90,
	}
	for expr, want := range cases {
		got, err := evaluateExpression(expr, values)
		if err != nil {
			t.Fatalf("%q 计算失败: %v", expr, err)
		}
		if !almostEqualE(got, want) {
			t.Fatalf("%q = %v, 期望 %v", expr, got, want)
		}
	}
}

func TestEvaluateExpressionRejectsInvalidInput(t *testing.T) {
	values := map[string]float64{"A5_CPU": 80}
	cases := []string{
		"A5_CPU / 0",
		"A5_CPU +",
		"(A5_CPU",
		"A5_CPU @ 2",
		"B5_MEM * 2",
		"import os",
	}
	for _, expr := range cases {
		if _, err := evaluateExpression(expr, values); err == nil {
			t.Fatalf("%q 应被拒绝", expr)
		}
	}
}

func almostEqualE(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}
