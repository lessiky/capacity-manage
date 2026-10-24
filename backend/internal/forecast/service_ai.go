package forecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
)

// AIClient 是模型侧的对话适配器（与 internal/ai 解耦，避免循环依赖）。
type AIClient interface {
	Chat(ctx context.Context, system, user string) (content, model string, err error)
	Enabled() bool
}

// SetAI 注入 AI 能力。client 为 nil 或 Enabled()=false 时预测完全走统计路径，
// 满足计划 §9.3「AI 服务不可用时自动回退统计预测」。
func (s *Service) SetAI(client AIClient) {
	s.ai = client
}

// AIEnabled 供批次记录使用。
func (s *Service) AIEnabled() bool {
	return s != nil && s.ai != nil && s.ai.Enabled()
}

// confirmEventForAI 是发送给模型的事件摘要，只含脱敏后的结构化字段。
type confirmEventForAI struct {
	Name            string  `json:"name"`
	EventType       string  `json:"eventType"`
	ScopeType       string  `json:"scopeType"`
	ScopeValue      string  `json:"scopeValue"`
	StartDate       string  `json:"startDate"`
	EndDate         string  `json:"endDate,omitempty"`
	ImpactDirection string  `json:"impactDirection"`
	ImpactPct       float64 `json:"impactPct"`
	Confidence      float64 `json:"confidence"`
}

// activeEvents 返回与预测窗口 [dataEnd, dataEnd+horizonDays] 有交集的已确认事件：
// 既包含数据截止日仍在进行的事件，也包含窗口内即将开始的事件（如大促、迁移），
// 但不含在 dataEnd 之前就结束的历史事件。事件按 id 排序，保证同一输入集合产生
// 相同的事件版本摘要（可复现要求）。
func (s *Service) activeEvents(ctx context.Context, dataEnd time.Time, horizonDays int) ([]persistence.ForecastEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	windowEnd := startDay(dataEnd).AddDate(0, 0, horizonDays)
	var events []persistence.ForecastEvent
	err := s.db.WithContext(ctx).
		Where("status = ?", EventStatusConfirmed).
		Where("start_date <= ?", windowEnd).
		Where("end_date IS NULL OR end_date >= ?", startDay(dataEnd)).
		Order("id").
		Find(&events).Error
	return events, err
}

