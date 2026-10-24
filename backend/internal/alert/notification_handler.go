package alert

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

type notificationChannelRequest struct {
	Name    string         `json:"name" binding:"required,min=2,max=128"`
	Type    string         `json:"type" binding:"required,oneof=smtp wecom dingtalk feishu webhook"`
	Config  map[string]any `json:"config"`
	Enabled *bool          `json:"enabled"`
}

type notificationChannelView struct {
	persistence.NotificationChannel
	Config map[string]any `json:"config"`
}

func (h *Handler) ListNotificationChannels(c *gin.Context) {
	var rows []persistence.NotificationChannel
	if err := h.db.Order("id").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	out := make([]notificationChannelView, 0, len(rows))
	for i := range rows {
		cfg, _ := h.decodeChannelConfig(rows[i].Config)
		out = append(out, notificationChannelView{NotificationChannel: rows[i], Config: maskNotificationConfig(cfg)})
	}
	resp.OK(c, gin.H{"items": out, "total": len(out)})
}

func (h *Handler) CreateNotificationChannel(c *gin.Context) {
	var req notificationChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if err := validateNotificationChannel(req.Type, req.Config); err != nil {
		resp.Fail(c, err)
		return
	}
	raw, err := h.encryptChannelConfig(req.Config)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	username, _ := c.Get(middleware.CtxUsername)
	row := persistence.NotificationChannel{
		Name: strings.TrimSpace(req.Name), Type: req.Type, Config: raw,
		Enabled: enabled, HealthStatus: "unknown", CreatedBy: asStringValue(username),
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "渠道名称已存在或创建失败: %v", err))
		return
	}
	middleware.Log(c, "alert", "notification.channel.create", row.Name, "type="+row.Type)
	resp.OK(c, notificationChannelView{NotificationChannel: row, Config: maskNotificationConfig(req.Config)})
}

func (h *Handler) UpdateNotificationChannel(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "渠道 ID 非法"))
		return
	}
	var row persistence.NotificationChannel
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "通知渠道不存在"))
		return
	}
	var req notificationChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if req.Type != "" {
		updates["type"] = req.Type
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Config != nil {
		old, _ := h.decodeChannelConfig(row.Config)
		mergeSecretPlaceholders(req.Config, old)
		if err := validateNotificationChannel(firstNonEmpty(req.Type, row.Type), req.Config); err != nil {
			resp.Fail(c, err)
			return
		}
		raw, err := h.encryptChannelConfig(req.Config)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		updates["config"] = raw
	}
	if err := h.db.Model(&row).Updates(updates).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "渠道更新失败: %v", err))
		return
	}
	middleware.Log(c, "alert", "notification.channel.update", row.Name, "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteNotificationChannel(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "渠道 ID 非法"))
		return
	}
	var n int64
	_ = h.db.Model(&persistence.NotificationSubscription{}).Where("channel_ids LIKE ?", "%"+c.Param("id")+"%").Count(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "渠道仍被订阅引用，请先解除订阅"))
		return
	}
	if err := h.db.Delete(&persistence.NotificationChannel{}, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "notification.channel.delete", c.Param("id"), "")
	resp.OK(c, nil)
}

func (h *Handler) TestNotificationChannel(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "渠道 ID 非法"))
		return
	}
	var row persistence.NotificationChannel
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "通知渠道不存在"))
		return
	}
	cfg, err := h.decodeChannelConfig(row.Config)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	req := Notification{
		Event: "test", ChannelType: row.Type, Target: notificationTarget(row.Type, splitRecipients(fmt.Sprint(cfg["testRecipients"])), cfg),
		Secret: configString(cfg, "secret"), IdempotencyKey: "channel-test:" + fmt.Sprint(row.ID) + ":" + time.Now().Format("20060102150405"),
		Subject: "容量管理平台通知渠道测试", Body: "渠道配置有效。", Level: "p4", Config: cfg,
	}
	notifier := NewNotificationManager(h.db, h.cipher, h.params, h.log).notifier(row.Type)
	if notifier == nil {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "不支持的渠道类型: %s", row.Type))
		return
	}
	err = notifier.Send(c.Request.Context(), req)
	now := time.Now()
	updates := map[string]any{"last_test_at": now, "last_error": ""}
	if err != nil {
		updates["health_status"] = "error"
		updates["last_error"] = err.Error()
	} else {
		updates["health_status"] = "ok"
	}
	_ = h.db.Model(&row).Updates(updates).Error
	middleware.Log(c, "alert", "notification.channel.test", row.Name, fmt.Sprint(err))
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "渠道测试失败: %v", err))
		return
	}
	resp.OK(c, gin.H{"ok": true, "testedAt": now})
}

