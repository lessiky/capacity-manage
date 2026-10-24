package forecast

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
)

func metric(code, system string, warn, danger float64) persistence.CapacityMetric {
	return persistence.CapacityMetric{
		MetricCode: code, SystemCode: system, JudgeDirection: "higher_worse",
		WarnLine: &warn, DangerLine: &danger,
	}
}

func ptrFloat(v float64) *float64 { return &v }

func event(id int64, scope, value, direction string, pct, conf float64, start time.Time, end *time.Time) persistence.ForecastEvent {
	return persistence.ForecastEvent{
		ID: id, Name: "事件" + value, EventType: EventTypePeak,
		ScopeType: scope, ScopeValue: value, StartDate: start, EndDate: end,
		ImpactDirection: direction, ImpactPct: ptrFloat(pct), Confidence: ptrFloat(conf),
		Status: EventStatusConfirmed, Version: 1,
	}
}

func TestEventInputValidatesScopeAndImpact(t *testing.T) {
	valid := EventInput{
		Name: "双十一高峰", EventType: EventTypePeak, ScopeType: EventScopeSystem,
		ScopeValue: "A5", StartDate: day(2026, 11, 1), ImpactDirection: EventDirectionUp,
		ImpactPct: ptrFloat(30), Confidence: ptrFloat(0.8),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法事件不应报错: %v", err)
	}
	cases := map[string]func(*EventInput){
		"名称空":      func(in *EventInput) { in.Name = " " },
		"类型非法":     func(in *EventInput) { in.EventType = "unknown" },
		"作用域非法":    func(in *EventInput) { in.ScopeType = "cluster" },
		"作用域缺失":    func(in *EventInput) { in.ScopeValue = "" },
		"全局带取值":    func(in *EventInput) { in.ScopeType = EventScopeGlobal; in.ScopeValue = "A5" },
		"方向非法":     func(in *EventInput) { in.ImpactDirection = "sideways" },
		"影响越界":     func(in *EventInput) { in.ImpactPct = ptrFloat(150) },
		"置信度越界":    func(in *EventInput) { in.Confidence = ptrFloat(1.5) },
		"结束日早于开始日": func(in *EventInput) { end := day(2026, 10, 1); in.EndDate = &end },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := valid
			mutate(&in)
			if err := in.Validate(); err == nil {
				t.Fatalf("%s 应校验失败", name)
			}
		})
	}
}

// TestEventInputUnmarshalAcceptsDayFormat 锁定前端 el-date-picker 的
// "YYYY-MM-DD" 口径：早期直接按 time.Time 反序列化会返回 40001 参数错误。
func TestEventInputUnmarshalAcceptsDayFormat(t *testing.T) {
	raw := `{"name":"双十一大促","eventType":"peak","scopeType":"metric","scopeValue":"M7_FC",` +
		`"startDate":"2026-10-25","endDate":"2026-11-15","impactDirection":"increase",` +
		`"impactPct":28,"confidence":0.8,"description":"大促"}`
	var in EventInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("YYYY-MM-DD 应可解析: %v", err)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("解析后应校验通过: %v", err)
	}
	if got := in.StartDate.Format("2006-01-02"); got != "2026-10-25" {
		t.Fatalf("开始日解析错误: %s", got)
	}
	if in.EndDate == nil || in.EndDate.Format("2006-01-02") != "2026-11-15" {
		t.Fatalf("结束日解析错误: %v", in.EndDate)
	}
	// RFC3339 与 null 结束日同样兼容。
	var rfc EventInput
	if err := json.Unmarshal([]byte(`{"name":"x","eventType":"peak","scopeType":"global",`+
		`"startDate":"2026-10-25T00:00:00+08:00","endDate":null,"impactDirection":"increase"}`), &rfc); err != nil {
		t.Fatalf("RFC3339 应可解析: %v", err)
	}
	if rfc.EndDate != nil {
		t.Fatalf("null 结束日应为长期事件")
	}
	// 非法日期应被拒绝。
	var bad EventInput
	if err := json.Unmarshal([]byte(`{"startDate":"10/25/2026"}`), &bad); err == nil {
		t.Fatalf("非法日期应报错")
	}
}

