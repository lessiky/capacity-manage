package forecast

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func forecastHandlerHarness(t *testing.T, visible []string) (*gin.Engine, *Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := forecastHarness(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_API", 80, 95)
	run := persistence.ForecastRun{
		RunDate: now, ModelVersion: ModelVersion, DataEndDate: now,
		Status: "success", MetricsTotal: 1, MetricsDone: 1,
	}
	if err := h.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	eta := now.AddDate(0, 0, 10)
	risk := 0.91
	confidence := 0.88
	quality := 1.0
	curve, _ := json.Marshal([]CurvePoint{{BizDate: eta, Value: 90, Lower: 88, Upper: 92}})
	if err := h.DB.Create(&persistence.ForecastResult{
		RunID: run.ID, MetricCode: "M7_API", HorizonDays: 30, ModelType: ModelLinear,
		Status: StatusSuccess, ETAWarnDate: &eta, RiskScore: &risk, Confidence: &confidence,
		DataQuality: &quality, Explanation: "API test", Curve: string(curve), InputDigest: "digest",
	}).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(h.DB, nil, nil)
	handler.scopeFn = func(*gin.Context) []string { return visible }
	engine := gin.New()
	engine.GET("/metrics/:code/forecast", handler.MetricForecast)
	engine.GET("/forecasts/risk", handler.Risk)
	return engine, handler, h
}

func TestMetricForecastEndpointEnforcesDataScope(t *testing.T) {
	engine, _, _ := forecastHandlerHarness(t, []string{"SYS_M7_API"})
	req := httptest.NewRequest(http.MethodGet, "/metrics/M7_API/forecast?horizon=30", nil)
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"].(float64) != 0 {
		t.Fatalf("body=%v", body)
	}
	data := body["data"].(map[string]any)
	if data["metricCode"] != "M7_API" {
		t.Fatalf("data=%v", data)
	}

	engine, _, _ = forecastHandlerHarness(t, []string{"SYS_OTHER"})
	req = httptest.NewRequest(http.MethodGet, "/metrics/M7_API/forecast?horizon=30", nil)
	resp = httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("forbidden status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestRiskEndpointReturnsScopedHighRiskItems(t *testing.T) {
	engine, _, _ := forecastHandlerHarness(t, []string{"SYS_M7_API"})
	req := httptest.NewRequest(http.MethodGet, "/forecasts/risk?days=30", nil)
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	data := body["data"].(map[string]any)
	if data["count"].(float64) != 1 {
		t.Fatalf("data=%v", data)
	}
}

func TestRecomputeEndpointAcceptsSystemScope(t *testing.T) {
	h := forecastHarness(t)
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_RECOMP_A", 80, 95)
	seedForecastMetric(t, h, "M7_RECOMP_B", 80, 95)
	seedForecastDays(t, h, "M7_RECOMP_A", end, 100, 50, 0.45, 1)
	seedForecastDays(t, h, "M7_RECOMP_B", end, 100, 50, 0.45, 1)

	svc := NewService(h.DB)
	if err := svc.RunDaily(context.Background(), end); err != nil {
		t.Fatal(err)
	}
	var beforeB persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_RECOMP_B").First(&beforeB).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(h.DB, nil, nil)
	engine := gin.New()
	engine.POST("/forecasts/recompute", handler.Recompute)

	body := `{"date":"2026-10-20","systemCodes":["SYS_M7_RECOMP_A"]}`
	req := httptest.NewRequest(http.MethodPost, "/forecasts/recompute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	data := out["data"].(map[string]any)
	if data["scoped"] != true || int(data["scopeMetricsTotal"].(float64)) != 1 {
		t.Fatalf("scoped response=%v", data)
	}
	if int(data["metricsTotal"].(float64)) != 2 {
		t.Fatalf("run-level metricsTotal should keep the other metric: %v", data)
	}
	var afterB persistence.ForecastResult
	if err := h.DB.Where("metric_code = ? AND horizon_days = 30", "M7_RECOMP_B").First(&afterB).Error; err != nil {
		t.Fatal(err)
	}
	if afterB.ID != beforeB.ID {
		t.Fatalf("unselected metric was replaced: before=%d after=%d", beforeB.ID, afterB.ID)
	}

	legacy := httptest.NewRequest(http.MethodPost, "/forecasts/recompute?date=2026-10-20", nil)
	legacyResp := httptest.NewRecorder()
	engine.ServeHTTP(legacyResp, legacy)
	if legacyResp.Code != http.StatusOK {
		t.Fatalf("legacy status=%d body=%s", legacyResp.Code, legacyResp.Body.String())
	}
	var legacyOut map[string]any
	if err := json.Unmarshal(legacyResp.Body.Bytes(), &legacyOut); err != nil {
		t.Fatal(err)
	}
	legacyData := legacyOut["data"].(map[string]any)
	if legacyData["scoped"] != false || int(legacyData["scopeMetricsTotal"].(float64)) != 2 {
		t.Fatalf("legacy response=%v", legacyData)
	}
}

func TestRiskEndpointUsesWindowHorizon(t *testing.T) {
	h := forecastHarness(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_WIN_BOTH", 80, 95)
	seedForecastMetric(t, h, "M7_WIN_ONLY30", 80, 95)
	run := persistence.ForecastRun{
		RunDate: now, ModelVersion: ModelVersion, DataEndDate: now,
		Status: "success", MetricsTotal: 2, MetricsDone: 2,
	}
	if err := h.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	eta10 := now.AddDate(0, 0, 10)
	eta20 := now.AddDate(0, 0, 20)
	eta70 := now.AddDate(0, 0, 70)
	risk := 0.9
	quality := 1.0
	curve, _ := json.Marshal([]CurvePoint{{BizDate: eta70, Value: 96, Lower: 94, Upper: 98}})
	seed := func(code string, horizon int, eta *time.Time) {
		t.Helper()
		if err := h.DB.Create(&persistence.ForecastResult{
			RunID: run.ID, MetricCode: code, HorizonDays: horizon, ModelType: ModelLinear,
			Status: StatusSuccess, ETAWarnDate: eta, RiskScore: &risk, DataQuality: &quality,
			Explanation: "window test", Curve: string(curve), InputDigest: code,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed("M7_WIN_BOTH", 30, &eta10)
	seed("M7_WIN_BOTH", 90, &eta70)
	seed("M7_WIN_ONLY30", 30, &eta20)

	handler := NewHandler(h.DB, nil, nil)
	handler.scopeFn = func(*gin.Context) []string { return []string{"SYS_M7_WIN_BOTH", "SYS_M7_WIN_ONLY30"} }
	engine := gin.New()
	engine.GET("/forecasts/risk", handler.Risk)

	get := func(days int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/forecasts/risk?days=%d", days), nil)
		resp := httptest.NewRecorder()
		engine.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("days=%d status=%d body=%s", days, resp.Code, resp.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["data"].(map[string]any)
	}
	byCode := func(data map[string]any) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		for _, raw := range data["items"].([]any) {
			row := raw.(map[string]any)
			out[row["metricCode"].(string)] = row
		}
		return out
	}

	thirty := byCode(get(30))
	if int(thirty["M7_WIN_BOTH"]["horizonDays"].(float64)) != 30 || !strings.HasPrefix(thirty["M7_WIN_BOTH"]["etaWarnDate"].(string), eta10.Format("2006-01-02")) {
		t.Fatalf("30-day window should use the 30-day row: %v", thirty["M7_WIN_BOTH"])
	}
	if int(thirty["M7_WIN_ONLY30"]["horizonDays"].(float64)) != 30 {
		t.Fatalf("only-30 metric=%v", thirty["M7_WIN_ONLY30"])
	}

	sixty := byCode(get(60))
	if int(sixty["M7_WIN_BOTH"]["horizonDays"].(float64)) != 90 || !strings.HasPrefix(sixty["M7_WIN_BOTH"]["etaWarnDate"].(string), eta70.Format("2006-01-02")) {
		t.Fatalf("60-day window should use the 90-day row: %v", sixty["M7_WIN_BOTH"])
	}
	if int(sixty["M7_WIN_ONLY30"]["horizonDays"].(float64)) != 30 {
		t.Fatalf("60-day window should fall back to 30 for metrics without a 90-day row: %v", sixty["M7_WIN_ONLY30"])
	}

	ninety := byCode(get(90))
	if int(ninety["M7_WIN_BOTH"]["horizonDays"].(float64)) != 90 {
		t.Fatalf("90-day window=%v", ninety["M7_WIN_BOTH"])
	}
}

func TestRiskEndpointExposesAIAdjustment(t *testing.T) {
	h := forecastHarness(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	seedForecastMetric(t, h, "M7_AI_RISK", 80, 95)
	run := persistence.ForecastRun{
		RunDate: now, ModelVersion: ModelVersion, DataEndDate: now,
		Status: "success", MetricsTotal: 1, MetricsDone: 1,
	}
	if err := h.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	eta := now.AddDate(0, 0, 10)
	risk, quality := 0.9, 1.0
	curve, _ := json.Marshal([]CurvePoint{{BizDate: eta, Value: 90, Lower: 88, Upper: 92}})
	result := persistence.ForecastResult{
		RunID: run.ID, MetricCode: "M7_AI_RISK", HorizonDays: 30, ModelType: ModelLinear,
		Status: StatusSuccess, ETAWarnDate: &eta, RiskScore: &risk, DataQuality: &quality,
		Explanation: "AI risk test", Curve: string(curve), InputDigest: "ai-risk",
	}
	if err := h.DB.Create(&result).Error; err != nil {
		t.Fatal(err)
	}
	suggested := now.AddDate(0, 0, 50).Format("2006-01-02")
	danger := now.AddDate(0, 0, 60).Format("2006-01-02")
	snapshot, _ := json.Marshal(map[string]any{
		"adjustmentNote":         "AI 建议的到线日期偏移超过基础窗口 50%，仅作建议展示。",
		"suggestedEtaWarnDate":   suggested,
		"suggestedEtaDangerDate": danger,
	})
	if err := h.DB.Create(&persistence.ForecastAIAdjustment{
		ResultID: result.ID, RunID: run.ID, MetricCode: "M7_AI_RISK", HorizonDays: 30,
		AIEnabled: true, Status: AIStatusOversized, Model: "gpt-test",
		Rationale: "节假日流量上升，建议提前扩容。", AdjustedSnapshot: string(snapshot),
		AdvisoryOnly: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(h.DB, nil, nil)
	handler.scopeFn = func(*gin.Context) []string { return []string{"SYS_M7_AI_RISK"} }
	engine := gin.New()
	engine.GET("/forecasts/risk", handler.Risk)
	req := httptest.NewRequest(http.MethodGet, "/forecasts/risk?days=30", nil)
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items := body["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%v", items)
	}
	item := items[0].(map[string]any)
	if item["aiStatus"] != AIStatusOversized || item["aiRationale"] != "节假日流量上升，建议提前扩容。" {
		t.Fatalf("ai fields=%v", item)
	}
	if !strings.HasPrefix(item["suggestedEtaWarnDate"].(string), suggested) ||
		!strings.HasPrefix(item["suggestedEtaDangerDate"].(string), danger) {
		t.Fatalf("suggested dates=%v", item)
	}
	if item["adjustmentNote"] == "" {
		t.Fatalf("adjustment note missing: %v", item)
	}
}
