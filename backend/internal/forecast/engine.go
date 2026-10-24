package forecast

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	StatusSuccess             = "success"
	StatusInsufficientData    = "insufficient_data"
	StatusInsufficientQuality = "insufficient_quality"
	StatusNever               = "never"

	ModelLinear = "linear"
	ModelGrowth = "growth"
)

// Point is one valid daily observation. Missing or anomalous days are absent
// from Points and therefore do not silently become zero observations.
type Point struct {
	BizDate time.Time `json:"bizDate"`
	Value   float64   `json:"value"`
}

type Series struct {
	MetricCode     string   `json:"metricCode"`
	JudgeDirection string   `json:"judgeDirection"`
	WarnLine       *float64 `json:"warnLine"`
	DangerLine     *float64 `json:"dangerLine"`
	Points         []Point  `json:"points"`
	QualityScore   float64  `json:"qualityScore"`
	QualityKnown   bool     `json:"qualityKnown"`
}

type CurvePoint struct {
	BizDate time.Time `json:"bizDate"`
	Value   float64   `json:"value"`
	Lower   float64   `json:"lower"`
	Upper   float64   `json:"upper"`
}

type ForecastResult struct {
	ModelType     string       `json:"modelType"`
	Status        string       `json:"status"`
	HorizonDays   int          `json:"horizonDays"`
	ETAWarnDate   *time.Time   `json:"etaWarnDate,omitempty"`
	ETADangerDate *time.Time   `json:"etaDangerDate,omitempty"`
	Slope         float64      `json:"slope"`
	Intercept     float64      `json:"intercept"`
	ResidualStd   float64      `json:"residualStd"`
	Confidence    float64      `json:"confidence"`
	Curve         []CurvePoint `json:"curve"`
	RiskScore     float64      `json:"riskScore"`
	BacktestMAPE  float64      `json:"backtestMape"`
	Explanation   string       `json:"explanation"`
	// M8 AI 辅助修正（计划 §6.3）：AdvisoryOnly 为 true 时主指标仍是统计预测，
	// AI 建议只通过 Suggested* 与 AdjustmentNote 展示。
	AIStatus               string     `json:"aiStatus,omitempty"`
	AdvisoryOnly           bool       `json:"advisoryOnly,omitempty"`
	AdjustmentNote         string     `json:"adjustmentNote,omitempty"`
	SuggestedETAWarnDate   *time.Time `json:"suggestedEtaWarnDate,omitempty"`
	SuggestedETADangerDate *time.Time `json:"suggestedEtaDangerDate,omitempty"`
	// M8 事件因子：记录进入模型的事件版本，供预测结果追溯（计划 §6.4）。
	EventFactorVersion string `json:"eventFactorVersion,omitempty"`
	// M8 AI 审计：记录本次修正使用的模型，便于 ai_call_log 与前端展示。
	AIModel string `json:"aiModel,omitempty"`
}

func ForecastLinear(series Series, horizonDays int) ForecastResult {
	points := normalizePoints(series.Points)
	if !hasEnoughPoints(points, horizonDays) {
		return skipped(ModelLinear, StatusInsufficientData, horizonDays, "有效日数据不足，无法形成统计预测")
	}
	if series.QualityKnown && series.QualityScore < 0.8 {
		result := skipped(ModelLinear, StatusInsufficientQuality, horizonDays, "数据质量分低于 0.8，不输出确定性 ETA")
		result.Slope, result.Intercept = linearFit(points)
		return result
	}
	if len(points) < 2 {
		return skipped(ModelLinear, StatusInsufficientData, horizonDays, "有效日数据不足，无法形成统计预测")
	}

	start := startDay(points[0].BizDate)
	slope, intercept := linearFit(points)
	residual := residualStd(points, slope, intercept)
	target := targetForDirection(slope, series.JudgeDirection)
	threshold := thresholdForDirection(target, series.WarnLine, series.DangerLine)
	modelType := ModelLinear
	result := baseResult(modelType, horizonDays, slope, intercept, residual, points, series.QualityScore)
	if target == 0 || threshold == nil {
		result.Status = StatusNever
		result.Explanation = "当前趋势方向与阈值风险方向不一致或未配置阈值"
		return result
	}
	result.Curve = projectLinear(start, points[len(points)-1], slope, intercept, residual, horizonDays)
	result.ETAWarnDate = firstCrossing(points, result.Curve, series.WarnLine, target, start)
	result.ETADangerDate = firstCrossing(points, result.Curve, series.DangerLine, target, start)
	if result.ETAWarnDate == nil && result.ETADangerDate == nil {
		result.Status = StatusNever
		result.Explanation = "预测窗口内未达到预警线或危险线"
	} else {
		result.Status = StatusSuccess
		result.Explanation = linearExplanation(points, series, result)
	}
	// 到线紧迫度以预测起点(最后一个有效数据日)为基准, 而非训练窗口起点
	result.RiskScore = calculateRisk(result, series, threshold, startDay(points[len(points)-1].BizDate))
	result.BacktestMAPE = linearBacktestMAPE(points, horizonDays)
	return result
}