func (h *Handler) ListNotificationTemplates(c *gin.Context) {
	q := h.db.Model(&persistence.NotificationTemplate{})
	if v := c.Query("event"); v != "" {
		q = q.Where("event = ?", v)
	}
	if v := c.Query("channelType"); v != "" {
		q = q.Where("channel_type = ?", v)
	}
	var rows []persistence.NotificationTemplate
	if err := q.Order("event, channel_type, id").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "total": len(rows)})
}

type notificationTemplateRequest struct {
	Name        string   `json:"name" binding:"required,min=2,max=128"`
	Event       string   `json:"event" binding:"required,oneof=alert recovered escalate report"`
	ChannelType string   `json:"channelType" binding:"required"`
	Format      string   `json:"format" binding:"required,oneof=text markdown card"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body" binding:"required,min=1"`
	Variables   []string `json:"variables"`
	Enabled     *bool    `json:"enabled"`
}

func (h *Handler) CreateNotificationTemplate(c *gin.Context) {
	var req notificationTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if _, err := renderNotificationTemplate(req.Body, map[string]string{}); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "模板语法错误: %v", err))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	vars, _ := json.Marshal(req.Variables)
	username, _ := c.Get(middleware.CtxUsername)
	row := persistence.NotificationTemplate{
		Name: req.Name, Event: req.Event, ChannelType: req.ChannelType, Format: req.Format,
		Subject: req.Subject, Body: req.Body, Variables: string(vars), Version: 1,
		Enabled: enabled, CreatedBy: asStringValue(username),
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "模板名称或事件/渠道组合已存在: %v", err))
		return
	}
	middleware.Log(c, "alert", "notification.template.create", row.Name, row.Event+"/"+row.ChannelType)
	resp.OK(c, row)
}

func (h *Handler) UpdateNotificationTemplate(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "模板 ID 非法"))
		return
	}
	var row persistence.NotificationTemplate
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "通知模板不存在"))
		return
	}
	var req notificationTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if _, err := renderNotificationTemplate(req.Body, map[string]string{}); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "模板语法错误: %v", err))
		return
	}
	vars, _ := json.Marshal(req.Variables)
	enabled := row.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"name": req.Name, "event": req.Event, "channel_type": req.ChannelType,
		"format": req.Format, "subject": req.Subject, "body": req.Body,
		"variables": string(vars), "enabled": enabled, "version": row.Version + 1,
	}).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "模板更新失败: %v", err))
		return
	}
	middleware.Log(c, "alert", "notification.template.update", row.Name, "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteNotificationTemplate(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "模板 ID 非法"))
		return
	}
	if err := h.db.Delete(&persistence.NotificationTemplate{}, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "notification.template.delete", c.Param("id"), "")
	resp.OK(c, nil)
}

type notificationSubscriptionRequest struct {
	Name           string   `json:"name" binding:"required,min=2,max=128"`
	ScopeType      string   `json:"scopeType" binding:"required,oneof=global system metric"`
	ScopeValue     string   `json:"scopeValue"`
	Levels         []string `json:"levels" binding:"required"`
	AlertTypes     []string `json:"alertTypes"`
	Recipients     []string `json:"recipients"`
	ChannelIDs     []int64  `json:"channelIds" binding:"required"`
	SilentStart    string   `json:"silentStart"`
	SilentEnd      string   `json:"silentEnd"`
	DedupWindowMin int      `json:"dedupWindowMin"`
	Enabled        *bool    `json:"enabled"`
}

func (h *Handler) ListNotificationSubscriptions(c *gin.Context) {
	var rows []persistence.NotificationSubscription
	if err := h.db.Order("id").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "total": len(rows)})
}

func (h *Handler) CreateNotificationSubscription(c *gin.Context) {
	var req notificationSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	row, err := h.subscriptionFromRequest(req, asStringValue(username))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.db.Create(row).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "订阅名称已存在或创建失败: %v", err))
		return
	}
	middleware.Log(c, "alert", "notification.subscription.create", row.Name, row.ScopeType+":"+row.ScopeValue)
	resp.OK(c, row)
}

