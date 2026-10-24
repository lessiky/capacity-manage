package alert

import (
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ListPolicies(c *gin.Context) {
	q := h.db.Model(&persistence.AlertEscalationPolicy{})
	if v := c.Query("enabled"); v != "" {
		q = q.Where("enabled = ?", v == "true" || v == "1")
	}
	if v := c.Query("scopeType"); v != "" {
		q = q.Where("scope_type = ?", v)
	}
	var rows []persistence.AlertEscalationPolicy
	if err := q.Order("priority DESC, id").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "total": len(rows)})
}

func (h *Handler) GetPolicy(c *gin.Context) {
	var row persistence.AlertEscalationPolicy
	if err := h.db.First(&row, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "升级策略不存在"))
		return
	}
	resp.OK(c, row)
}

func (h *Handler) CreatePolicy(c *gin.Context) {
	var in PolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if err := h.preparePolicyScope(c, &in); err != nil {
		resp.Fail(c, err)
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	row, err := createEscalationPolicy(h.db, in, asStringValue(username))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.policy.create", row.Name, policyAuditDetail(row))
	resp.OK(c, row)
}

func (h *Handler) UpdatePolicy(c *gin.Context) {
	var in PolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if err := h.preparePolicyScope(c, &in); err != nil {
		resp.Fail(c, err)
		return
	}
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "策略 ID 非法"))
		return
	}
	row, err := updateEscalationPolicy(h.db, id, in)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.policy.update", row.Name, policyAuditDetail(row))
	resp.OK(c, row)
}

func (h *Handler) DeletePolicy(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "策略 ID 非法"))
		return
	}
	if err := deleteEscalationPolicy(h.db, id); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.policy.delete", c.Param("id"), "")
	resp.OK(c, nil)
}

func (h *Handler) SimulatePolicy(c *gin.Context) {
	var req struct {
		MetricCode string      `json:"metricCode" binding:"required"`
		From       string      `json:"from" binding:"required"`
		To         string      `json:"to" binding:"required"`
		Policy     PolicyInput `json:"policy" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	from, to, err := parseDayRange(req.From, req.To)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", req.MetricCode).First(&metric).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !h.metricVisible(c, metric.SystemCode) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无指标数据权限"))
		return
	}
	req.Policy.ScopeValue = mergeScopeValues(req.Policy)
	req.Policy.ScopeValues = nil
	if err := validatePolicyScope(req.Policy.ScopeType, req.Policy.ScopeValue, h.scopeSystems(c), h.isPlatformAdmin(c)); err != nil {
		resp.Fail(c, err)
		return
	}
	result, err := New(h.db, h.params, nil).SimulatePolicy(c.Request.Context(), &metric, req.Policy, from, to)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.policy.simulate", req.MetricCode, req.From+".."+req.To)
	resp.OK(c, result)
}

func (h *Handler) PreviewPolicy(c *gin.Context) {
	id, ok := parseID(c.Param("id"))
	if !ok {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "策略 ID 非法"))
		return
	}
	var policy persistence.AlertEscalationPolicy
	if err := h.db.First(&policy, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "升级策略不存在"))
		return
	}
	metricCode := c.Query("metricCode")
	if metricCode == "" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "metricCode 必填"))
		return
	}
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", metricCode).First(&metric).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !h.metricVisible(c, metric.SystemCode) {
		resp.Fail(c, resp.NewErr(resp.CodeDataForbidden, "无指标数据权限"))
		return
	}
	days := 30
	if v := c.Query("days"); v != "" {
		if n, err := parsePositiveInt(v); err == nil && n > 0 && n <= 366 {
			days = n
		}
	}
	to := clock.DayStart(time.Now())
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = clock.DayStart(t)
		}
	}
	from := to.AddDate(0, 0, -(days - 1))
	result, err := New(h.db, h.params, nil).SimulatePolicy(c.Request.Context(), &metric, policyToInput(policy), from, to)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, result)
}

func (h *Handler) preparePolicyScope(c *gin.Context, in *PolicyInput) error {
	in.ScopeValue = mergeScopeValues(*in)
	in.ScopeValues = nil
	if in.ScopeType == "metric" {
		for i, v := range in.ScopeValue {
			if strings.Contains(v, ".") {
				continue
			}
			var metric persistence.CapacityMetric
			if err := h.db.Where("metric_code = ?", v).First(&metric).Error; err != nil {
				return resp.NewErr(resp.CodeNotFound, "指标不存在: %s", v)
			}
			in.ScopeValue[i] = metric.SystemCode + "." + metric.MetricCode
		}
	}
	return validatePolicyScope(in.ScopeType, in.ScopeValue, h.scopeSystems(c), h.isPlatformAdmin(c))
}

func (h *Handler) isPlatformAdmin(c *gin.Context) bool {
	if h.az == nil {
		return false
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	h.az.EnsureFresh()
	return h.az.IsPlatformAdmin(un)
}

func (h *Handler) metricVisible(c *gin.Context, systemCode string) bool {
	if h.isPlatformAdmin(c) {
		return true
	}
	for _, code := range h.scopeSystems(c) {
		if code == systemCode {
			return true
		}
	}
	return false
}

func parseDayRange(fromS, toS string) (time.Time, time.Time, error) {
	from, err := time.ParseInLocation("2006-01-02", fromS, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, resp.NewErr(resp.CodeBadRequest, "from 日期格式错误")
	}
	to, err := time.ParseInLocation("2006-01-02", toS, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, resp.NewErr(resp.CodeBadRequest, "to 日期格式错误")
	}
	return clock.DayStart(from), clock.DayStart(to), nil
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, resp.NewErr(resp.CodeBadRequest, "非法整数")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func parseID(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n, n > 0
}

func policyAuditDetail(p *persistence.AlertEscalationPolicy) string {
	if p == nil {
		return ""
	}
	return "mode=" + p.Mode + " scope=" + p.ScopeType + ":" + strings.Join(p.ScopeValue, ",")
}

func asStringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