// eventSetVersion 用事件集合的内容摘要作为版本号，事件变更即版本变化。
func eventSetVersion(events []persistence.ForecastEvent) string {
	if len(events) == 0 {
		return ""
	}
	type digestEvent struct {
		ID              int64   `json:"id"`
		Version         int     `json:"version"`
		Name            string  `json:"name"`
		StartDate       string  `json:"startDate"`
		EndDate         string  `json:"endDate,omitempty"`
		ImpactDirection string  `json:"impactDirection"`
		ImpactPct       float64 `json:"impactPct"`
		Confidence      float64 `json:"confidence"`
		ScopeType       string  `json:"scopeType"`
		ScopeValue      string  `json:"scopeValue"`
	}
	payload := make([]digestEvent, 0, len(events))
	for _, ev := range events {
		end := ""
		if ev.EndDate != nil {
			end = startDay(*ev.EndDate).Format("2006-01-02")
		}
		pct, conf := 0.0, 1.0
		if ev.ImpactPct != nil {
			pct = *ev.ImpactPct
		}
		if ev.Confidence != nil {
			conf = *ev.Confidence
		}
		payload = append(payload, digestEvent{
			ID: ev.ID, Version: ev.Version, Name: ev.Name,
			StartDate: startDay(ev.StartDate).Format("2006-01-02"), EndDate: end,
			ImpactDirection: ev.ImpactDirection, ImpactPct: pct, Confidence: conf,
			ScopeType: ev.ScopeType, ScopeValue: ev.ScopeValue,
		})
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "ev-" + hex.EncodeToString(sum[:6])
}

// ForecastAISummary 是允许发送给模型的统计摘要：不含任何原始业务明细。
type ForecastAISummary struct {
	MetricCode     string              `json:"metricCode"`
	Unit           string              `json:"unit"`
	JudgeDirection string              `json:"judgeDirection"`
	WarnLine       *float64            `json:"warnLine,omitempty"`
	DangerLine     *float64            `json:"dangerLine,omitempty"`
	HorizonDays    int                 `json:"horizonDays"`
	Slope          float64             `json:"slope"`
	Intercept      float64             `json:"intercept"`
	ResidualStd    float64             `json:"residualStd"`
	Confidence     float64             `json:"confidence"`
	QualityScore   float64             `json:"qualityScore"`
	ETAWarnDate    string              `json:"etaWarnDate,omitempty"`
	ETADangerDate  string              `json:"etaDangerDate,omitempty"`
	WeekendRatio   float64             `json:"weekendRatio"`
	Events         []confirmEventForAI `json:"events,omitempty"`
}

const forecastAISystemPrompt = `你是容量管理平台的预测修正助手。输入只包含脱敏统计摘要与已确认事件，不含原始业务明细。
请判断节假日、季节性与已确认事件对趋势的影响，只输出严格 JSON：
{"slopeMultiplier":<0.5~3.0>,"interceptAdjustmentPct":<-30~30>,"confidenceAdjustment":<-0.2~0.2>,"rationale":"<不少于4字的中文解释>"}
不要输出 JSON 之外的任何内容；无法判断时给出 slopeMultiplier=1、interceptAdjustmentPct=0、confidenceAdjustment=0 并说明理由。`

// RequestAIAdjustment 组装脱敏输入并调用模型，返回解析后的修正建议。
// 模型未启用、调用失败或输出非法时返回错误，由调用方回退统计预测。
func (s *Service) RequestAIAdjustment(ctx context.Context, summary ForecastAISummary) (AIAdjustment, string, error) {
	if !s.AIEnabled() {
		return AIAdjustment{}, "", fmt.Errorf("AI 未启用")
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return AIAdjustment{}, "", err
	}
	content, model, err := s.ai.Chat(ctx, forecastAISystemPrompt, string(payload))
	if err != nil {
		return AIAdjustment{}, model, err
	}
	adj, err := ParseAIAdjustment(content)
	if err != nil {
		return AIAdjustment{}, model, err
	}
	return adj, model, nil
}

// buildAISummary 从指标与统计结果生成脱敏摘要。周末占比按训练窗口内的
// 有效点统计，不暴露任何具体业务值明细。
func buildAISummary(metric persistence.CapacityMetric, series Series, result ForecastResult,
	events []persistence.ForecastEvent, quality float64) ForecastAISummary {
	summary := ForecastAISummary{
		MetricCode: metric.MetricCode, Unit: metric.Unit,
		JudgeDirection: metric.JudgeDirection,
		WarnLine:       metric.WarnLine, DangerLine: metric.DangerLine,
		HorizonDays: result.HorizonDays,
		Slope:       result.Slope, Intercept: result.Intercept,
		ResidualStd: result.ResidualStd, Confidence: result.Confidence,
		QualityScore: quality,
	}
	if result.ETAWarnDate != nil {
		summary.ETAWarnDate = startDay(*result.ETAWarnDate).Format("2006-01-02")
	}
	if result.ETADangerDate != nil {
		summary.ETADangerDate = startDay(*result.ETADangerDate).Format("2006-01-02")
	}
	weekend, total := 0, len(series.Points)
	for _, p := range series.Points {
		if wd := p.BizDate.Weekday(); wd == time.Saturday || wd == time.Sunday {
			weekend++
		}
	}
	if total > 0 {
		summary.WeekendRatio = round4(float64(weekend) / float64(total))
	}
	for _, ev := range events {
		if !eventCoversMetric(ev, metric) {
			continue
		}
		item := confirmEventForAI{
			Name: ev.Name, EventType: ev.EventType, ScopeType: ev.ScopeType, ScopeValue: ev.ScopeValue,
			StartDate: startDay(ev.StartDate).Format("2006-01-02"), ImpactDirection: ev.ImpactDirection,
		}
		if ev.EndDate != nil {
			item.EndDate = startDay(*ev.EndDate).Format("2006-01-02")
		}
		if ev.ImpactPct != nil {
			item.ImpactPct = *ev.ImpactPct
		}
		if ev.Confidence != nil {
			item.Confidence = *ev.Confidence
		}
		summary.Events = append(summary.Events, item)
	}
	sort.SliceStable(summary.Events, func(i, j int) bool {
		if summary.Events[i].Name != summary.Events[j].Name {
			return summary.Events[i].Name < summary.Events[j].Name
		}
		return summary.Events[i].StartDate < summary.Events[j].StartDate
	})
	return summary
}

func trimForDigest(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n])
}