func ForecastGrowth(series Series, horizonDays int) ForecastResult {
	points := normalizePoints(series.Points)
	if !hasEnoughPoints(points, horizonDays) {
		return skipped(ModelGrowth, StatusInsufficientData, horizonDays, "有效日数据不足，无法形成增长率预测")
	}
	if series.QualityKnown && series.QualityScore < 0.8 {
		result := skipped(ModelGrowth, StatusInsufficientQuality, horizonDays, "数据质量分低于 0.8，不输出确定性 ETA")
		result.Slope, result.Intercept = linearFit(points)
		return result
	}
	last := points[len(points)-1]
	base := average(points[:max(0, len(points)-7)])
	recent := average(points[max(0, len(points)-7):])
	dailyRate := 0.0
	modelType := ModelGrowth
	if base > 0 && recent > 0 {
		dailyRate = math.Pow(recent/base, 1.0/7.0) - 1
	} else {
		modelType = ModelLinear
		dailyRate = last.Value - points[0].Value
		if len(points) > 1 {
			dailyRate /= float64(len(points) - 1)
		}
	}
	dailyRate = clamp(dailyRate, -0.20, 0.20)
	target := targetForDirection(dailyRate, series.JudgeDirection)
	threshold := thresholdForDirection(target, series.WarnLine, series.DangerLine)
	slope := last.Value * dailyRate
	intercept := last.Value - slope*float64(daysBetween(startDay(points[0].BizDate), startDay(last.BizDate)))
	residual := growthResidual(points, dailyRate)
	result := baseResult(modelType, horizonDays, slope, intercept, residual, points, series.QualityScore)
	if target == 0 || threshold == nil {
		result.Status = StatusNever
		result.Explanation = "当前增长率方向与阈值风险方向不一致或未配置阈值"
		return result
	}
	result.Curve = projectGrowth(startDay(last.BizDate), last.Value, dailyRate, residual, horizonDays)
	result.ETAWarnDate = firstCrossing(points, result.Curve, series.WarnLine, target, startDay(points[0].BizDate))
	result.ETADangerDate = firstCrossing(points, result.Curve, series.DangerLine, target, startDay(points[0].BizDate))
	if result.ETAWarnDate == nil && result.ETADangerDate == nil {
		result.Status = StatusNever
		result.Explanation = "预测窗口内未达到预警线或危险线"
	} else {
		result.Status = StatusSuccess
		result.Explanation = fmt.Sprintf(
			"增长率模型按最近 7 日与前一窗口估算日增长率 %.2f%%，预测窗口内出现阈值风险",
			dailyRate*100,
		)
	}
	result.RiskScore = calculateRisk(result, series, threshold, startDay(points[len(points)-1].BizDate))
	result.BacktestMAPE = growthBacktestMAPE(points, horizonDays)
	return result
}

