package forecast

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/masking"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

// ListEvents 事件因子列表（FR-AI-06）。支持 status/scopeType/eventType 过滤与
// 当前用户数据权限过滤；确认前的事件默认也能看到，便于草稿继续编辑。
func (h *Handler) ListEvents(c *gin.Context) {
	q := h.db.Model(&persistence.ForecastEvent{})
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := strings.TrimSpace(c.Query("scopeType")); v != "" {
		q = q.Where("scope_type = ?", v)
	}
	if v := strings.TrimSpace(c.Query("eventType")); v != "" {
		q = q.Where("event_type = ?", v)
	}
	var rows []persistence.ForecastEvent
	if err := q.Order("status, start_date DESC, id DESC").Limit(500).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	visible := h.visible(c)
	items := make([]persistence.ForecastEvent, 0, len(rows))
	for _, ev := range rows {
		if !h.canViewEvent(visible, ev) {
			continue
		}
		items = append(items, ev)
	}
	resp.OK(c, gin.H{"items": items, "count": len(items)})
}

// GetEvent 单条事件详情，含历史版本。
func (h *Handler) GetEvent(c *gin.Context) {
	id, ok := parseEventID(c)
	if !ok {
		return
	}
	var ev persistence.ForecastEvent
	if err := h.db.First(&ev, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "事件不存在"))
		return
	}
	if !h.canViewEvent(h.visible(c), ev) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权查看该事件"))
		return
	}
	var versions []persistence.ForecastEvent
	root := ev.ID
	if ev.RootID != nil {
		root = *ev.RootID
	}
	_ = h.db.Where("id = ? OR root_id = ?", root, root).Order("version").Find(&versions).Error
	resp.OK(c, gin.H{"event": ev, "versions": versions})
}

// CreateEvent 录入事件因子，默认 draft；只有人工确认后的 confirmed 才进入模型。
func (h *Handler) CreateEvent(c *gin.Context) {
	var in EventInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if err := in.Validate(); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", err.Error()))
		return
	}
	if !h.canWriteEvent(c, in) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权在该作用域录入事件"))
		return
	}
	user := currentUser(c)
	ev := eventFromInput(in)
	ev.Status = EventStatusDraft
	ev.CreatedBy = user
	ev.Version = 1
	if err := h.db.Create(&ev).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	root := ev.ID
	if err := h.db.Model(&ev).Updates(map[string]any{"root_id": root}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	ev.RootID = &root
	middleware.Log(c, "forecast", "event_create", ev.Name, fmt.Sprintf("scope=%s/%s", ev.ScopeType, ev.ScopeValue))
	resp.OK(c, ev)
}

// UpdateEvent 修改事件并生成新版本：旧版本保留为历史，新版本继承 root 并 +1。
func (h *Handler) UpdateEvent(c *gin.Context) {
	id, ok := parseEventID(c)
	if !ok {
		return
	}
	var prev persistence.ForecastEvent
	if err := h.db.First(&prev, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "事件不存在"))
		return
	}
	if !h.canViewEvent(h.visible(c), prev) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权修改该事件"))
		return
	}
	var in EventInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if err := in.Validate(); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", err.Error()))
		return
	}
	if !h.canWriteEvent(c, in) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权在该作用域修改事件"))
		return
	}
	next := eventFromInput(in)
	next.Status = prev.Status
	if next.Status == EventStatusExpired {
		next.Status = EventStatusDraft
	}
	next.CreatedBy = prev.CreatedBy
	next.ConfirmedBy = prev.ConfirmedBy
	next.ConfirmedAt = prev.ConfirmedAt
	next.Version = prev.Version + 1
	root := prev.ID
	if prev.RootID != nil {
		root = *prev.RootID
	}
	next.RootID = &root
	if err := h.db.Create(&next).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.db.Model(&persistence.ForecastEvent{}).Where("id = ?", prev.ID).
		Updates(map[string]any{"superseded_by": next.ID, "updated_at": time.Now()}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "forecast", "event_update", next.Name, fmt.Sprintf("version=%d", next.Version))
	resp.OK(c, next)
}

