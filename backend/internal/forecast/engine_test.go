package forecast

import (
	"math"
	"sort"
	"testing"
	"time"
)

func testSeries(start time.Time, values ...float64) Series {
	points := make([]Point, 0, len(values))
	for i, v := range values {
		points = append(points, Point{BizDate: start.AddDate(0, 0, i), Value: v})
	}
	return Series{
		MetricCode:     "M7_TEST",
		JudgeDirection: "higher_worse",
		WarnLine:       ptr(80.0),
		DangerLine:     ptr(95.0),
		Points:         points,
		QualityScore:   1,
	}
}

func ptr(v float64) *float64 { return &v }

func TestForecastLinearComputesETAConfidenceAndRisk(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	values := make([]float64, 30)
	for i := range values {
		values[i] = 40 + float64(i)*1.5
	}
	series := testSeries(start, values...)

	got := ForecastLinear(series, 30)

	if got.Status != StatusSuccess {
		t.Fatalf("status=%q explanation=%q", got.Status, got.Explanation)
	}
	if got.ModelType != ModelLinear {
		t.Fatalf("model=%q", got.ModelType)
	}
	if got.Slope <= 1.49 || got.Slope >= 1.51 {
		t.Fatalf("slope=%v", got.Slope)
	}
	wantETA := start.AddDate(0, 0, 27)
	if got.ETAWarnDate == nil || !got.ETAWarnDate.Equal(wantETA) {
		t.Fatalf("eta warn=%v want %v", got.ETAWarnDate, wantETA)
	}
	if got.ETADangerDate == nil || got.ETADangerDate.Before(start.AddDate(0, 0, 36)) {
		t.Fatalf("eta danger should be outside horizon, got %v", got.ETADangerDate)
	}
	if got.Confidence <= 0 || got.Confidence > 1 {
		t.Fatalf("confidence=%v", got.Confidence)
	}
	if got.RiskScore <= 0 || got.RiskScore > 1 {
		t.Fatalf("risk=%v", got.RiskScore)
	}
	if len(got.Curve) != 30 {
		t.Fatalf("curve len=%d", len(got.Curve))
	}
	for _, p := range got.Curve {
		if p.Lower < 0 {
			t.Fatalf("lower bound must be clamped to zero: %+v", p)
		}
		if p.Lower > p.Value || p.Value > p.Upper {
			t.Fatalf("invalid interval: %+v", p)
		}
	}
}

// 到线紧迫度必须以预测起点(最后一个有效数据日)计算, 否则窗口越长风险分越低。
func TestRiskUrgencyIsMeasuredFromForecastOrigin(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	values := make([]float64, 30)
	for i := range values {
		values[i] = 40 + float64(i)*0.4 // 40.0 -> 51.6, 预测起点距预警线约 9 天
	}
	series := testSeries(start, values...)
	series.WarnLine = ptr(55.0)
	series.DangerLine = ptr(60.0)

	got := ForecastLinear(series, 30)

	if got.Status != StatusSuccess {
		t.Fatalf("status=%q explanation=%q", got.Status, got.Explanation)
	}
	wantETA := start.AddDate(0, 0, 38)
	if got.ETAWarnDate == nil || !got.ETAWarnDate.Equal(wantETA) {
		t.Fatalf("eta warn=%v want %v", got.ETAWarnDate, wantETA)
	}
	// 0.40*紧迫度(约 0.70) + 趋势强度 + 置信度 + 数据质量; 若以窗口起点计算紧急度为 0
	if got.RiskScore < 0.5 {
		t.Fatalf("risk=%v, 紧迫度未按预测起点计算", got.RiskScore)
	}
}

// M7 准出回测指标: 稳定趋势指标 7 天留出 MAPE 不超过 15%, ETA 与真值偏差不超过 7 天。
func TestBacktestAccuracyOnStableTrend(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	maes := make([]float64, 0, 12)
	etaOffsets := make([]float64, 0, 12)
	for s := 0; s < 12; s++ {
		values := make([]float64, 0, 60)
		for i := 0; i < 60; i++ {
			// 稳定日增 0.5 + 固定周期噪声(幅值 ±0.3, 相对趋势 <1%)
			values = append(values, 40+float64(i)*0.5+0.3*math.Sin(float64(i*3+s)))
		}
		series := testSeries(start.AddDate(0, 0, s), values...)
		series.WarnLine = ptr(75.0)
		series.DangerLine = ptr(85.0)

		backtest := EvaluateBacktest(series, 7)
		got, ok := backtest[ModelLinear]
		if !ok {
			t.Fatalf("backtest 缺少线性模型结果: %v", backtest)
		}
		maes = append(maes, got)

		result := ForecastLinear(series, 30)
		if result.Status != StatusSuccess || result.ETAWarnDate == nil {
			t.Fatalf("样本 %d 未产出 ETA: status=%q", s, result.Status)
		}
		// 真值: 预测起点真实值到预警线的天数
		lastValue := values[len(values)-1]
		truthDays := (75.0 - lastValue) / 0.5
		dataEnd := start.AddDate(0, 0, s+len(values)-1)
		offset := math.Abs(result.ETAWarnDate.Sub(dataEnd).Hours()/24 - truthDays)
		etaOffsets = append(etaOffsets, offset)
	}
	medianMAE := medianFloat(maes)
	if medianMAE > 15 {
		t.Fatalf("MAPE 中位数 %.4f%% 超过 15%%", medianMAE)
	}
	t.Logf("12 个稳定趋势样本: MAPE 中位数 %.2f%%, ETA 最大偏差 %.1f 天",
		medianMAE, maxFloat(etaOffsets))
	for i, offset := range etaOffsets {
		if offset > 7 {
			t.Fatalf("样本 %d ETA 偏差 %.1f 天超过 7 天", i, offset)
		}
	}
}

