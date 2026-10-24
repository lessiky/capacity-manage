package forecast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pkg/scope"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	db      *gorm.DB
	svc     *Service
	az      *middleware.AuthZ
	log     *slog.Logger
	scopeFn func(c *gin.Context) []string
}

func NewHandler(db *gorm.DB, az *middleware.AuthZ, log *slog.Logger) *Handler {
	return &Handler{db: db, svc: NewService(db), az: az, log: log}
}

// SetAI 注入 AI 对话能力 getter（由 app 层从 internal/ai 适配）。getter 每次
// 返回当前生效配置下的对话函数；返回 nil 表示 AI 关闭，预测与服务全部回退
// 统计路径（计划 §9.3，支持系统管理热更新）。
func (h *Handler) SetAI(getter func() func(ctx context.Context, system, user string) (string, string, error)) {
	if getter == nil {
		h.svc.SetAI(nil)
		return
	}
	h.svc.SetAI(aiChatAdapter{getter: getter})
}

// Service 暴露服务实例，便于调度器与测试复用同一 AI 配置。
func (h *Handler) Service() *Service { return h.svc }

type aiChatAdapter struct {
	getter func() func(ctx context.Context, system, user string) (string, string, error)
}

func (a aiChatAdapter) Chat(ctx context.Context, system, user string) (string, string, error) {
	fn := a.getter()
	if fn == nil {
		return "", "", fmt.Errorf("AI 未启用或未配置")
	}
	return fn(ctx, system, user)
}

func (a aiChatAdapter) Enabled() bool { return a.getter != nil && a.getter() != nil }

func (h *Handler) visible(c *gin.Context) []string {
	if h.scopeFn != nil {
		return h.scopeFn(c)
	}
	return scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c))
}

func (h *Handler) MetricForecast(c *gin.Context) {
	metricCode := strings.TrimSpace(c.Param("code"))
	if metricCode == "" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "指标编码不能为空"))
		return
	}
	horizon, ok := parseHorizon(c.Query("horizon"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "horizon 仅支持 30 或 90"))
		return
	}
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", metricCode).First(&metric).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !scope.Contains(h.visible(c), metric.SystemCode) {
		scope.Forbidden(c, metric.SystemCode)
		return
	}
	var result persistence.ForecastResult
	err := h.db.Table("forecast_result r").
		Select("r.*").
		Joins("JOIN forecast_run run ON run.id = r.run_id").
		Where("r.metric_code = ? AND r.horizon_days = ?", metricCode, horizon).
		Order("run.run_date DESC, r.id DESC").
		First(&result).Error
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "该指标暂无预测结果"))
		return
	}
	out, err := resultView(result)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	attachAIAdjustment(h.db, result.ID, out)
	out["metric"] = gin.H{
		"metricCode": metric.MetricCode, "name": metric.Name, "unit": metric.Unit,
		"systemCode": metric.SystemCode, "judgeDirection": metric.JudgeDirection,
		"warnLine": metric.WarnLine, "dangerLine": metric.DangerLine,
	}
	resp.OK(c, out)
}

// attachAIAdjustment 把 AI 修正记录（若存在）合并进预测结果视图。基础快照与
// 修正快照均为 JSON 原文，前端据此叠加统计预测与 AI 修正两条曲线；AI 关闭或
// 降级时该行缺失，视图保持统计结果不变（计划 §9.3 降级提示）。
func attachAIAdjustment(db *gorm.DB, resultID int64, view gin.H) {
	var adj persistence.ForecastAIAdjustment
	if err := db.Where("result_id = ?", resultID).First(&adj).Error; err != nil {
		return
	}
	applyAIAdjustmentView(view, adj)
}

// attachAIAdjustments 批量补齐风险清单的 AI 修正信息，避免逐行查询。
func attachAIAdjustments(db *gorm.DB, views map[int64]gin.H) {
	if len(views) == 0 {
		return
	}
	ids := make([]int64, 0, len(views))
	for id := range views {
		ids = append(ids, id)
	}
	var adjustments []persistence.ForecastAIAdjustment
	if err := db.Where("result_id IN ?", ids).Find(&adjustments).Error; err != nil {
		return
	}
	for _, adj := range adjustments {
		if view, ok := views[adj.ResultID]; ok {
			applyAIAdjustmentView(view, adj)
		}
	}
}

