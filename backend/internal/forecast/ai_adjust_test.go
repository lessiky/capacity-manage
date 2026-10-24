package forecast

import (
	"strings"
	"testing"
	"time"
)

func baseAIResult() ForecastResult {
	origin := day(2026, 10, 1)
	curve := make([]CurvePoint, 0, 30)
	for i := 0; i < 30; i++ {
		d := origin.AddDate(0, 0, i)
		v := 70 + float64(i)
		curve = append(curve, CurvePoint{BizDate: d, Value: v, Lower: v - 1, Upper: v + 1})
	}
	warn := origin.AddDate(0, 0, 11)
	danger := origin.AddDate(0, 0, 16)
	return ForecastResult{
		ModelType: ModelLinear, Status: StatusSuccess, HorizonDays: 30,
		Slope: 1, Intercept: 70, ResidualStd: 0.1, Confidence: 0.8,
		ETAWarnDate: &warn, ETADangerDate: &danger,
		Curve: curve, RiskScore: 0.5, Explanation: "线性模型拟合近 30 个有效日",
	}
}

func TestApplyAIAdjustmentValidatesBounds(t *testing.T) {
	base := baseAIResult()
	cases := []struct {
		name string
		adj  AIAdjustment
	}{
		{"斜率过低", AIAdjustment{SlopeMultiplier: 0.1, InterceptAdjustPct: 0, ConfidenceAdjustment: 0, Rationale: "合理说明"}},
		{"斜率过高", AIAdjustment{SlopeMultiplier: 4, InterceptAdjustPct: 0, ConfidenceAdjustment: 0, Rationale: "合理说明"}},
		{"截距越界", AIAdjustment{SlopeMultiplier: 1, InterceptAdjustPct: 60, ConfidenceAdjustment: 0, Rationale: "合理说明"}},
		{"置信度越界", AIAdjustment{SlopeMultiplier: 1, InterceptAdjustPct: 0, ConfidenceAdjustment: -0.9, Rationale: "合理说明"}},
		{"解释为空", AIAdjustment{SlopeMultiplier: 1, InterceptAdjustPct: 0, ConfidenceAdjustment: 0, Rationale: "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ApplyAIAdjustment(base, tc.adj)
			if err != nil {
				t.Fatalf("AI 不可用不应阻断主流程: %v", err)
			}
			if out.Slope != base.Slope || out.Confidence != base.Confidence {
				t.Fatalf("非法调整必须回退统计预测: slope=%v confidence=%v", out.Slope, out.Confidence)
			}
			if out.AIStatus != AIStatusInvalid {
				t.Fatalf("状态应为 invalid, 实际 %s", out.AIStatus)
			}
			if !strings.Contains(out.AdjustmentNote, "AI 修正已忽略") {
				t.Fatalf("应给出降级说明, 实际 %q", out.AdjustmentNote)
			}
			if out.ETAWarnDate == nil || !out.ETAWarnDate.Equal(*base.ETAWarnDate) {
				t.Fatalf("降级后 ETA 应保持统计值")
			}
		})
	}
}