func (h *Handler) UpdateNotificationSubscription(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "订阅 ID 非法"))
		return
	}
	var old persistence.NotificationSubscription
	if err := h.db.First(&old, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "通知订阅不存在"))
		return
	}
	var req notificationSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	row, err := h.subscriptionFromRequest(req, asStringValue(username))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	row.ID = old.ID
	row.CreatedBy = old.CreatedBy
	row.CreatedAt = old.CreatedAt
	if err := h.db.Model(&old).Updates(map[string]any{
		"name": row.Name, "scope_type": row.ScopeType, "scope_value": row.ScopeValue,
		"levels": row.Levels, "alert_types": row.AlertTypes, "recipients": row.Recipients,
		"channel_ids": row.ChannelIDs, "silent_start": row.SilentStart, "silent_end": row.SilentEnd,
		"dedup_window_min": row.DedupWindowMin, "enabled": row.Enabled,
	}).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "订阅更新失败: %v", err))
		return
	}
	middleware.Log(c, "alert", "notification.subscription.update", row.Name, "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteNotificationSubscription(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "订阅 ID 非法"))
		return
	}
	if err := h.db.Delete(&persistence.NotificationSubscription{}, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "notification.subscription.delete", c.Param("id"), "")
	resp.OK(c, nil)
}

func (h *Handler) subscriptionFromRequest(req notificationSubscriptionRequest, username string) (*persistence.NotificationSubscription, error) {
	if !validLevelCSV(req.Levels) {
		return nil, resp.NewErr(resp.CodeUnprocessable, "级别仅支持 p1/p2/p3/p4")
	}
	if req.DedupWindowMin < 0 {
		return nil, resp.NewErr(resp.CodeUnprocessable, "去重窗口不能为负数")
	}
	if err := validateSilentClock(req.SilentStart, req.SilentEnd); err != nil {
		return nil, err
	}
	if err := h.prepareNotificationScope(&req.ScopeType, &req.ScopeValue); err != nil {
		return nil, err
	}
	for _, id := range req.ChannelIDs {
		var n int64
		_ = h.db.Model(&persistence.NotificationChannel{}).Where("id = ? AND enabled = ?", id, true).Count(&n).Error
		if n == 0 {
			return nil, resp.NewErr(resp.CodeUnprocessable, "通知渠道不存在或已停用: %d", id)
		}
	}
	alertTypes := req.AlertTypes
	if len(alertTypes) == 0 {
		alertTypes = []string{"*"}
	}
	channelIDs, _ := json.Marshal(req.ChannelIDs)
	recipients, _ := json.Marshal(req.Recipients)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	window := req.DedupWindowMin
	if window == 0 {
		window = 60
	}
	return &persistence.NotificationSubscription{
		Name: req.Name, ScopeType: req.ScopeType, ScopeValue: req.ScopeValue,
		Levels: strings.Join(req.Levels, ","), AlertTypes: strings.Join(alertTypes, ","),
		Recipients: string(recipients), ChannelIDs: string(channelIDs),
		SilentStart: req.SilentStart, SilentEnd: req.SilentEnd,
		DedupWindowMin: window, Enabled: enabled, CreatedBy: username,
	}, nil
}

func (h *Handler) prepareNotificationScope(scopeType, scopeValue *string) error {
	if *scopeType == "global" {
		*scopeValue = ""
		return nil
	}
	if strings.TrimSpace(*scopeValue) == "" {
		return resp.NewErr(resp.CodeUnprocessable, "非全局订阅必须填写作用域")
	}
	if *scopeType == "metric" && !strings.Contains(*scopeValue, ".") {
		var metric persistence.CapacityMetric
		if err := h.db.Where("metric_code = ?", *scopeValue).First(&metric).Error; err != nil {
			return resp.NewErr(resp.CodeNotFound, "指标不存在: %s", *scopeValue)
		}
		*scopeValue = metric.SystemCode + "." + metric.MetricCode
	}
	return nil
}

func validLevelCSV(levels []string) bool {
	if len(levels) == 0 {
		return false
	}
	for _, l := range levels {
		switch l {
		case "p1", "p2", "p3", "p4":
		default:
			return false
		}
	}
	return true
}

func (h *Handler) ListNotificationSilences(c *gin.Context) {
	var rows []persistence.NotificationSilence
	_ = h.db.Order("start_at DESC").Limit(200).Find(&rows).Error
	resp.OK(c, gin.H{"items": rows, "total": len(rows)})
}

