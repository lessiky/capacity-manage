package forecast

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"

	"gorm.io/gorm"
)

const (
	EventTypeLaunch  = "launch"
	EventTypeMigrate = "migrate"
	EventTypePeak    = "peak"
	EventTypeMaint   = "maint"
	EventTypePolicy  = "policy"
	EventTypeOther   = "other"

	EventScopeGlobal = "global"
	EventScopeSystem = "system"
	EventScopeMetric = "metric"

	EventDirectionUp   = "increase"
	EventDirectionDown = "decrease"

	EventStatusDraft     = "draft"
	EventStatusConfirmed = "confirmed"
	EventStatusExpired   = "expired"
)

var (
	validEventTypes = map[string]bool{
		EventTypeLaunch: true, EventTypeMigrate: true, EventTypePeak: true,
		EventTypeMaint: true, EventTypePolicy: true, EventTypeOther: true,
	}
	validEventScopes = map[string]bool{
		EventScopeGlobal: true, EventScopeSystem: true, EventScopeMetric: true,
	}
	validEventDirections = map[string]bool{
		EventDirectionUp: true, EventDirectionDown: true,
	}
)

// EventSuggestion 是 AI 对事件影响给出的建议（计划 §6.4），默认只落 draft。
type EventSuggestion struct {
	ImpactDirection string  `json:"impactDirection"`
	ImpactPct       float64 `json:"impactPct"`
	Confidence      float64 `json:"confidence"`
	Rationale       string  `json:"rationale"`
}

// EventInput 是事件录入/变更的请求载体。
type EventInput struct {
	Name            string     `json:"name"`
	EventType       string     `json:"eventType"`
	ScopeType       string     `json:"scopeType"`
	ScopeValue      string     `json:"scopeValue"`
	StartDate       time.Time  `json:"startDate"`
	EndDate         *time.Time `json:"endDate"`
	ImpactDirection string     `json:"impactDirection"`
	ImpactPct       *float64   `json:"impactPct"`
	Confidence      *float64   `json:"confidence"`
	Description     string     `json:"description"`
}