func TestParseEventSuggestionStrict(t *testing.T) {
	good := `{"impactDirection":"increase","impactPct":25,"confidence":0.7,"rationale":"业务高峰推高水位"}`
	s, err := ParseEventSuggestion(good)
	if err != nil {
		t.Fatalf("合法建议不应报错: %v", err)
	}
	if s.ImpactPct != 25 || s.ImpactDirection != EventDirectionUp {
		t.Fatalf("建议解析错误: %+v", s)
	}
	bad := map[string]string{
		"非法 JSON": `{`,
		"方向非法":    `{"impactDirection":"up","impactPct":25,"confidence":0.7,"rationale":"说明"}`,
		"影响越界":    `{"impactDirection":"increase","impactPct":250,"confidence":0.7,"rationale":"说明"}`,
		"置信度越界":   `{"impactDirection":"increase","impactPct":25,"confidence":2,"rationale":"说明"}`,
		"解释为空":    `{"impactDirection":"increase","impactPct":25,"confidence":0.7,"rationale":""}`,
		"缺字段":     `{"impactDirection":"increase"}`,
	}
	for name, raw := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEventSuggestion(raw); err == nil {
				t.Fatalf("%s 应被拒绝", name)
			}
		})
	}
}

func TestComputeEventImpactScopesAndWeights(t *testing.T) {
	m := metric("A5_CPU", "A5", 80, 90)
	end := day(2026, 11, 10)
	during := event(1, EventScopeSystem, "A5", EventDirectionUp, 40, 1, day(2026, 11, 1), nil)
	other := event(2, EventScopeSystem, "B5", EventDirectionUp, 90, 1, day(2026, 11, 1), nil)
	global := event(3, EventScopeGlobal, "", EventDirectionDown, 10, 0.5, day(2026, 11, 1), nil)
	// 该事件在数据截止日之前就已结束，不应计入。
	pastEnd := day(2026, 10, 20)
	past := event(4, EventScopeGlobal, "", EventDirectionUp, 100, 1, day(2026, 10, 1), &pastEnd)

	impact := ComputeEventImpact(m, []persistence.ForecastEvent{during, other, global, past}, end)
	if impact.Events != 2 {
		t.Fatalf("应只计入 2 个有效事件, 实际 %d", impact.Events)
	}
	// 加权: (40*1 + -10*0.5) / (1 + 0.5) = 35/1.5 = 23.3333
	if !almostEqual(impact.Pct, 23.3333) {
		t.Fatalf("加权影响计算错误: %v", impact.Pct)
	}
	if impact.Version == "" {
		t.Fatalf("应给出事件版本")
	}
}

func TestComputeEventImpactMetricScope(t *testing.T) {
	m := metric("A5_CPU", "A5", 80, 90)
	end := day(2026, 11, 10)
	multi := event(1, EventScopeMetric, "A5_CPU,A5_MEM", EventDirectionUp, 20, 1, day(2026, 11, 1), nil)
	miss := event(2, EventScopeMetric, "B5_CPU", EventDirectionUp, 50, 1, day(2026, 11, 1), nil)
	impact := ComputeEventImpact(m, []persistence.ForecastEvent{multi, miss}, end)
	if impact.Events != 1 || !almostEqual(impact.Pct, 20) {
		t.Fatalf("指标级作用域匹配错误: %+v", impact)
	}
}