func applyAIAdjustmentView(view gin.H, adj persistence.ForecastAIAdjustment) {
	view["aiStatus"] = adj.Status
	view["advisoryOnly"] = adj.AdvisoryOnly
	view["aiModel"] = adj.Model
	view["aiRationale"] = adj.Rationale
	view["aiError"] = adj.ErrMsg
	view["etaShiftDays"] = adj.ETAShiftDays
	view["aiLatencyMs"] = adj.LatencyMS
	if strings.TrimSpace(adj.BaseSnapshot) != "" {
		view["baseSnapshot"] = json.RawMessage(adj.BaseSnapshot)
	}
	if strings.TrimSpace(adj.AdjustedSnapshot) != "" {
		view["adjustedSnapshot"] = json.RawMessage(adj.AdjustedSnapshot)
		var snap struct {
			AdjustmentNote         string `json:"adjustmentNote"`
			SuggestedETAWarnDate   string `json:"suggestedEtaWarnDate"`
			SuggestedETADangerDate string `json:"suggestedEtaDangerDate"`
		}
		if err := json.Unmarshal([]byte(adj.AdjustedSnapshot), &snap); err == nil {
			if note := strings.TrimSpace(snap.AdjustmentNote); note != "" {
				view["adjustmentNote"] = note
			}
			if warn := strings.TrimSpace(snap.SuggestedETAWarnDate); warn != "" {
				view["suggestedEtaWarnDate"] = warn
			}
			if danger := strings.TrimSpace(snap.SuggestedETADangerDate); danger != "" {
				view["suggestedEtaDangerDate"] = danger
			}
		}
	}
}

func (h *Handler) Risk(c *gin.Context) {
	days := 30
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 7 || n > 90 {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "days 范围为 7~90"))
			return
		}
		days = n
	}
	var run persistence.ForecastRun
	if err := h.db.Where("status = ?", "success").Order("run_date DESC, id DESC").First(&run).Error; err != nil {
		resp.OK(c, gin.H{"run": nil, "items": []any{}, "count": 0})
		return
	}
	until := clock.DayStart(time.Now()).AddDate(0, 0, days)
	var rows []persistence.ForecastResult
	q := h.db.Table("forecast_result r").
		Select("r.*").
		Joins("JOIN capacity_metric m ON m.metric_code = r.metric_code").
		Where("r.run_id = ? AND r.horizon_days IN ?", run.ID, []int{30, 90}).
		Where("r.status IN ?", []string{StatusSuccess, StatusNever})
	if systems := h.visible(c); systems != nil {
		if len(systems) == 0 {
			resp.OK(c, gin.H{"run": run, "items": []any{}, "count": 0})
			return
		}
		q = q.Where("m.system_code IN ?", systems)
	}
	if err := q.Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	byMetric := make(map[string]map[int]persistence.ForecastResult, len(rows))
	for _, row := range rows {
		if byMetric[row.MetricCode] == nil {
			byMetric[row.MetricCode] = make(map[int]persistence.ForecastResult, 2)
		}
		byMetric[row.MetricCode][row.HorizonDays] = row
	}
	preferred := riskHorizonForWindow(days)
	selected := make([]persistence.ForecastResult, 0, len(byMetric))
	for _, horizons := range byMetric {
		row, ok := selectRiskRow(horizons, preferred, until, days)
		if ok {
			selected = append(selected, row)
		}
	}
	metricByCode := make(map[string]persistence.CapacityMetric, len(selected))
	codes := make([]string, 0, len(selected))
	for _, row := range selected {
		codes = append(codes, row.MetricCode)
	}
	if len(codes) > 0 {
		var metrics []persistence.CapacityMetric
		if err := h.db.Where("metric_code IN ?", codes).Find(&metrics).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		for _, metric := range metrics {
			metricByCode[metric.MetricCode] = metric
		}
	}
	items := make([]gin.H, 0, len(selected))
	views := make(map[int64]gin.H, len(selected))
	for _, row := range selected {
		view, err := resultView(row)
		if err != nil {
			continue
		}
		metric := metricByCode[row.MetricCode]
		view["name"] = metric.Name
		view["systemCode"] = metric.SystemCode
		view["unit"] = metric.Unit
		view["warnLine"] = metric.WarnLine
		view["dangerLine"] = metric.DangerLine
		items = append(items, view)
		views[row.ID] = view
	}
	attachAIAdjustments(h.db, views)
	sort.SliceStable(items, func(i, j int) bool {
		ri := number(items[i]["riskScore"])
		rj := number(items[j]["riskScore"])
		if ri != rj {
			return ri > rj
		}
		return stringValue(items[i]["metricCode"]) < stringValue(items[j]["metricCode"])
	})
	resp.OK(c, gin.H{"run": run, "items": items, "count": len(items), "days": days})
}