func TestApplyAIAdjustmentAppliesWithinBounds(t *testing.T) {
	base := baseAIResult()
	out, err := ApplyAIAdjustment(base, AIAdjustment{
		SlopeMultiplier: 1.2, InterceptAdjustPct: 2, ConfidenceAdjustment: 0.1,
		Rationale: "双十一业务高峰预计推高使用率",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.AdvisoryOnly {
		t.Fatalf("小幅调整不应降级为建议")
	}
	if out.AIStatus != AIStatusSuccess {
		t.Fatalf("状态应为 success, 实际 %s", out.AIStatus)
	}
	if !almostEqual(out.Slope, base.Slope*1.2) {
		t.Fatalf("斜率应放大 1.2 倍: %v", out.Slope)
	}
	if !almostEqual(out.Confidence, 0.9) {
		t.Fatalf("置信度应为 0.9: %v", out.Confidence)
	}
	if out.ETAWarnDate == nil || out.ETADangerDate == nil {
		t.Fatalf("应产出 AI 修正后的 ETA")
	}
	if !out.ETADangerDate.Before(*base.ETADangerDate) {
		t.Fatalf("斜率放大后危险线 ETA 应提前: base=%v adjusted=%v", base.ETADangerDate, out.ETADangerDate)
	}
	if !strings.Contains(out.Explanation, "AI 修正") {
		t.Fatalf("解释应记录 AI 修正: %q", out.Explanation)
	}
}

func TestApplyAIAdjustmentCapsOversizedETAShift(t *testing.T) {
	// 构造一个「到线日贴近窗口末端」的基准：这时任何有意义的斜率放大都会把
	// ETA 提前远超 30 天窗口的 50%（15 天），从而触发降级保护。
	origin := day(2026, 10, 1)
	base := ForecastResult{
		ModelType: ModelLinear, Status: StatusSuccess, HorizonDays: 30,
		Slope: 1, Intercept: 100, ResidualStd: 0.1, Confidence: 0.8,
		Curve:       make([]CurvePoint, 0, 30),
		RiskScore:   0.3,
		Explanation: "线性模型拟合近 30 个有效日",
	}
	for i := 0; i < 30; i++ {
		d := origin.AddDate(0, 0, i)
		v := 100 + float64(i)
		base.Curve = append(base.Curve, CurvePoint{BizDate: d, Value: v, Lower: v - 1, Upper: v + 1})
	}
	warn := origin.AddDate(0, 0, 29)
	danger := origin.AddDate(0, 0, 29)
	base.ETAWarnDate = &warn
	base.ETADangerDate = &danger

	// 斜率放大 3 倍后到线日期提前约 20 天，超过 30 天窗口的 50%（15 天）。
	out, err := ApplyAIAdjustment(base, AIAdjustment{
		SlopeMultiplier: 3, InterceptAdjustPct: 0, ConfidenceAdjustment: 0.2,
		Rationale: "突发业务放量导致容量快速逼近危险线",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.AdvisoryOnly || out.AIStatus != AIStatusOversized {
		t.Fatalf("超幅调整应降为建议: advisory=%v status=%s", out.AdvisoryOnly, out.AIStatus)
	}
	if out.ETAWarnDate == nil || !out.ETAWarnDate.Equal(*base.ETAWarnDate) {
		t.Fatalf("超幅调整后主指标 ETA 必须保留统计值")
	}
	if !almostEqual(out.Slope, base.Slope) || !almostEqual(out.Confidence, base.Confidence) {
		t.Fatalf("超幅调整不得改写主指标斜率/置信度")
	}
	if out.SuggestedETAWarnDate == nil || out.SuggestedETADangerDate == nil {
		t.Fatalf("应保留 AI 建议的 ETA 供展示")
	}
	if !strings.Contains(out.AdjustmentNote, "仅作建议展示") {
		t.Fatalf("应说明降级原因: %q", out.AdjustmentNote)
	}
}

func TestParseAIAdjustmentStrictJSON(t *testing.T) {
	good := `{"slopeMultiplier":1.1,"interceptAdjustmentPct":-5,"confidenceAdjustment":0.05,"rationale":"节假日业务量下降"}`
	if _, err := ParseAIAdjustment(good); err != nil {
		t.Fatalf("合法 JSON 不应报错: %v", err)
	}
	fenced := "```json\n" + good + "\n```"
	if _, err := ParseAIAdjustment(fenced); err != nil {
		t.Fatalf("代码块包裹应可解析: %v", err)
	}
	cases := map[string]string{
		"非法 JSON": `{"slopeMultiplier":`,
		"字段缺失":    `{"slopeMultiplier":1.1}`,
		"字段类型错误":  `{"slopeMultiplier":"high","interceptAdjustmentPct":0,"confidenceAdjustment":0,"rationale":"说明"}`,
		"未知字段":    `{"slopeMultiplier":1.1,"interceptAdjustmentPct":0,"confidenceAdjustment":0,"rationale":"说明","extra":1}`,
		"越界斜率":    `{"slopeMultiplier":4,"interceptAdjustmentPct":0,"confidenceAdjustment":0,"rationale":"说明"}`,
		"空解释":     `{"slopeMultiplier":1.1,"interceptAdjustmentPct":0,"confidenceAdjustment":0,"rationale":""}`,
		"空字符串":    ``,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAIAdjustment(raw); err == nil {
				t.Fatalf("应拒绝 %s", name)
			}
		})
	}
}

func TestApplyAIAdjustmentSkipsWhenNoThreshold(t *testing.T) {
	base := baseAIResult()
	base.Curve = nil
	base.ETAWarnDate = nil
	base.ETADangerDate = nil
	out, err := ApplyAIAdjustment(base, AIAdjustment{
		SlopeMultiplier: 1.1, InterceptAdjustPct: 0, ConfidenceAdjustment: 0,
		Rationale: "无阈值时仍可给出趋势修正",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ETAWarnDate != nil || out.ETADangerDate != nil {
		t.Fatalf("无阈值不应凭空产出 ETA")
	}
	if out.AdvisoryOnly {
		t.Fatalf("无阈值且无偏移不应降级")
	}
}

func TestApplyAIAdjustmentNeverDegradesOnBadInput(t *testing.T) {
	base := baseAIResult()
	out, err := ApplyAIAdjustment(base, AIAdjustment{
		SlopeMultiplier: 0, InterceptAdjustPct: 0, ConfidenceAdjustment: 0, Rationale: "",
	})
	if err != nil {
		t.Fatalf("AI 异常必须降级而非报错: %v", err)
	}
	if out.Status != base.Status || out.RiskScore != base.RiskScore {
		t.Fatalf("降级后统计结果必须原样可用")
	}
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func almostEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-6
}