// TestComputeEventImpactIncludesUpcomingEvent 未来窗口内的活动（如大促）应在
// 数据截止日之前就被纳入预测修正，否则预测无法提前反映计划内事件。
func TestComputeEventImpactIncludesUpcomingEvent(t *testing.T) {
	m := metric("M7_FC", "A5", 80, 90)
	end := day(2026, 10, 21)
	promoEnd := day(2026, 11, 15)
	promo := event(1, EventScopeMetric, "M7_FC", EventDirectionUp, 28, 0.8, day(2026, 10, 25), &promoEnd)
	impact := ComputeEventImpact(m, []persistence.ForecastEvent{promo}, end)
	if impact.Events != 1 || !almostEqual(impact.Pct, 28) {
		t.Fatalf("窗口内未来事件应计入: %+v", impact)
	}
}

func TestApplyEventImpactScalesCurveAndEta(t *testing.T) {
	base := baseAIResult()
	impact := EventImpact{Pct: 10, Version: "ev-abc123", Events: 1}
	out := ApplyEventImpact(base, impact, 100)
	if out.EventFactorVersion != "ev-abc123" {
		t.Fatalf("应记录事件版本: %s", out.EventFactorVersion)
	}
	if !strings.Contains(out.Explanation, "事件因子") {
		t.Fatalf("解释应包含事件因子说明: %q", out.Explanation)
	}
	if len(out.Curve) != len(base.Curve) {
		t.Fatalf("曲线长度不应变化")
	}
	if out.Curve[len(out.Curve)-1].Value <= base.Curve[len(base.Curve)-1].Value {
		t.Fatalf("正向事件应抬升曲线尾部: base=%v adjusted=%v",
			base.Curve[len(base.Curve)-1].Value, out.Curve[len(out.Curve)-1].Value)
	}
	if out.ETADangerDate == nil || !out.ETADangerDate.Before(*base.ETADangerDate) {
		t.Fatalf("正向事件应提前危险线 ETA: base=%v adjusted=%v", base.ETADangerDate, out.ETADangerDate)
	}
}

func TestApplyEventImpactHandlesDecrease(t *testing.T) {
	base := baseAIResult()
	out := ApplyEventImpact(base, EventImpact{Pct: -20, Version: "ev-down", Events: 1}, 100)
	if out.Curve[len(out.Curve)-1].Value >= base.Curve[len(base.Curve)-1].Value {
		t.Fatalf("负向事件应下调曲线")
	}
}

func TestApplyEventImpactNoopWithoutImpact(t *testing.T) {
	base := baseAIResult()
	out := ApplyEventImpact(base, EventImpact{}, 100)
	if out.ETADangerDate == nil || !out.ETADangerDate.Equal(*base.ETADangerDate) {
		t.Fatalf("无事件时不应改变 ETA")
	}
	if out.EventFactorVersion != "" {
		t.Fatalf("无事件时不应设置版本")
	}
}

func TestEventSetVersionChangesWithContent(t *testing.T) {
	a := event(1, EventScopeGlobal, "", EventDirectionUp, 10, 1, day(2026, 11, 1), nil)
	b := a
	if v1, v2 := eventSetVersion([]persistence.ForecastEvent{a}), eventSetVersion([]persistence.ForecastEvent{b}); v1 != v2 {
		t.Fatalf("相同事件集合应产生相同版本: %s vs %s", v1, v2)
	}
	c := a
	c.ImpactPct = ptrFloat(20)
	if eventSetVersion([]persistence.ForecastEvent{a}) == eventSetVersion([]persistence.ForecastEvent{c}) {
		t.Fatalf("事件内容变化应产生不同版本")
	}
	if eventSetVersion(nil) != "" {
		t.Fatalf("无事件时版本应为空")
	}
}

func TestEventSuggestionPayloadMasksCredentials(t *testing.T) {
	ev := event(1, EventScopeGlobal, "", EventDirectionUp, 10, 1, day(2026, 11, 1), nil)
	ev.Description = "password=Secret@9xKq7 connection=jdbc:mysql://db:3306/app"
	payload, err := eventSuggestionPayload(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "Secret@9xKq7") {
		t.Fatalf("AI 事件摘要泄露凭据: %s", payload)
	}
	if !strings.Contains(payload, "******") {
		t.Fatalf("AI 事件摘要应保留脱敏占位: %s", payload)
	}
}
