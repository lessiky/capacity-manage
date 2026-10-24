package forecast

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// AIAdjustment 是 AI 对统计预测的修正建议（计划 §6.3）。字段缺失/越界/非数值
// 一律回退统计预测；调整幅度过大只作建议展示，不进入风险清单与报表主指标。
type AIAdjustment struct {
	SlopeMultiplier      float64 `json:"slopeMultiplier"`
	InterceptAdjustPct   float64 `json:"interceptAdjustmentPct"`
	ConfidenceAdjustment float64 `json:"confidenceAdjustment"`
	Rationale            string  `json:"rationale"`
}

// AI 调整的合法区间。放宽会破坏统计预测的可解释性，故在纯函数层做硬约束。
// 约束需同时满足两个目标：允许真实的节假日/事件修正，又让「ETA 偏移超过基础
// horizon 50%」这一降级保护可达，从而不会出现永远触发不了的护栏。
const (
	aiSlopeMultiplierMin = 0.5
	aiSlopeMultiplierMax = 3.0
	aiInterceptPctMin    = -30.0
	aiInterceptPctMax    = 30.0
	aiConfidenceAdjMin   = -0.2
	aiConfidenceAdjMax   = 0.2
	aiRationaleMinLen    = 4
	aiRationaleMaxLen    = 1024
	aiETAShiftRatio      = 0.5
)

// AI 调整结果的显式状态，便于持久化与前端展示降级原因。
const (
	AIStatusSuccess   = "success"
	AIStatusInvalid   = "invalid"
	AIStatusOversized = "oversized"
	AIStatusDisabled  = "disabled"
)

// ApplyAIAdjustment 对统计预测施加 AI 修正（计划 §6.3 接口）。
//
// 返回的 ForecastResult 始终可用于主流程：
//   - 校验失败返回原统计结果，并追加降级说明（不返回 error，保证 AI 问题不阻断主流程）；
//   - 校验通过但 ETA 偏移超过基础 horizon 的 50% 时，ETA 保留统计值，
//     AI 建议写入 Suggested* 字段并置 AdvisoryOnly，只作展示。
func ApplyAIAdjustment(base ForecastResult, adj AIAdjustment) (ForecastResult, error) {
	if err := validateAIAdjustment(adj); err != nil {
		base.AdvisoryOnly = true
		base.AdjustmentNote = "AI 修正已忽略：" + err.Error()
		base.AIStatus = AIStatusInvalid
		return base, nil
	}

	adjusted := base
	adjusted.AdjustmentNote = strings.TrimSpace(adj.Rationale)
	adjusted.AIStatus = AIStatusSuccess
	adjusted.Slope = base.Slope * adj.SlopeMultiplier
	adjusted.Intercept = base.Intercept * (1 + adj.InterceptAdjustPct/100)
	adjusted.Confidence = clamp(base.Confidence+adj.ConfidenceAdjustment, 0, 1)

	horizon := base.HorizonDays
	if horizon <= 0 {
		horizon = 30
	}
	threshold := thresholdFromCurve(base)
	adjusted.Curve = adjustCurve(base.Curve, adj.SlopeMultiplier, adj.InterceptAdjustPct)
	upward := base.Slope >= 0
	adjusted.ETAWarnDate = firstCurveCrossing(adjusted.Curve, base.ETAWarnDate, threshold.warn, upward)
	adjusted.ETADangerDate = firstCurveCrossing(adjusted.Curve, base.ETADangerDate, threshold.danger, upward)
	adjusted.Explanation = aiExplanation(base, adjusted, adj)

	shift := maxETAShiftDays(base, adjusted)
	if shift > int(math.Ceil(aiETAShiftRatio*float64(horizon))) {
		// 调整幅度过大：保留统计 ETA 作为主指标，AI 结果降为建议。
		adjusted.SuggestedETAWarnDate = adjusted.ETAWarnDate
		adjusted.SuggestedETADangerDate = adjusted.ETADangerDate
		adjusted.ETAWarnDate = base.ETAWarnDate
		adjusted.ETADangerDate = base.ETADangerDate
		adjusted.Curve = base.Curve
		adjusted.Slope = base.Slope
		adjusted.Intercept = base.Intercept
		adjusted.Confidence = base.Confidence
		adjusted.AdvisoryOnly = true
		adjusted.AIStatus = AIStatusOversized
		adjusted.AdjustmentNote = fmt.Sprintf(
			"AI 建议的到线日期偏移 %d 天超过基础窗口 %d 天的 50%%，仅作建议展示。原建议：%s",
			shift, horizon, strings.TrimSpace(adj.Rationale))
		return adjusted, nil
	}

	adjusted.AdvisoryOnly = false
	return adjusted, nil
}

// ParseAIAdjustment 严格解析 AI 返回的 JSON。非对象、字段缺失或类型不符都返回错误，
// 由调用方回退统计预测（非法 JSON 场景见计划 §6.6）。
func ParseAIAdjustment(raw string) (AIAdjustment, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return AIAdjustment{}, fmt.Errorf("AI 返回为空")
	}
	// 容错剥离 ```json 代码块，但内容仍按严格 JSON 校验。
	cleaned := stripCodeFence(trimmed)
	dec := json.NewDecoder(strings.NewReader(cleaned))
	dec.DisallowUnknownFields()
	var adj AIAdjustment
	if err := dec.Decode(&adj); err != nil {
		return AIAdjustment{}, fmt.Errorf("AI 返回不是合法 JSON: %w", err)
	}
	if err := validateAIAdjustment(adj); err != nil {
		return AIAdjustment{}, err
	}
	return adj, nil
}