// AIAdjustmentRecord 是一次 AI 修正的可持久化记录（AI 调节前后快照）。
type AIAdjustmentRecord struct {
	MetricCode       string
	HorizonDays      int
	Status           string
	Model            string
	SlopeMultiplier  float64
	InterceptAdjPct  float64
	ConfidenceAdj    float64
	Rationale        string
	ETAShiftDays     int
	AdvisoryOnly     bool
	LatencyMS        int
	ErrMsg           string
	BaseSnapshot     string
	AdjustedSnapshot string
}

// applyAdjustments 先施加已确认事件因子，再按需调用 AI 修正。返回的结果始终可
// 用于主流程；第二个返回值非空时表示发生过 AI 调用（成功或降级），需要落库审计。
func (s *Service) applyAdjustments(ctx context.Context, metric persistence.CapacityMetric, series Series,
	result ForecastResult, dataEnd time.Time, events []persistence.ForecastEvent, quality float64) (ForecastResult, *AIAdjustmentRecord) {
	magnitude := averageValue(series.Points)
	result = ApplyEventImpact(result, ComputeEventImpact(metric, events, dataEnd), magnitude)
	if !s.AIEnabled() {
		result.AIStatus = AIStatusDisabled
		return result, nil
	}
	if result.Status != StatusSuccess {
		return result, nil
	}
	summary := buildAISummary(metric, series, result, events, quality)
	baseSnapshot := snapshotForecast(result)
	start := time.Now()
	adj, model, err := s.RequestAIAdjustment(ctx, summary)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		result.AIStatus = AIStatusInvalid
		result.AdjustmentNote = "AI 修正不可用，已回退统计预测：" + trimForDigest(err.Error(), 200)
		return result, &AIAdjustmentRecord{
			Status: AIStatusInvalid, Model: model, LatencyMS: latency,
			ErrMsg:       trimForDigest(err.Error(), 1000),
			BaseSnapshot: baseSnapshot, AdjustedSnapshot: snapshotForecast(result),
		}
	}
	adjusted, _ := ApplyAIAdjustment(result, adj)
	adjusted.AIModel = model
	return adjusted, &AIAdjustmentRecord{
		Status: adjusted.AIStatus, Model: model,
		SlopeMultiplier: adj.SlopeMultiplier, InterceptAdjPct: adj.InterceptAdjustPct,
		ConfidenceAdj: adj.ConfidenceAdjustment, Rationale: trimForDigest(adj.Rationale, 1000),
		ETAShiftDays: maxETAShiftDays(result, adjusted), AdvisoryOnly: adjusted.AdvisoryOnly,
		LatencyMS: latency, BaseSnapshot: baseSnapshot, AdjustedSnapshot: snapshotForecast(adjusted),
	}
}

// snapshotForecast 生成用于对比/回滚的快照。curve 一并写入，使指标详情页可以
// 叠加「统计预测 vs AI 修正」两条曲线；jsonb 列无需额外迁移。
func snapshotForecast(result ForecastResult) string {
	payload := map[string]any{
		"status": result.Status, "modelType": result.ModelType,
		"slope": result.Slope, "intercept": result.Intercept,
		"confidence": result.Confidence, "riskScore": result.RiskScore,
		"etaWarnDate": formatDayPtr(result.ETAWarnDate), "etaDangerDate": formatDayPtr(result.ETADangerDate),
		"aiStatus": result.AIStatus, "advisoryOnly": result.AdvisoryOnly,
		"adjustmentNote": result.AdjustmentNote, "aiModel": result.AIModel,
		"suggestedEtaWarnDate":   formatDayPtr(result.SuggestedETAWarnDate),
		"suggestedEtaDangerDate": formatDayPtr(result.SuggestedETADangerDate),
		"curve":                  result.Curve,
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func formatDayPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return startDay(*t).Format("2006-01-02")
}

func averageValue(points []Point) float64 {
	if len(points) == 0 {
		return 0
	}
	var sum float64
	for _, p := range points {
		sum += p.Value
	}
	return sum / float64(len(points))
}