func (h *Handler) CreateNotificationSilence(c *gin.Context) {
	var req struct {
		ScopeType  string `json:"scopeType" binding:"required,oneof=global system metric"`
		ScopeValue string `json:"scopeValue"`
		StartAt    string `json:"startAt" binding:"required"`
		EndAt      string `json:"endAt" binding:"required"`
		Reason     string `json:"reason" binding:"required,min=2"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	start, err1 := time.ParseInLocation(time.RFC3339, req.StartAt, time.Local)
	end, err2 := time.ParseInLocation(time.RFC3339, req.EndAt, time.Local)
	if err1 != nil || err2 != nil || !end.After(start) {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "静默时间范围非法"))
		return
	}
	if err := h.prepareNotificationScope(&req.ScopeType, &req.ScopeValue); err != nil {
		resp.Fail(c, err)
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	row := persistence.NotificationSilence{
		ScopeType: req.ScopeType, ScopeValue: req.ScopeValue,
		StartAt: start, EndAt: end, Reason: req.Reason, CreatedBy: asStringValue(username),
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "notification.silence.create", row.ScopeValue, row.Reason)
	resp.OK(c, row)
}

func (h *Handler) DeleteNotificationSilence(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "静默 ID 非法"))
		return
	}
	if err := h.db.Delete(&persistence.NotificationSilence{}, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, nil)
}

func (h *Handler) ListNotificationLogs(c *gin.Context) {
	q := h.db.Model(&persistence.AlertNotificationLog{})
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("channelType"); v != "" {
		q = q.Where("channel_type = ?", v)
	}
	if v := c.Query("alertNo"); v != "" {
		q = q.Where("alert_no LIKE ?", "%"+v+"%")
	}
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	var total int64
	_ = q.Count(&total).Error
	var rows []persistence.AlertNotificationLog
	_ = q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	resp.OK(c, resp.Page(rows, total, page, size))
}

func (h *Handler) RetryNotificationLog(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日志 ID 非法"))
		return
	}
	var row persistence.AlertNotificationLog
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "通知日志不存在"))
		return
	}
	if row.Status != "failed" && row.Status != "retrying" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅失败/重试中日志可手工重试"))
		return
	}
	_ = h.db.Model(&row).Updates(map[string]any{"status": "retrying", "next_retry_at": time.Now()}).Error
	mgr := NewNotificationManager(h.db, h.cipher, h.params, h.log)
	if err := mgr.ProcessRetry(c.Request.Context(), row.ID); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "notification.log.retry", c.Param("id"), "")
	resp.OK(c, nil)
}

func validateNotificationChannel(kind string, cfg map[string]any) error {
	if len(cfg) == 0 {
		return resp.NewErr(resp.CodeUnprocessable, "渠道配置不能为空")
	}
	switch kind {
	case "smtp":
		if configString(cfg, "host") == "" || configString(cfg, "from") == "" {
			return resp.NewErr(resp.CodeUnprocessable, "SMTP 渠道必须配置 host 和 from")
		}
	case "wecom", "dingtalk", "feishu", "webhook":
		raw := configString(cfg, "url")
		if err := validateWebhookTarget(raw); err != nil {
			return resp.NewErr(resp.CodeUnprocessable, "Webhook URL 非法")
		}
	default:
		return resp.NewErr(resp.CodeBadRequest, "不支持的渠道类型")
	}
	return nil
}

func validateSilentClock(start, end string) error {
	if start == "" && end == "" {
		return nil
	}
	if start == "" || end == "" {
		return resp.NewErr(resp.CodeUnprocessable, "静默时段开始/结束必须同时填写")
	}
	if _, err := time.Parse("15:04", start); err != nil {
		return resp.NewErr(resp.CodeUnprocessable, "静默开始时间须为 HH:MM")
	}
	if _, err := time.Parse("15:04", end); err != nil {
		return resp.NewErr(resp.CodeUnprocessable, "静默结束时间须为 HH:MM")
	}
	return nil
}

func (h *Handler) encryptChannelConfig(cfg map[string]any) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	if h.cipher == nil {
		return string(raw), nil
	}
	return h.cipher.Encrypt(string(raw))
}

func (h *Handler) decodeChannelConfig(raw string) (map[string]any, error) {
	if h.cipher != nil && strings.HasPrefix(raw, "enc.v1.") {
		plain, err := h.cipher.Decrypt(raw)
		if err != nil {
			return nil, err
		}
		raw = plain
	}
	out := map[string]any{}
	if raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func maskNotificationConfig(cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		switch strings.ToLower(k) {
		case "password", "secret", "token", "authorization":
			if fmt.Sprint(v) != "" {
				out[k] = "******"
			}
		default:
			out[k] = v
		}
	}
	return out
}

func mergeSecretPlaceholders(in, old map[string]any) {
	for k, v := range in {
		if fmt.Sprint(v) == "******" {
			if oldV, ok := old[k]; ok {
				in[k] = oldV
			}
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