func validateAIAdjustment(adj AIAdjustment) error {
	if math.IsNaN(adj.SlopeMultiplier) || math.IsInf(adj.SlopeMultiplier, 0) {
		return fmt.Errorf("斜率系数非数值")
	}
	if adj.SlopeMultiplier < aiSlopeMultiplierMin || adj.SlopeMultiplier > aiSlopeMultiplierMax {
		return fmt.Errorf("斜率系数 %.4f 超出 [%.1f, %.1f]", adj.SlopeMultiplier, aiSlopeMultiplierMin, aiSlopeMultiplierMax)
	}
	if math.IsNaN(adj.InterceptAdjustPct) || math.IsInf(adj.InterceptAdjustPct, 0) {
		return fmt.Errorf("截距调整非数值")
	}
	if adj.InterceptAdjustPct < aiInterceptPctMin || adj.InterceptAdjustPct > aiInterceptPctMax {
		return fmt.Errorf("截距调整 %.2f%% 超出 [%.0f%%, %.0f%%]", adj.InterceptAdjustPct, aiInterceptPctMin, aiInterceptPctMax)
	}
	if math.IsNaN(adj.ConfidenceAdjustment) || math.IsInf(adj.ConfidenceAdjustment, 0) {
		return fmt.Errorf("置信度调整非数值")
	}
	if adj.ConfidenceAdjustment < aiConfidenceAdjMin || adj.ConfidenceAdjustment > aiConfidenceAdjMax {
		return fmt.Errorf("置信度调整 %.4f 超出 [%.1f, %.1f]", adj.ConfidenceAdjustment, aiConfidenceAdjMin, aiConfidenceAdjMax)
	}
	rationale := strings.TrimSpace(adj.Rationale)
	if len([]rune(rationale)) < aiRationaleMinLen {
		return fmt.Errorf("解释为空或过短")
	}
	if len([]rune(rationale)) > aiRationaleMaxLen {
		return fmt.Errorf("解释超过 %d 字", aiRationaleMaxLen)
	}
	return nil
}

func adjustCurve(curve []CurvePoint, slopeMultiplier, interceptPct float64) []CurvePoint {
	if len(curve) == 0 {
		return curve
	}
	// 以预测起点为基准做相对缩放：起点按截距百分比整体抬降，斜率部分按相对
	// 起点的增量放大/缩小。这样调整量与实际量纲一致，不会对高基数指标失真。
	interceptDelta := curve[0].Value * interceptPct / 100
	out := make([]CurvePoint, len(curve))
	for i, p := range curve {
		delta := interceptDelta + (slopeMultiplier-1)*(p.Value-curve[0].Value)
		shift := round4(delta)
		point := CurvePoint{
			BizDate: p.BizDate,
			Value:   math.Max(0, round4(p.Value+shift)),
			Lower:   math.Max(0, round4(p.Lower+shift)),
			Upper:   math.Max(0, round4(p.Upper+shift)),
		}
		out[i] = point
	}
	return out
}

// firstCurveCrossing 在调整后的曲线上找首个越过阈值的工作日，找不到时回退统计 ETA。
// 曲线本身已按容量值截断到 0，这里只负责“首次跨越”的判定。
func firstCurveCrossing(adjustedCurve []CurvePoint, statETA *time.Time, threshold *float64, upward bool) *time.Time {
	if threshold == nil || len(adjustedCurve) == 0 {
		return statETA
	}
	for _, p := range adjustedCurve {
		if upward && p.Value >= *threshold {
			d := startDay(p.BizDate)
			return &d
		}
		if !upward && p.Value <= *threshold {
			d := startDay(p.BizDate)
			return &d
		}
	}
	return statETA
}

type adjustThresholds struct {
	warn   *float64
	danger *float64
}

// thresholdFromCurve 从基础结果中恢复阈值：Base/Adjusted ETA 与曲线不能带阈值，
// 因此以“统计 ETA 对应的曲线点值”作为阈值的近似。若没有 ETA 则无阈值。
func thresholdFromCurve(base ForecastResult) adjustThresholds {
	// ForecastResult 未携带阈值本身，调用方需要时可通过 Explain 文本；此处以
	// 曲线上与 ETA 同日的取值做近似，保证纯函数不引入额外依赖。
	return adjustThresholds{
		warn:   curveValueOn(base.Curve, base.ETAWarnDate),
		danger: curveValueOn(base.Curve, base.ETADangerDate),
	}
}

func curveValueOn(curve []CurvePoint, day *time.Time) *float64 {
	if day == nil {
		return nil
	}
	target := startDay(*day)
	for _, p := range curve {
		if startDay(p.BizDate).Equal(target) {
			v := p.Value
			return &v
		}
	}
	return nil
}

func curveOrigin(curve []CurvePoint) time.Time {
	if len(curve) == 0 {
		return time.Time{}
	}
	return startDay(curve[0].BizDate)
}

func maxETAShiftDays(base, adjusted ForecastResult) int {
	maxShift := 0
	for _, pair := range [][2]*time.Time{
		{base.ETAWarnDate, adjusted.ETAWarnDate},
		{base.ETADangerDate, adjusted.ETADangerDate},
	} {
		if pair[0] == nil || pair[1] == nil {
			continue
		}
		shift := absInt(daysBetween(startDay(*pair[0]), startDay(*pair[1])))
		if shift > maxShift {
			maxShift = shift
		}
	}
	return maxShift
}

func aiExplanation(base, adjusted ForecastResult, adj AIAdjustment) string {
	switch {
	case base.Explanation == "":
		return strings.TrimSpace(adj.Rationale)
	case adjusted.Slope == base.Slope && adjusted.Intercept == base.Intercept:
		return base.Explanation
	default:
		return base.Explanation + "；AI 修正：" + strings.TrimSpace(adj.Rationale)
	}
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return s
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