func SelectBestModel(candidates []ForecastResult, historicalError map[string]float64) ForecastResult {
	usable := make([]ForecastResult, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Status == StatusSuccess || candidate.Status == StatusNever {
			usable = append(usable, candidate)
		}
	}
	if len(usable) == 0 {
		if len(candidates) > 0 {
			return candidates[0]
		}
		return ForecastResult{Status: StatusInsufficientData, Explanation: "没有可用预测模型"}
	}
	sort.SliceStable(usable, func(i, j int) bool {
		ei, iok := historicalError[usable[i].ModelType]
		ej, jok := historicalError[usable[j].ModelType]
		switch {
		case iok && jok && ei != ej:
			return ei < ej
		case iok != jok:
			return iok
		case usable[i].BacktestMAPE != usable[j].BacktestMAPE:
			return usable[i].BacktestMAPE < usable[j].BacktestMAPE
		default:
			return modelRank(usable[i].ModelType) < modelRank(usable[j].ModelType)
		}
	})
	return usable[0]
}

func EvaluateBacktest(series Series, holdoutDays int) map[string]float64 {
	points := normalizePoints(series.Points)
	out := map[string]float64{}
	if holdoutDays <= 0 {
		holdoutDays = 7
	}
	if len(points) <= holdoutDays+3 {
		return out
	}
	train := Series{MetricCode: series.MetricCode, JudgeDirection: series.JudgeDirection,
		WarnLine: series.WarnLine, DangerLine: series.DangerLine, QualityScore: series.QualityScore,
		QualityKnown: series.QualityKnown, Points: append([]Point(nil), points[:len(points)-holdoutDays]...)}
	out[ModelLinear] = mape(points[len(points)-holdoutDays:], ForecastLinear(train, holdoutDays).Curve)
	out[ModelGrowth] = mape(points[len(points)-holdoutDays:], ForecastGrowth(train, holdoutDays).Curve)
	return out
}

func normalizePoints(points []Point) []Point {
	if len(points) == 0 {
		return nil
	}
	byDay := make(map[string]Point, len(points))
	for _, p := range points {
		if p.BizDate.IsZero() || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		key := startDay(p.BizDate).Format("2006-01-02")
		byDay[key] = Point{BizDate: startDay(p.BizDate), Value: p.Value}
	}
	out := make([]Point, 0, len(byDay))
	for _, p := range byDay {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BizDate.Before(out[j].BizDate) })
	return out
}

func hasEnoughPoints(points []Point, horizonDays int) bool {
	minimum := 30
	if horizonDays <= 30 {
		minimum = 14
	}
	return len(points) >= minimum
}

func linearFit(points []Point) (float64, float64) {
	if len(points) == 0 {
		return 0, 0
	}
	base := startDay(points[0].BizDate)
	var sumX, sumY, sumXX, sumXY float64
	for _, point := range points {
		x := float64(daysBetween(base, startDay(point.BizDate)))
		sumX += x
		sumY += point.Value
		sumXX += x * x
		sumXY += x * point.Value
	}
	n := float64(len(points))
	denominator := n*sumXX - sumX*sumX
	if math.Abs(denominator) < 1e-12 {
		return 0, sumY / n
	}
	slope := (n*sumXY - sumX*sumY) / denominator
	intercept := (sumY - slope*sumX) / n
	return slope, intercept
}

func residualStd(points []Point, slope, intercept float64) float64 {
	if len(points) < 3 {
		return 0
	}
	base := startDay(points[0].BizDate)
	sum := 0.0
	for _, point := range points {
		x := float64(daysBetween(base, startDay(point.BizDate)))
		residual := point.Value - (intercept + slope*x)
		sum += residual * residual
	}
	return math.Sqrt(sum / float64(len(points)-2))
}

func growthResidual(points []Point, dailyRate float64) float64 {
	if len(points) < 3 {
		return 0
	}
	last := points[len(points)-1].Value
	baseDate := startDay(points[len(points)-1].BizDate)
	sum := 0.0
	for _, point := range points {
		expected := last * math.Pow(1+dailyRate, -float64(daysBetween(startDay(point.BizDate), baseDate)))
		residual := point.Value - expected
		sum += residual * residual
	}
	return math.Sqrt(sum / float64(len(points)-2))
}