func (h *Handler) Recompute(c *gin.Context) {
	var req struct {
		Date        string   `json:"date"`
		SystemCodes []string `json:"systemCodes"`
		MetricCodes []string `json:"metricCodes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	runDate := clock.DayStart(time.Now())
	raw := strings.TrimSpace(req.Date)
	if raw == "" {
		raw = strings.TrimSpace(c.Query("date"))
	}
	if raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "date 格式应为 YYYY-MM-DD"))
			return
		}
		runDate = parsed
	}
	filter := RunFilter{
		SystemCodes: normalizeCodes(req.SystemCodes),
		MetricCodes: normalizeCodes(req.MetricCodes),
	}
	out, err := h.svc.RunScopedResult(c.Request.Context(), runDate, filter)
	if err != nil {
		if errors.Is(err, ErrNoMatchedMetrics) {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", err.Error()))
			return
		}
		resp.Fail(c, err)
		return
	}
	detail := "runId=" + strconv.FormatInt(out.Run.ID, 10)
	if !filter.Empty() {
		detail += fmt.Sprintf(" systems=%v metrics=%v", filter.SystemCodes, filter.MetricCodes)
	}
	middleware.Log(c, "forecast", "forecast.recompute", runDate.Format("2006-01-02"), detail)
	resp.OK(c, recomputeView{
		ForecastRun:         *out.Run,
		Scoped:              !filter.Empty(),
		ScopeMetricsTotal:   out.ScopeMetricsTotal,
		ScopeMetricsDone:    out.ScopeMetricsDone,
		ScopeMetricsSkipped: out.ScopeMetricsSkipped,
	})
}

func riskHorizonForWindow(days int) int {
	if days > 30 {
		return 90
	}
	return 30
}

// selectRiskRow 优先使用与时间窗口匹配的预测时窗；该时窗未命中时再看另一个
// 时窗，保证窗口扩大后风险集合不会反而缩小。
func selectRiskRow(rows map[int]persistence.ForecastResult, preferred int, until time.Time, days int) (persistence.ForecastResult, bool) {
	if row, ok := rows[preferred]; ok && riskWithin(row, until, days) {
		return row, true
	}
	other := 30
	if preferred == 30 {
		other = 90
	}
	if row, ok := rows[other]; ok && riskWithin(row, until, days) {
		return row, true
	}
	return persistence.ForecastResult{}, false
}

// recomputeView 在批次对象上补充本次筛选范围的产出计数，保持旧的顶层字段兼容。
type recomputeView struct {
	persistence.ForecastRun
	Scoped              bool `json:"scoped"`
	ScopeMetricsTotal   int  `json:"scopeMetricsTotal"`
	ScopeMetricsDone    int  `json:"scopeMetricsDone"`
	ScopeMetricsSkipped int  `json:"scopeMetricsSkipped"`
}

func normalizeCodes(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func (h *Handler) Runs(c *gin.Context) {
	limit := 30
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	runs, err := h.svc.latestRuns(c.Request.Context(), limit)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, runs)
}

func (h *Handler) RunDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "运行 ID 非法"))
		return
	}
	var run persistence.ForecastRun
	if err := h.db.First(&run, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "预测运行不存在"))
		return
	}
	var rows []persistence.ForecastResult
	q := h.db.Table("forecast_result r").
		Select("r.*").
		Joins("JOIN capacity_metric m ON m.metric_code = r.metric_code").
		Where("r.run_id = ?", id)
	if systems := h.visible(c); systems != nil {
		if len(systems) == 0 {
			resp.OK(c, gin.H{"run": run, "items": []any{}})
			return
		}
		q = q.Where("m.system_code IN ?", systems)
	}
	if err := q.Order("r.metric_code, r.horizon_days").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		view, err := resultView(row)
		if err == nil {
			items = append(items, view)
		}
	}
	resp.OK(c, gin.H{"run": run, "items": items})
}

func parseHorizon(raw string) (int, bool) {
	if raw == "" {
		return 30, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && (n == 30 || n == 90)
}

func resultView(row persistence.ForecastResult) (gin.H, error) {
	var curve []CurvePoint
	if strings.TrimSpace(row.Curve) != "" {
		if err := json.Unmarshal([]byte(row.Curve), &curve); err != nil {
			return nil, err
		}
	}
	return gin.H{
		"id": row.ID, "runId": row.RunID, "metricCode": row.MetricCode,
		"horizonDays": row.HorizonDays, "modelType": row.ModelType, "status": row.Status,
		"etaWarnDate": row.ETAWarnDate, "etaDangerDate": row.ETADangerDate,
		"riskScore": row.RiskScore, "confidence": row.Confidence,
		"slope": row.Slope, "intercept": row.Intercept, "residualStd": row.ResidualStd,
		"dataQuality": row.DataQuality, "explanation": row.Explanation,
		"curve": curve, "inputDigest": row.InputDigest,
		"eventFactorVersion": row.EventFactorVersion,
	}, nil
}

func riskWithin(row persistence.ForecastResult, until time.Time, days int) bool {
	risk := 0.0
	if row.RiskScore != nil {
		risk = *row.RiskScore
	}
	if risk >= 0.7 {
		return true
	}
	for _, eta := range []*time.Time{row.ETAWarnDate, row.ETADangerDate} {
		if eta != nil && !eta.After(until) {
			return true
		}
	}
	return risk > 0 && days >= 90
}

func number(v any) float64 {
	switch n := v.(type) {
	case *float64:
		if n != nil {
			return *n
		}
	case float64:
		return n
	case *float32:
		if n != nil {
			return float64(*n)
		}
	}
	return 0
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