// UnmarshalJSON 兼容前端表单口径 "YYYY-MM-DD" 与 API 直接调用的 RFC3339。
// 前端 el-date-picker 使用 value-format="YYYY-MM-DD"，若直接按 time.Time 反
// 序列化会因缺少时间与时区而报 40001，故在此统一解析。
func (in *EventInput) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name            string   `json:"name"`
		EventType       string   `json:"eventType"`
		ScopeType       string   `json:"scopeType"`
		ScopeValue      string   `json:"scopeValue"`
		StartDate       string   `json:"startDate"`
		EndDate         *string  `json:"endDate"`
		ImpactDirection string   `json:"impactDirection"`
		ImpactPct       *float64 `json:"impactPct"`
		Confidence      *float64 `json:"confidence"`
		Description     string   `json:"description"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	start, err := parseEventDay(raw.StartDate)
	if err != nil && raw.StartDate != "" {
		return err
	}
	var end *time.Time
	if raw.EndDate != nil && strings.TrimSpace(*raw.EndDate) != "" {
		parsed, err := parseEventDay(*raw.EndDate)
		if err != nil {
			return err
		}
		end = &parsed
	}
	*in = EventInput{
		Name: raw.Name, EventType: raw.EventType, ScopeType: raw.ScopeType,
		ScopeValue: raw.ScopeValue, StartDate: start, EndDate: end,
		ImpactDirection: raw.ImpactDirection, ImpactPct: raw.ImpactPct,
		Confidence: raw.Confidence, Description: raw.Description,
	}
	return nil
}

// parseEventDay 解析 "YYYY-MM-DD"、"YYYY-MM-DDTHH:MM:SS" 与 RFC3339，统一按本地日 00:00 归一。
func parseEventDay(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()), nil
		}
	}
	return time.Time{}, fmt.Errorf("日期格式应为 YYYY-MM-DD")
}

func (in EventInput) Validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("事件名称不能为空")
	}
	if len([]rune(in.Name)) > 128 {
		return fmt.Errorf("事件名称过长")
	}
	if !validEventTypes[in.EventType] {
		return fmt.Errorf("事件类型不合法")
	}
	if !validEventScopes[in.ScopeType] {
		return fmt.Errorf("作用域类型不合法")
	}
	if in.ScopeType == EventScopeGlobal && strings.TrimSpace(in.ScopeValue) != "" {
		return fmt.Errorf("全局事件不需要作用域取值")
	}
	if in.ScopeType != EventScopeGlobal && strings.TrimSpace(in.ScopeValue) == "" {
		return fmt.Errorf("系统/指标事件必须指定作用域取值")
	}
	if in.StartDate.IsZero() {
		return fmt.Errorf("开始日不能为空")
	}
	if in.EndDate != nil && in.EndDate.Before(in.StartDate) {
		return fmt.Errorf("结束日不能早于开始日")
	}
	if !validEventDirections[in.ImpactDirection] {
		return fmt.Errorf("影响方向不合法")
	}
	if in.ImpactPct != nil {
		if math.IsNaN(*in.ImpactPct) || math.IsInf(*in.ImpactPct, 0) {
			return fmt.Errorf("预计影响百分比非数值")
		}
		if *in.ImpactPct < -100 || *in.ImpactPct > 100 {
			return fmt.Errorf("预计影响百分比超出 [-100, 100]")
		}
	}
	if in.Confidence != nil {
		if math.IsNaN(*in.Confidence) || math.IsInf(*in.Confidence, 0) {
			return fmt.Errorf("置信度非数值")
		}
		if *in.Confidence < 0 || *in.Confidence > 1 {
			return fmt.Errorf("置信度超出 [0, 1]")
		}
	}
	if len([]rune(in.Description)) > 1024 {
		return fmt.Errorf("事件描述过长")
	}
	return nil
}

// ParseEventSuggestion 严格解析 AI 事件影响建议：字段缺失、越界或解释为空都拒绝，
// 调用方回退为「保留用户录入值 + 不写入建议」。
func ParseEventSuggestion(raw string) (EventSuggestion, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return EventSuggestion{}, fmt.Errorf("AI 返回为空")
	}
	dec := json.NewDecoder(strings.NewReader(stripCodeFence(trimmed)))
	dec.DisallowUnknownFields()
	var s EventSuggestion
	if err := dec.Decode(&s); err != nil {
		return EventSuggestion{}, fmt.Errorf("AI 返回不是合法 JSON: %w", err)
	}
	if !validEventDirections[s.ImpactDirection] {
		return EventSuggestion{}, fmt.Errorf("影响方向不合法: %s", s.ImpactDirection)
	}
	if math.IsNaN(s.ImpactPct) || s.ImpactPct < -100 || s.ImpactPct > 100 {
		return EventSuggestion{}, fmt.Errorf("预计影响百分比超出 [-100, 100]")
	}
	if math.IsNaN(s.Confidence) || s.Confidence < 0 || s.Confidence > 1 {
		return EventSuggestion{}, fmt.Errorf("置信度超出 [0, 1]")
	}
	if len([]rune(strings.TrimSpace(s.Rationale))) < 4 {
		return EventSuggestion{}, fmt.Errorf("解释为空或过短")
	}
	return s, nil
}

// EventImpact 是事件对某指标的合成影响：按确认事件的置信度加权后的净百分比。
type EventImpact struct {
	Pct     float64
	Version string
	Events  int
}

// EventAdjustmentVersion 是进入模型的事件版本标识（预测结果记录
// event_factor_version，计划 §6.4）。
func EventAdjustmentVersion(events int) string {
	if events == 0 {
		return ""
	}
	return fmt.Sprintf("events-%d", events)
}

// ComputeEventImpact 按作用域/日期窗口合成确认事件的影响。全局、系统、指标
// 三级作用域都计入；同一事件只算一次；置信度缺省按 1 处理。
func ComputeEventImpact(metric persistence.CapacityMetric, events []persistence.ForecastEvent, dataEnd time.Time) EventImpact {
	end := startDay(dataEnd)
	var weightSum, pctSum float64
	used := 0
	for _, ev := range events {
		if ev.Status != EventStatusConfirmed {
			continue
		}
		if !eventCoversMetric(ev, metric) {
			continue
		}
		start := startDay(ev.StartDate)
		if ev.EndDate != nil && startDay(*ev.EndDate).Before(end) {
			continue
		}
		if start.After(end) && ev.EndDate == nil {
			// 事件尚未开始且无结束日：不参与当前批次影响。
			continue
		}
		pct := 0.0
		if ev.ImpactPct != nil {
			pct = *ev.ImpactPct
		}
		if ev.ImpactDirection == EventDirectionDown {
			pct = -pct
		}
		weight := 1.0
		if ev.Confidence != nil && *ev.Confidence > 0 {
			weight = *ev.Confidence
		}
		weightSum += weight
		pctSum += pct * weight
		used++
	}
	if used == 0 {
		return EventImpact{}
	}
	impact := 0.0
	if weightSum > 0 {
		impact = pctSum / weightSum
	}
	return EventImpact{Pct: round4(impact), Version: EventAdjustmentVersion(used), Events: used}
}

func eventCoversMetric(ev persistence.ForecastEvent, metric persistence.CapacityMetric) bool {
	switch ev.ScopeType {
	case EventScopeGlobal:
		return true
	case EventScopeSystem:
		return ev.ScopeValue == metric.SystemCode
	case EventScopeMetric:
		for _, code := range splitScopeValue(ev.ScopeValue) {
			if code == metric.MetricCode {
				return true
			}
		}
	}
	return false
}

func splitScopeValue(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ApplyEventImpact 把合成事件影响作用到统计预测结果上（计划 §6.4）。
// 影响按「语义缩放」施加：水位/使用率类指标做相对放大，绝对值指标按量级叠加。
// 结果只改写曲线与 ETA，不影响模型版本与输入摘要。
func ApplyEventImpact(result ForecastResult, impact EventImpact, magnitude float64) ForecastResult {
	if impact.Pct == 0 || len(result.Curve) == 0 {
		if impact.Version != "" {
			result.EventFactorVersion = impact.Version
		}
		return result
	}
	ratio := impact.Pct / 100
	if magnitude <= 0 {
		magnitude = 100
	}
	adjusted := result
	curve := make([]CurvePoint, len(result.Curve))
	for i, p := range result.Curve {
		value := p.Value
		shift := value * ratio
		if math.Abs(value) < 1 {
			shift = magnitude * ratio
		}
		curve[i] = CurvePoint{
			BizDate: p.BizDate,
			Value:   math.Max(0, round4(value+shift)),
			Lower:   math.Max(0, round4(p.Lower+shift)),
			Upper:   math.Max(0, round4(p.Upper+shift)),
		}
	}
	adjusted.Curve = curve
	adjusted.ETAWarnDate = firstCurveCrossing(curve, result.ETAWarnDate, curveValueOn(result.Curve, result.ETAWarnDate), result.Slope >= 0)
	adjusted.ETADangerDate = firstCurveCrossing(curve, result.ETADangerDate, curveValueOn(result.Curve, result.ETADangerDate), result.Slope >= 0)
	adjusted.Explanation = appendEventNote(result.Explanation, impact)
	adjusted.EventFactorVersion = impact.Version
	return adjusted
}

func appendEventNote(explanation string, impact EventImpact) string {
	if impact.Version == "" {
		return explanation
	}
	note := fmt.Sprintf("事件因子 %s 调整 %.2f%%（%d 个已确认事件）", impact.Version, impact.Pct, impact.Events)
	if strings.TrimSpace(explanation) == "" {
		return note
	}
	return explanation + "；" + note
}

// expireDueEvents 把已过结束日仍处于 draft/confirmed 的事件置为 expired。
// 由预测批次启动时调用，保证过期事件不再进入模型。
func expireDueEvents(ctx context.Context, db *gorm.DB, now time.Time) error {
	if db == nil {
		return nil
	}
	return db.WithContext(ctx).Model(&persistence.ForecastEvent{}).
		Where("status IN ?", []string{EventStatusDraft, EventStatusConfirmed}).
		Where("end_date IS NOT NULL AND end_date < ?", startDay(now)).
		Updates(map[string]any{
			"status":     EventStatusExpired,
			"expires_at": now,
			"updated_at": now,
		}).Error
}