func baseResult(modelType string, horizonDays int, slope, intercept, residual float64,
	points []Point, quality float64) ForecastResult {
	if horizonDays <= 0 {
		horizonDays = 30
	}
	if quality <= 0 {
		quality = 1
	}
	confidence := 0.45 + 0.35*rSquared(points, slope, intercept)
	if residual > 0 && math.Abs(intercept) > 0 {
		confidence -= math.Min(0.20, residual/(math.Abs(intercept)+residual)*0.25)
	}
	confidence += 0.15 * (quality - 0.5)
	confidence = clamp(confidence, 0.05, 0.99)
	return ForecastResult{
		ModelType: modelType, Status: StatusSuccess, HorizonDays: horizonDays,
		Slope: slope, Intercept: intercept, ResidualStd: residual, Confidence: confidence,
		Curve: []CurvePoint{},
	}
}

func rSquared(points []Point, slope, intercept float64) float64 {
	if len(points) < 2 {
		return 0
	}
	mean := 0.0
	for _, p := range points {
		mean += p.Value
	}
	mean /= float64(len(points))
	base := startDay(points[0].BizDate)
	ssTot, ssRes := 0.0, 0.0
	for _, p := range points {
		x := float64(daysBetween(base, startDay(p.BizDate)))
		pred := intercept + slope*x
		ssTot += (p.Value - mean) * (p.Value - mean)
		ssRes += (p.Value - pred) * (p.Value - pred)
	}
	if ssTot <= 1e-12 {
		return 0
	}
	return clamp(1-ssRes/ssTot, 0, 1)
}

func projectLinear(base time.Time, last Point, slope, intercept, residual float64, horizon int) []CurvePoint {
	out := make([]CurvePoint, 0, horizon)
	lastDay := daysBetween(base, startDay(last.BizDate))
	for i := 1; i <= horizon; i++ {
		day := startDay(last.BizDate).AddDate(0, 0, i)
		x := float64(lastDay + i)
		value := intercept + slope*x
		out = append(out, curvePoint(day, value, residual))
	}
	return out
}

func projectGrowth(base time.Time, last float64, dailyRate, residual float64, horizon int) []CurvePoint {
	out := make([]CurvePoint, 0, horizon)
	for i := 1; i <= horizon; i++ {
		value := last * math.Pow(1+dailyRate, float64(i))
		out = append(out, curvePoint(base.AddDate(0, 0, i), value, residual))
	}
	return out
}

func curvePoint(day time.Time, value, residual float64) CurvePoint {
	return CurvePoint{
		BizDate: day,
		Value:   value,
		Lower:   math.Max(0, value-1.96*residual),
		Upper:   math.Max(0, value+1.96*residual),
	}
}

func firstCrossing(observed []Point, curve []CurvePoint, threshold *float64, target int, base time.Time) *time.Time {
	if threshold == nil {
		return nil
	}
	for _, point := range observed {
		if crosses(point.Value, *threshold, target) {
			d := startDay(point.BizDate)
			return &d
		}
	}
	for _, point := range curve {
		if crosses(point.Value, *threshold, target) {
			d := startDay(point.BizDate)
			return &d
		}
	}
	_ = base
	return nil
}

func crosses(value, threshold float64, target int) bool {
	if target > 0 {
		return value >= threshold
	}
	return value <= threshold
}

func targetForDirection(trend float64, judgeDirection string) int {
	if strings.EqualFold(judgeDirection, "lower_worse") {
		if trend < 0 {
			return -1
		}
		return 0
	}
	if trend > 0 {
		return 1
	}
	return 0
}

func thresholdForDirection(target int, warn, danger *float64) *float64 {
	if target > 0 {
		if warn != nil {
			return warn
		}
		return danger
	}
	if target < 0 {
		if danger != nil {
			return danger
		}
		return warn
	}
	return nil
}