// ConfirmEvent 人工确认事件；确认后事件进入预测模型并写入版本摘要。
func (h *Handler) ConfirmEvent(c *gin.Context) {
	id, ok := parseEventID(c)
	if !ok {
		return
	}
	var ev persistence.ForecastEvent
	if err := h.db.First(&ev, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "事件不存在"))
		return
	}
	if !h.canViewEvent(h.visible(c), ev) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权确认该事件"))
		return
	}
	if ev.Status == EventStatusExpired {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "已过期事件不能确认"))
		return
	}
	now := time.Now()
	user := currentUser(c)
	if err := h.db.Model(&ev).Updates(map[string]any{
		"status": EventStatusConfirmed, "confirmed_by": user, "confirmed_at": now, "updated_at": now,
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	ev.Status = EventStatusConfirmed
	ev.ConfirmedBy = user
	ev.ConfirmedAt = &now
	middleware.Log(c, "forecast", "event_confirm", ev.Name, fmt.Sprintf("id=%d", ev.ID))
	resp.OK(c, ev)
}

// ExpireEvent 手工将事件置为过期；到期事件也会被预测批次自动置过期。
func (h *Handler) ExpireEvent(c *gin.Context) {
	id, ok := parseEventID(c)
	if !ok {
		return
	}
	var ev persistence.ForecastEvent
	if err := h.db.First(&ev, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "事件不存在"))
		return
	}
	if !h.canViewEvent(h.visible(c), ev) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权处理该事件"))
		return
	}
	now := time.Now()
	if err := h.db.Model(&ev).Updates(map[string]any{
		"status": EventStatusExpired, "expires_at": now, "updated_at": now,
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	ev.Status = EventStatusExpired
	middleware.Log(c, "forecast", "event_expire", ev.Name, fmt.Sprintf("id=%d", ev.ID))
	resp.OK(c, ev)
}

// SuggestEvent 让 AI 依据事件描述给出影响建议，只写入 draft 的 ai_suggestion，
// 不直接改变影响参数；人工确认后才进入模型。
func (h *Handler) SuggestEvent(c *gin.Context) {
	id, ok := parseEventID(c)
	if !ok {
		return
	}
	var ev persistence.ForecastEvent
	if err := h.db.First(&ev, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "事件不存在"))
		return
	}
	if !h.canViewEvent(h.visible(c), ev) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无权获取该事件的 AI 建议"))
		return
	}
	if !h.svc.AIEnabled() {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "AI 服务未启用，事件可人工录入影响参数"))
		return
	}
	summary, err := eventSuggestionPayload(ev)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	content, model, err := h.svc.ai.Chat(c.Request.Context(), eventAISystemPrompt, summary)
	if err != nil {
		h.auditAIFailure(c, model, err)
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "AI 服务暂不可用，请人工填写影响参数"))
		return
	}
	suggestion, err := ParseEventSuggestion(content)
	if err != nil {
		h.auditAIFailure(c, model, err)
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "AI 建议格式非法，已忽略"))
		return
	}
	raw, _ := json.Marshal(suggestion)
	now := time.Now()
	if err := h.db.Model(&ev).Updates(map[string]any{
		"ai_suggestion": string(raw), "ai_model": model, "ai_generated_at": now, "updated_at": now,
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	h.auditAISuccess(c, model, string(raw))
	middleware.Log(c, "forecast", "event_ai_suggest", ev.Name, fmt.Sprintf("model=%s", model))
	resp.OK(c, gin.H{"suggestion": suggestion, "model": model, "event": ev})
}

const eventAISystemPrompt = `你是容量管理平台的事件影响评估助手。输入是脱敏后的事件元数据，不含原始业务明细。
请评估该事件对容量水位的影响，只输出严格 JSON：
{"impactDirection":"increase|decrease","impactPct":<-100~100>,"confidence":<0~1>,"rationale":"<不少于4字的中文解释>"}
不要输出 JSON 之外的任何内容。`

func eventSuggestionPayload(ev persistence.ForecastEvent) (string, error) {
	payload := map[string]any{
		"name": ev.Name, "eventType": ev.EventType, "scopeType": ev.ScopeType,
		"scopeValue": ev.ScopeValue, "startDate": startDay(ev.StartDate).Format("2006-01-02"),
		"description": masking.SanitizeForAI(ev.Description),
	}
	if ev.EndDate != nil {
		payload["endDate"] = startDay(*ev.EndDate).Format("2006-01-02")
	}
	raw, err := json.Marshal(payload)
	return string(raw), err
}

func (h *Handler) auditAISuccess(c *gin.Context, model, out string) {
	h.writeAICallLog(c, "forecast_adjust", model, "success", "", out)
}

func (h *Handler) auditAIFailure(c *gin.Context, model string, err error) {
	h.writeAICallLog(c, "forecast_adjust", model, "degraded", err.Error(), "")
}

func (h *Handler) writeAICallLog(c *gin.Context, feature, model, status, errMsg, out string) {
	user, _ := c.Get(middleware.CtxUsername)
	un, _ := user.(string)
	rec := persistence.AICallLog{
		Feature: feature, UserID: un, Model: model, Status: status,
		ErrMsg: masking.Digest(errMsg, 1000),
	}
	if out != "" {
		rec.OutputDigest = masking.Digest(out, 1000)
	}
	if err := h.db.Create(&rec).Error; err != nil {
		h.log.Warn("ai call log write failed", "feature", feature, "err", err.Error())
	}
}

func eventFromInput(in EventInput) persistence.ForecastEvent {
	start := startDay(in.StartDate)
	ev := persistence.ForecastEvent{
		Name: in.Name, EventType: in.EventType, ScopeType: in.ScopeType,
		ScopeValue: strings.TrimSpace(in.ScopeValue), StartDate: start,
		ImpactDirection: in.ImpactDirection, ImpactPct: in.ImpactPct,
		Confidence: in.Confidence, Description: in.Description,
		// ai_suggestion 为 jsonb，零值空串不是合法 JSON；草稿先落空对象，
		// AI 建议回来后由 SuggestEvent 覆盖。
		AISuggestion: "{}",
	}
	if in.EndDate != nil {
		end := startDay(*in.EndDate)
		ev.EndDate = &end
	}
	return ev
}

// canViewEvent 判断当前用户数据权限是否覆盖事件作用域。全局事件对所有登录
// 用户可见（只读），系统/指标事件要求作用域在授权范围内。
func (h *Handler) canViewEvent(visible []string, ev persistence.ForecastEvent) bool {
	switch ev.ScopeType {
	case EventScopeGlobal:
		return true
	case EventScopeSystem:
		return containsString(visible, ev.ScopeValue)
	case EventScopeMetric:
		for _, code := range splitScopeValue(ev.ScopeValue) {
			var metric persistence.CapacityMetric
			if err := h.db.Where("metric_code = ?", code).First(&metric).Error; err != nil {
				continue
			}
			if containsString(visible, metric.SystemCode) {
				return true
			}
		}
	}
	return false
}

// canWriteEvent 要求写入作用域的所有目标都在授权范围内，避免越权扩散事件。
func (h *Handler) canWriteEvent(c *gin.Context, in EventInput) bool {
	visible := h.visible(c)
	switch in.ScopeType {
	case EventScopeGlobal:
		return true
	case EventScopeSystem:
		return containsString(visible, strings.TrimSpace(in.ScopeValue))
	case EventScopeMetric:
		codes := splitScopeValue(in.ScopeValue)
		if len(codes) == 0 {
			return false
		}
		for _, code := range codes {
			var metric persistence.CapacityMetric
			if err := h.db.Where("metric_code = ?", code).First(&metric).Error; err != nil {
				return false
			}
			if !containsString(visible, metric.SystemCode) {
				return false
			}
		}
		return true
	}
	return false
}

func containsString(list []string, target string) bool {
	if list == nil {
		return true // nil 表示不限数据权限
	}
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

func parseEventID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "事件 ID 非法"))
		return 0, false
	}
	return id, true
}

func currentUser(c *gin.Context) string {
	user, _ := c.Get(middleware.CtxUsername)
	un, _ := user.(string)
	if un == "" {
		return "system"
	}
	return un
}