func medianFloat(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func maxFloat(values []float64) float64 {
	best := 0.0
	for _, v := range values {
		if v > best {
			best = v
		}
	}
	return best
}

func TestForecastLinearSupportsLowerWorseDirection(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	values := make([]float64, 30)
	for i := range values {
		values[i] = 90 - float64(i)*2
	}
	series := testSeries(start, values...)
	series.MetricCode = "M7_FREE"
	series.JudgeDirection = "lower_worse"
	series.WarnLine = ptr(50.0)
	series.DangerLine = ptr(25.0)

	got := ForecastLinear(series, 30)

	if got.Status != StatusSuccess {
		t.Fatalf("status=%q explanation=%q", got.Status, got.Explanation)
	}
	if got.Slope >= 0 {
		t.Fatalf("slope=%v", got.Slope)
	}
	wantETA := start.AddDate(0, 0, 20)
	if got.ETAWarnDate == nil || !got.ETAWarnDate.Equal(wantETA) {
		t.Fatalf("eta warn=%v want %v", got.ETAWarnDate, wantETA)
	}
}

func TestForecastReturnsExplicitSkipStates(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	short := testSeries(start, 10, 11, 12)
	if got := ForecastLinear(short, 30); got.Status != StatusInsufficientData {
		t.Fatalf("short status=%q", got.Status)
	}

	enough := testSeries(start,
		10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25,
		26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
	)
	enough.QualityScore = 0.79
	enough.QualityKnown = true
	if got := ForecastLinear(enough, 30); got.Status != StatusInsufficientQuality {
		t.Fatalf("quality status=%q", got.Status)
	}

	flat := testSeries(start,
		20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
		20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
	)
	if got := ForecastLinear(flat, 30); got.Status != StatusNever {
		t.Fatalf("flat status=%q", got.Status)
	}

	noThreshold := testSeries(start,
		10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25,
		26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
	)
	noThreshold.WarnLine = nil
	if got := ForecastLinear(noThreshold, 30); got.Status != StatusNever {
		t.Fatalf("no threshold status=%q", got.Status)
	}
}

func TestForecastGrowthProducesStableMonotonicCurve(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	values := make([]float64, 30)
	values[0] = 100
	for i := 1; i < len(values); i++ {
		values[i] = values[i-1] * 1.02
	}
	series := testSeries(start, values...)
	series.WarnLine = ptr(170)
	series.DangerLine = ptr(220)

	got := ForecastGrowth(series, 30)

	if got.Status != StatusSuccess {
		t.Fatalf("status=%q explanation=%q", got.Status, got.Explanation)
	}
	if got.ModelType != ModelGrowth {
		t.Fatalf("model=%q", got.ModelType)
	}
	if len(got.Curve) != 30 {
		t.Fatalf("curve len=%d", len(got.Curve))
	}
	for i := 1; i < len(got.Curve); i++ {
		if got.Curve[i].Value <= got.Curve[i-1].Value {
			t.Fatalf("growth curve is not monotonic at %d: %v", i, got.Curve[i].Value)
		}
	}
}

func TestSelectBestModelUsesDeterministicBacktestWinner(t *testing.T) {
	linear := ForecastResult{ModelType: ModelLinear, Status: StatusSuccess, BacktestMAPE: 0.20}
	growth := ForecastResult{ModelType: ModelGrowth, Status: StatusSuccess, BacktestMAPE: 0.10}
	got := SelectBestModel([]ForecastResult{linear, growth}, map[string]float64{
		ModelLinear: 0.12,
		ModelGrowth: 0.08,
	})
	if got.ModelType != ModelGrowth {
		t.Fatalf("model=%q", got.ModelType)
	}

	got = SelectBestModel([]ForecastResult{linear, growth}, nil)
	if got.ModelType != ModelGrowth {
		t.Fatalf("fallback model=%q", got.ModelType)
	}
}

func TestPowerLawGrowthRateIsBounded(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	series := testSeries(start,
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30,
	)
	got := ForecastGrowth(series, 90)
	for _, p := range got.Curve {
		if math.IsInf(p.Value, 0) || math.IsNaN(p.Value) || p.Value < 0 {
			t.Fatalf("invalid curve point: %+v", p)
		}
	}
}