func calculateRisk(result ForecastResult, series Series, threshold *float64, base time.Time) float64 {
	if result.Status != StatusSuccess || threshold == nil {
		return 0
	}
	eta := result.ETAWarnDate
	if eta == nil {
		eta = result.ETADangerDate
	}
	urgency := 0.05
	if eta != nil {
		days := daysBetween(base, startDay(*eta))
		if result.HorizonDays > 0 {
			urgency = clamp(1-float64(days)/float64(result.HorizonDays), 0, 1)
		}
	}
	trendStrength := 0.0
	if threshold != nil && *threshold > 0 && result.HorizonDays > 0 {
		trendStrength = math.Abs(result.Slope) * float64(result.HorizonDays) / *threshold
		trendStrength = clamp(trendStrength, 0, 1)
	}
	quality := series.QualityScore
	if quality <= 0 {
		quality = 1
	}
	risk := 0.40*urgency + 0.30*trendStrength + 0.20*result.Confidence + 0.10*quality
	return round4(clamp(risk, 0, 1))
}

func linearExplanation(points []Point, series Series, result ForecastResult) string {
	direction := "上升"
	if result.Slope < 0 {
		direction = "下降"
	}
	eta := "窗口内未到线"
	if result.ETAWarnDate != nil {
		eta = result.ETAWarnDate.Format("2006-01-02")
	}
	return fmt.Sprintf("线性模型拟合近 %d 个有效日，趋势%s，预计预警线最早日期为 %s",
		len(points), direction, eta)
}

func linearBacktestMAPE(points []Point, horizon int) float64 {
	if len(points) < 8 {
		return 0
	}
	train := points[:len(points)-7]
	slope, intercept := linearFit(train)
	predictions := make([]CurvePoint, 0, 7)
	base := startDay(train[0].BizDate)
	lastDay := daysBetween(base, startDay(train[len(train)-1].BizDate))
	for i := 1; i <= 7; i++ {
		predictions = append(predictions, CurvePoint{
			BizDate: train[len(train)-1].BizDate.AddDate(0, 0, i),
			Value:   intercept + slope*float64(lastDay+i),
		})
	}
	return mape(points[len(points)-7:], predictions)
}

func growthBacktestMAPE(points []Point, horizon int) float64 {
	if len(points) < 8 {
		return 0
	}
	train := points[:len(points)-7]
	last := train[len(train)-1].Value
	base := average(train[:max(0, len(train)-7)])
	recent := average(train[max(0, len(train)-7):])
	rate := 0.0
	if base > 0 && recent > 0 {
		rate = math.Pow(recent/base, 1.0/7.0) - 1
	}
	rate = clamp(rate, -0.20, 0.20)
	predictions := make([]CurvePoint, 0, 7)
	for i := 1; i <= 7; i++ {
		predictions = append(predictions, CurvePoint{
			BizDate: train[len(train)-1].BizDate.AddDate(0, 0, i),
			Value:   last * math.Pow(1+rate, float64(i)),
		})
	}
	return mape(points[len(points)-7:], predictions)
}

func mape(actual []Point, predicted []CurvePoint) float64 {
	if len(actual) == 0 || len(predicted) == 0 {
		return 0
	}
	n := min(len(actual), len(predicted))
	var sum float64
	var count int
	for i := 0; i < n; i++ {
		if math.Abs(actual[i].Value) < 1e-9 {
			continue
		}
		sum += math.Abs((actual[i].Value - predicted[i].Value) / actual[i].Value)
		count++
	}
	if count == 0 {
		return 0
	}
	return round4(sum / float64(count))
}

func skipped(modelType, status string, horizon int, explanation string) ForecastResult {
	if horizon <= 0 {
		horizon = 30
	}
	return ForecastResult{
		ModelType: modelType, Status: status, HorizonDays: horizon,
		Curve: []CurvePoint{}, RiskScore: 0, Explanation: explanation,
	}
}

func average(points []Point) float64 {
	if len(points) == 0 {
		return 0
	}
	var sum float64
	for _, p := range points {
		sum += p.Value
	}
	return sum / float64(len(points))
}

func modelRank(model string) int {
	switch model {
	case ModelLinear:
		return 0
	case ModelGrowth:
		return 1
	default:
		return 99
	}
}

func startDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func daysBetween(from, to time.Time) int {
	return int(startDay(to).Sub(startDay(from)).Hours() / 24)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
