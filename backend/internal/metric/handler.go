package metric

import (
	"log/slog"
	"sort"
	"time"
	"strconv"
	"strings"

	"capacity-manage/backend/internal/metricagg"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"
"capacity-manage/backend/internal/pkg/scope"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	db  *gorm.DB
	az  *middleware.AuthZ
	log *slog.Logger
}

func NewHandler(db *gorm.DB, az *middleware.AuthZ, log *slog.Logger) *Handler {
	return &Handler{db: db, az: az, log: log}
}

// validateThreshold 阈值合理性校验（FR-MET-15）
// percent: higher_worse 0<warn<danger<=100; lower_worse 0<danger<warn<=100
// absolute: higher_worse warn<danger; lower_worse warn>danger
func validateThreshold(mode, direction string, warn, danger *float64) error {
	if warn == nil && danger == nil {
		return resp.NewErr(resp.CodeUnprocessable, "预警线/危险线至少配置一种")
	}
	switch mode {
	case "percent":
		if warn != nil && *warn <= 0 {
			return resp.NewErr(resp.CodeUnprocessable, "百分比预警线须 > 0")
		}
		if danger != nil && *danger > 100 {
			return resp.NewErr(resp.CodeUnprocessable, "百分比危险线须 <= 100")
		}
		if warn != nil && danger != nil {
			if direction == "lower_worse" {
				if !(*danger < *warn) {
					return resp.NewErr(resp.CodeUnprocessable, "越低越差: 须 0<危险线<预警线<=100")
				}
			} else if !(*warn < *danger) {
				return resp.NewErr(resp.CodeUnprocessable, "越高越差: 须 0<预警线<危险线<=100")
			}
		}
	case "absolute":
		if warn != nil && danger != nil {
			if direction == "lower_worse" {
				if !(*warn > *danger) {
					return resp.NewErr(resp.CodeUnprocessable, "越低越差: 须 预警线>危险线")
				}
			} else if !(*warn < *danger) {
				return resp.NewErr(resp.CodeUnprocessable, "越高越差: 须 预警线<危险线")
			}
		}
	default:
		return resp.NewErr(resp.CodeBadRequest, "%s", "未知阈值类型: " + mode)
	}
	return nil
}

type metricReq struct {
	MetricCode     string   `json:"metricCode" binding:"required,min=2,max=64"`
	Name           string   `json:"name" binding:"required"`
	MetricKind     string   `json:"metricKind" binding:"omitempty,oneof=normal group aggregate"`
	ParentCode       string   `json:"parentCode" binding:"max=64"`
	StatModule       string   `json:"statModule" binding:"max=32"`
	MainModule       string   `json:"mainModule" binding:"max=32"`
	WeightMetricCode string   `json:"weightMetricCode" binding:"max=64"`
	SystemCode       string   `json:"systemCode" binding:"required"`
	Category       string   `json:"category"`
	MetricType     string   `json:"metricType" binding:"omitempty,oneof=usage absolute increment"`
	Unit           string   `json:"unit" binding:"required"`
	DsType         string   `json:"dsType" binding:"omitempty,oneof=db log"`
	CollectMethod  string   `json:"collectMethod" binding:"omitempty,oneof=sql logparse python"`
	StatMode       string   `json:"statMode" binding:"omitempty,oneof=latest max min avg sum"`
	JudgeDirection string   `json:"judgeDirection" binding:"omitempty,oneof=higher_worse lower_worse"`
	Baseline       *float64 `json:"baseline"`
	ThresholdMode  string   `json:"thresholdMode" binding:"omitempty,oneof=percent absolute"`
	WarnLine       *float64 `json:"warnLine"`
	DangerLine     *float64 `json:"dangerLine"`
	Remark         string   `json:"remark"`
}

func (h *Handler) List(c *gin.Context) {
	q := h.db.Model(&persistence.CapacityMetric{})
	if v := c.Query("systemCode"); v != "" {
		q = q.Where("system_code = ?", v)
	}
	if v := c.Query("metricType"); v != "" {
		q = q.Where("metric_type = ?", v)
	}
	if v := c.Query("unit"); v != "" {
		q = q.Where("unit = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("metricKind"); v != "" {
		q = q.Where("metric_kind = ?", v)
	}
	if kw := c.Query("keyword"); kw != "" {
		q = q.Where("name LIKE ? OR metric_code LIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	q = scope.Apply(q, scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)))
	var total int64
	_ = q.Count(&total).Error
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := atoiSafe(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := atoiSafe(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	var items []persistence.CapacityMetric
	_ = q.Order("metric_code").Offset((page - 1) * size).Limit(size).Find(&items).Error
	resp.OK(c, resp.Page(items, total, page, size))
}

func (h *Handler) Get(c *gin.Context) {
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", c.Param("code")).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !scope.Contains(scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)), m.SystemCode) {
		scope.Forbidden(c, m.SystemCode)
		return
	}
	// 当前值与水位摘要（最近日数据）
	type dayRow struct {
		StatValue  *float64 `gorm:"column:stat_value"`
		WaterLevel *float64 `gorm:"column:water_level"`
		Level      string   `gorm:"column:level"`
		BizDate    string   `gorm:"column:biz_date"`
	}
	var dr dayRow
	_ = h.db.Raw(`SELECT stat_value, water_level, level, to_char(biz_date,'YYYY-MM-DD') AS biz_date
		FROM metric_value_day WHERE metric_code = ? ORDER BY biz_date DESC LIMIT 1`, m.MetricCode).Scan(&dr).Error
	resp.OK(c, gin.H{"metric": m, "latest": dr})
}

func (h *Handler) Create(c *gin.Context) {
	var req metricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	req.MetricKind = defaultOf(req.MetricKind, "normal")
	if req.MetricKind == "normal" {
		req.MetricType, req.StatMode, req.JudgeDirection, req.ThresholdMode =
			defaultOf(req.MetricType, "usage"), defaultOf(req.StatMode, "latest"),
			defaultOf(req.JudgeDirection, "higher_worse"), defaultOf(req.ThresholdMode, "percent")
		if err := validateThreshold(req.ThresholdMode, req.JudgeDirection, req.WarnLine, req.DangerLine); err != nil {
			resp.Fail(c, err)
			return
		}
	}
	if req.MetricKind == "aggregate" {
		// 汇总指标阈值可选: 配置后与常规指标同口径校验, 用于日值水位与阈值告警联动
		req.ThresholdMode = defaultOf(req.ThresholdMode, "absolute")
		req.JudgeDirection = defaultOf(req.JudgeDirection, "higher_worse")
		if req.WarnLine != nil || req.DangerLine != nil {
			if err := validateThreshold(req.ThresholdMode, req.JudgeDirection, req.WarnLine, req.DangerLine); err != nil {
				resp.Fail(c, err)
				return
			}
		}
	}
	if err := h.validateTree(req.MetricKind, req.ParentCode, req.StatModule, req.SystemCode); err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.validateWeight(req.WeightMetricCode, req.MetricCode, req.ParentCode, req.SystemCode); err != nil {
		resp.Fail(c, err)
		return
	}
	var sysN int64
	_ = h.db.Model(&persistence.BusinessSystem{}).Where("system_code = ?", req.SystemCode).Count(&sysN).Error
	if sysN == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", "归属系统不存在: " + req.SystemCode))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	m := persistence.CapacityMetric{
		MetricCode: req.MetricCode, Name: req.Name, SystemCode: req.SystemCode,
		Category: req.Category, MetricKind: req.MetricKind, StatModule: req.StatModule,
		MetricType: req.MetricType, Unit: req.Unit,
		DsType: req.DsType, CollectMethod: req.CollectMethod, StatMode: req.StatMode,
		JudgeDirection: req.JudgeDirection, Baseline: req.Baseline,
		BaselineSource: "manual", ThresholdMode: req.ThresholdMode,
		WarnLine: req.WarnLine, DangerLine: req.DangerLine, Status: "active",
		Remark: req.Remark, CreatedBy: un,
	}
	if req.ParentCode != "" {
		pc := req.ParentCode
		m.ParentCode = &pc
	}
	if req.MetricKind == "aggregate" {
		m.MainModule = req.MainModule
	}
	if req.WeightMetricCode != "" {
		wc := req.WeightMetricCode
		m.WeightMetricCode = &wc
	}
	if err := h.db.Create(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "创建失败(编码或 系统内名称 已存在?)"))
		return
	}
	middleware.Log(c, "metric", "metric.create", m.MetricCode, "")
	resp.OK(c, m)
}

func (h *Handler) Update(c *gin.Context) {
	var req struct {
		Name             string   `json:"name"`
		Category         string   `json:"category"`
		Unit             string   `json:"unit"`
		StatMode         string   `json:"statMode"`
		JudgeDirection   string   `json:"judgeDirection"`
		Baseline         *float64 `json:"baseline"`
		Remark           string   `json:"remark"`
		MainModule       *string  `json:"mainModule"`
		WeightMetricCode *string  `json:"weightMetricCode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	code := c.Param("code")
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Category != "" {
		updates["category"] = req.Category
	}
	if req.Unit != "" {
		updates["unit"] = req.Unit
	}
	if req.StatMode != "" {
		updates["stat_mode"] = req.StatMode
	}
	if req.JudgeDirection != "" {
		updates["judge_direction"] = req.JudgeDirection
	}
	if req.Baseline != nil {
		updates["baseline"] = *req.Baseline
	}
	if req.Remark != "" {
		updates["remark"] = req.Remark
	}
	if req.MainModule != nil {
		if m.MetricKind != "aggregate" && *req.MainModule != "" {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "主统计模块仅汇总指标可配置"))
			return
		}
		updates["main_module"] = *req.MainModule
	}
	if req.WeightMetricCode != nil {
		wc := *req.WeightMetricCode
		if wc != "" {
			if m.MetricKind != "normal" {
				resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "权重指标仅普通指标(统计模块)可配置"))
				return
			}
			sp := ""
			if m.ParentCode != nil {
				sp = *m.ParentCode
			}
			if err := h.validateWeight(wc, code, sp, m.SystemCode); err != nil {
				resp.Fail(c, err)
				return
			}
		}
		if wc == "" {
			updates["weight_metric_code"] = nil
		} else {
			updates["weight_metric_code"] = wc
		}
	}
	_ = h.db.Model(&m).Updates(updates).Error
	middleware.Log(c, "metric", "metric.update", code, "")
	resp.OK(c, nil)
}

// Disable 停用（FR-MET-03: 已产生告警需二次确认 confirm=true）
func (h *Handler) Disable(c *gin.Context) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	_ = c.ShouldBindJSON(&req)
	code := c.Param("code")
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if m.Status == "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "指标已停用"))
		return
	}
	var alertN int64
	_ = h.db.Model(&persistence.AlertRecord{}).Where("metric_code = ?", code).Count(&alertN).Error
	if alertN > 0 && !req.Confirm {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "该指标已产生告警记录，停用需二次确认(confirm=true)"))
		return
	}
	_ = h.db.Model(&m).Update("status", "disabled").Error
	// 联动停用其采集流程
	_ = h.db.Model(&persistence.CollectFlow{}).Where("metric_code = ? AND status='active'", code).
		Updates(map[string]any{"status": "disabled", "enabled": false}).Error
	middleware.Log(c, "metric", "metric.disable", code, "")
	resp.OK(c, nil)
}

// Enable 重新启用（与停用互逆: 联动恢复被停用联动的采集流程）
func (h *Handler) Enable(c *gin.Context) {
	code := c.Param("code")
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if m.Status == "active" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "指标已启用"))
		return
	}
	_ = h.db.Model(&m).Update("status", "active").Error
	// 逆操作: 恢复停用指标时联动停用的采集流程
	_ = h.db.Model(&persistence.CollectFlow{}).
		Where("metric_code = ? AND status = 'disabled' AND enabled = ?", code, false).
		Updates(map[string]any{"status": "active", "enabled": true}).Error
	middleware.Log(c, "metric", "metric.enable", code, "")
	resp.OK(c, nil)
}

// Delete 删除（仅停用的指标可删; 有关联历史数据/告警/流程的禁止物理删除, 保留停用即可）
func (h *Handler) Delete(c *gin.Context) {
	code := c.Param("code")
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if m.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅停用的指标可删除, 请先停用"))
		return
	}
	checks := []struct {
		model any
		label string
	}{
		{&persistence.CollectFlow{}, "采集流程"},
		{&persistence.MetricValueDay{}, "历史日数据"},
		{&persistence.MetricValueRT{}, "实时数据"},
		{&persistence.AlertRecord{}, "告警记录"},
		{&persistence.ThresholdHistory{}, "阈值调整历史"},
	}
	var childN int64
	_ = h.db.Model(&persistence.CapacityMetric{}).Where("parent_code = ?", code).Count(&childN).Error
	if childN > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "存在 %d 个子指标, 不可删除; 请先移除或迁移子指标", childN))
		return
	}
	var blocked []string
	for _, ck := range checks {
		var n int64
		_ = h.db.Model(ck.model).Where("metric_code = ?", code).Count(&n).Error
		if n > 0 {
			blocked = append(blocked, ck.label)
		}
	}
	if len(blocked) > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "指标存在%s, 不可删除; 请保持停用状态", strings.Join(blocked, "/")))
		return
	}
	if err := h.db.Delete(&m).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "metric.delete", code, "")
	resp.OK(c, nil)
}

// UpdateThreshold 阈值在线调整（FR-MET-12: 即时生效 + 历史）
func (h *Handler) UpdateThreshold(c *gin.Context) {
	var req struct {
		Baseline   *float64 `json:"baseline"`
		WarnLine   *float64 `json:"warnLine"`
		DangerLine *float64 `json:"dangerLine"`
		Reason     string   `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	code := c.Param("code")
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	newBaseline, newWarn, newDanger := m.Baseline, m.WarnLine, m.DangerLine
	if req.Baseline != nil {
		newBaseline = req.Baseline
	}
	if req.WarnLine != nil {
		newWarn = req.WarnLine
	}
	if req.DangerLine != nil {
		newDanger = req.DangerLine
	}
	if err := validateThreshold(m.ThresholdMode, m.JudgeDirection, newWarn, newDanger); err != nil {
		resp.Fail(c, err)
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	// 先快照旧值: GORM Updates(map) 会回写内存结构体, 之后再取 m.* 会拿到新值
	warnOld, dangerOld, baselineOld := m.WarnLine, m.DangerLine, m.Baseline
	tx := h.db.Begin()
	_ = tx.Model(&m).Updates(map[string]any{
		"baseline": newBaseline, "warn_line": newWarn, "danger_line": newDanger,
		"baseline_source": "manual",
	}).Error
	hist := persistence.ThresholdHistory{
		MetricCode: code, Operator: un, ChangedAt: time.Now(),
		BaselineOld: baselineOld, BaselineNew: newBaseline,
		WarnOld: warnOld, WarnNew: newWarn,
		DangerOld: dangerOld, DangerNew: newDanger,
		Reason: req.Reason,
	}
	if err := tx.Create(&hist).Error; err != nil {
		tx.Rollback()
		resp.Fail(c, err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "metric.threshold", code, "warn="+fstr(newWarn)+" danger="+fstr(newDanger))
	resp.OK(c, hist)
}

func (h *Handler) ThresholdHistory(c *gin.Context) {
	var items []persistence.ThresholdHistory
	_ = h.db.Where("metric_code = ?", c.Param("code")).Order("changed_at DESC, id DESC").Limit(100).Find(&items).Error
	resp.OK(c, items)
}

func defaultOf(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func fstr(p *float64) string {
	if p == nil {
		return "nil"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func atoiSafe(s string) (int, error) {
	n := 0
	neg := false
	for i, r := range s {
		if i == 0 && (r == '-' || r == '+') {
			neg = r == '-'
			continue
		}
		if r < '0' || r > '9' {
			return 0, errInvalid
		}
		n = n*10 + int(r-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

var errInvalid = resp.NewErr(resp.CodeBadRequest, "invalid number")


// ---------- 汇总指标树 ----------

// validateTree 汇总指标树形校验:
//
//	aggregate: 无父指标
//	group:     父必须是同系统 aggregate
//	normal:    父可为同系统 aggregate/group; 挂在 group 下必须指定 stat_module
func (h *Handler) validateTree(kind, parentCode, statModule, systemCode string) error {
	if kind == "aggregate" {
		if parentCode != "" {
			return resp.NewErr(resp.CodeBadRequest, "汇总指标不能有上级指标")
		}
		return nil
	}
	if parentCode == "" {
		if kind == "group" {
			return resp.NewErr(resp.CodeBadRequest, "子指标分组必须挂靠在汇总指标下")
		}
		return nil
	}
	var p persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", parentCode).First(&p).Error; err != nil {
		return resp.NewErr(resp.CodeBadRequest, "上级指标不存在: %s", parentCode)
	}
	if p.SystemCode != systemCode {
		return resp.NewErr(resp.CodeBadRequest, "上级指标与归属系统不一致")
	}
	switch kind {
	case "group":
		if p.MetricKind != "aggregate" {
			return resp.NewErr(resp.CodeBadRequest, "子指标分组的上级必须是汇总指标")
		}
	case "normal":
		if p.MetricKind != "aggregate" && p.MetricKind != "group" {
			return resp.NewErr(resp.CodeBadRequest, "上级指标须为汇总指标或子指标分组")
		}
		if p.MetricKind == "group" && statModule == "" {
			return resp.NewErr(resp.CodeBadRequest, "子指标分组下的统计模块须填写模块标识(如 count/max_latency/avg_latency)")
		}
	}
	return nil
}

// validateWeight 权重指标校验: 存在/同系统/仅普通/非自身/同一父级下同级(加权均值口径: 如平均耗时按请求量加权)
func (h *Handler) validateWeight(weightCode, selfCode, selfParentCode, systemCode string) error {
	if weightCode == "" {
		return nil
	}
	if weightCode == selfCode {
		return resp.NewErr(resp.CodeBadRequest, "权重指标不能是指标自身")
	}
	var w persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", weightCode).First(&w).Error; err != nil {
		return resp.NewErr(resp.CodeBadRequest, "权重指标不存在: %s", weightCode)
	}
	if w.SystemCode != systemCode {
		return resp.NewErr(resp.CodeBadRequest, "权重指标须与归属系统一致")
	}
	if w.MetricKind != "normal" {
		return resp.NewErr(resp.CodeBadRequest, "权重指标必须是普通指标")
	}
	if selfParentCode == "" {
		return resp.NewErr(resp.CodeBadRequest, "仅父级指标下的子指标可配置权重指标")
	}
	if w.ParentCode == nil || *w.ParentCode != selfParentCode {
		return resp.NewErr(resp.CodeBadRequest, "权重指标须为同一父级下的同级指标: %s", weightCode)
	}
	return nil
}

type metricTreeNode struct {
	persistence.CapacityMetric
	Children []*metricTreeNode `json:"children,omitempty"`
}

func metricKindRank(kind string) int {
	switch kind {
	case "aggregate":
		return 0
	case "group":
		return 1
	default:
		return 2
	}
}

func sortTreeNodes(ns []*metricTreeNode) {
	sort.Slice(ns, func(i, j int) bool {
		ri, rj := metricKindRank(ns[i].MetricKind), metricKindRank(ns[j].MetricKind)
		if ri != rj {
			return ri < rj
		}
		return ns[i].MetricCode < ns[j].MetricCode
	})
}

// Tree 指标树(汇总指标 -> 子指标分组 -> 统计模块; 独立普通指标平铺为顶层节点)
func (h *Handler) Tree(c *gin.Context) {
	q := h.db.Model(&persistence.CapacityMetric{})
	if v := c.Query("systemCode"); v != "" {
		q = q.Where("system_code = ?", v)
	}
	if kw := c.Query("keyword"); kw != "" {
		q = q.Where("name LIKE ? OR metric_code LIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	q = scope.Apply(q, scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)))
	var items []persistence.CapacityMetric
	if err := q.Order("metric_code").Find(&items).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	nodes := make(map[string]*metricTreeNode, len(items))
	for i := range items {
		nodes[items[i].MetricCode] = &metricTreeNode{CapacityMetric: items[i]}
	}
	var roots []*metricTreeNode
	for i := range items {
		n := nodes[items[i].MetricCode]
		if items[i].ParentCode != nil {
			if p, ok := nodes[*items[i].ParentCode]; ok {
				p.Children = append(p.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	sortTreeNodes(roots)
	for _, n := range nodes {
		sortTreeNodes(n.Children)
	}
	resp.OK(c, gin.H{"roots": roots, "count": len(items)})
}

// parseTrendRange 解析 days 或 from/to 区间(与 data.Trend 口径一致)
func parseTrendRange(c *gin.Context) (time.Time, time.Time, error) {
	if c.Query("from") != "" || c.Query("to") != "" {
		f, err1 := time.Parse("2006-01-02", c.Query("from"))
		t, err2 := time.Parse("2006-01-02", c.Query("to"))
		if err1 != nil || err2 != nil {
			return time.Time{}, time.Time{}, resp.NewErr(resp.CodeBadRequest, "from/to 格式 YYYY-MM-DD")
		}
		if t.Before(f) {
			return time.Time{}, time.Time{}, resp.NewErr(resp.CodeBadRequest, "to 不得早于 from")
		}
		from := clock.DayStart(f)
		to := clock.DayStart(t).AddDate(0, 0, 1)
		if to.Sub(from) > 400*24*time.Hour {
			return time.Time{}, time.Time{}, resp.NewErr(resp.CodeBadRequest, "区间跨度须 ≤ 400 天")
		}
		return from, to, nil
	}
	d, err := atoiSafe(c.DefaultQuery("days", "30"))
	if err != nil || d <= 0 || d > 400 {
		return time.Time{}, time.Time{}, resp.NewErr(resp.CodeBadRequest, "days 须为 1~400")
	}
	return clock.DayStart(time.Now().AddDate(0, 0, -d)), clock.DayStart(time.Now()).AddDate(0, 0, 1), nil
}

// Modules 统计模块视图(汇总指标/子指标分组详情页):
//
//	group:     直接子指标(统计模块)各自的值与序列
//	aggregate: 按 stat_module 跨子指标聚合 —— 每个模块按首个叶子指标的 statMode
//	           (sum/max/avg/min) 合并子指标; 日粒度按 biz_date 合并, 日内按同一时间戳对齐
func (h *Handler) Modules(c *gin.Context) {
	code := c.Param("code")
	var m persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&m).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !scope.Contains(scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)), m.SystemCode) {
		scope.Forbidden(c, m.SystemCode)
		return
	}
	if m.MetricKind != "group" && m.MetricKind != "aggregate" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "仅汇总指标/子指标分组支持统计模块视图"))
		return
	}
	gran := c.DefaultQuery("granularity", "day")
	if gran != "day" && gran != "rt" {
		gran = "day"
	}
	from, to, rerr := parseTrendRange(c)
	if rerr != nil {
		resp.Fail(c, rerr)
		return
	}

	var leaves []persistence.CapacityMetric
	if err := h.db.Where("parent_code = ? AND metric_kind = 'normal'", code).
		Order("metric_code").Find(&leaves).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if m.MetricKind == "aggregate" {
		var groupCodes []string
		_ = h.db.Model(&persistence.CapacityMetric{}).
			Where("parent_code = ? AND metric_kind = 'group'", code).
			Pluck("metric_code", &groupCodes).Error
		if len(groupCodes) > 0 {
			var subs []persistence.CapacityMetric
			if err := h.db.Where("parent_code IN ? AND metric_kind = 'normal'", groupCodes).
				Order("metric_code").Find(&subs).Error; err != nil {
				resp.Fail(c, err)
				return
			}
			leaves = append(leaves, subs...)
		}
	}

	out := make([]moduleView, 0, 1)
	if len(leaves) > 0 {
		var rows []struct {
			MetricCode string    `gorm:"column:metric_code"`
			Ts         time.Time `gorm:"column:ts"`
			Value      *float64  `gorm:"column:value"`
		}
		var err error
		// 查询编码: 叶子指标 + 其权重指标(加权均值按同一 biz_date/ts 对齐取权重值)
		codes := make([]string, 0, len(leaves)*2)
		for i := range leaves {
			codes = append(codes, leaves[i].MetricCode)
			if leaves[i].WeightMetricCode != nil && *leaves[i].WeightMetricCode != "" {
				codes = append(codes, *leaves[i].WeightMetricCode)
			}
		}
		if gran == "rt" {
			err = h.db.Raw(`SELECT metric_code, ts, clean_value AS value
				FROM metric_value_rt WHERE metric_code IN ? AND ts >= ? AND ts < ?
				ORDER BY ts`, codes, from, to).Scan(&rows).Error
		} else {
			err = h.db.Raw(`SELECT metric_code, biz_date AS ts, stat_value AS value
				FROM metric_value_day WHERE metric_code IN ? AND biz_date >= ? AND biz_date < ?
				ORDER BY biz_date`, codes, from, to).Scan(&rows).Error
		}
		if err != nil {
			resp.Fail(c, err)
			return
		}
		out = h.buildModuleViews(code, leaves, rows)
	}
	resp.OK(c, gin.H{
		"metricCode": code, "name": m.Name, "metricKind": m.MetricKind,
		"systemCode": m.SystemCode, "granularity": gran,
		"moduleCount": len(out), "childMetricCount": len(leaves),
		"modules": out,
	})
}

type modChild struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Unit     string   `json:"unit"`
	StatMode string   `json:"statMode"`
	Weighted bool     `json:"weighted"`
	Latest   *float64 `json:"latest"`
	LatestTs *time.Time `json:"latestTs"`
}

type modPoint struct {
	Ts    time.Time `json:"ts"`
	Value *float64  `json:"value"`
}

type modRangeStats struct {
	Count int      `json:"count"`
	Max   *float64 `json:"max"`
	Min   *float64 `json:"min"`
	Avg   *float64 `json:"avg"`
	sum   float64
}

type moduleView struct {
	Module   string       `json:"module"`
	Name     string       `json:"name"`
	Unit     string       `json:"unit"`
	AggMode  string       `json:"aggMode"` // self/sum/max/avg/min/weighted_avg
	Children []modChild   `json:"children"`
	Latest   *float64     `json:"latest"`
	LatestTs *time.Time   `json:"latestTs"`
	Series   []modPoint   `json:"series"`
	Stats    modRangeStats `json:"stats"`
}

// buildModuleViews 将叶子指标及其数据按统计模块分组并聚合
//
// 聚合口径与日汇总(data.summarizeAggregates)一致, 统一由 metricagg 提供:
// 单叶子=self; 配置权重指标=weighted_avg(加权均值); 否则取首个叶子 statMode。
func (h *Handler) buildModuleViews(code string, leaves []persistence.CapacityMetric, rows []struct {
	MetricCode string    `gorm:"column:metric_code"`
	Ts         time.Time `gorm:"column:ts"`
	Value      *float64  `gorm:"column:value"`
}) []moduleView {
	type moduleAgg struct {
		key    string
		leaves []persistence.CapacityMetric
	}
	modMap := map[string]*moduleAgg{}
	modOrder := []string{}
	for i := range leaves {
		l := leaves[i]
		key := l.StatModule
		if key == "" {
			key = l.MetricCode
		}
		mg, ok := modMap[key]
		if !ok {
			mg = &moduleAgg{key: key}
			modMap[key] = mg
			modOrder = append(modOrder, key)
		}
		mg.leaves = append(mg.leaves, l)
	}

	out := make([]moduleView, 0, len(modOrder))
	for _, key := range modOrder {
		mg := modMap[key]
		statModes := make([]string, len(mg.leaves))
		weights := make([]*string, len(mg.leaves))
		for i, l := range mg.leaves {
			statModes[i] = l.StatMode
			weights[i] = l.WeightMetricCode
		}
		mode := metricagg.Mode(statModes, weights)
		// 参与查询的编码: 叶子指标 + 其权重指标
		codeSet := map[string]bool{}
		for _, l := range mg.leaves {
			codeSet[l.MetricCode] = true
			if l.WeightMetricCode != nil && *l.WeightMetricCode != "" {
				codeSet[*l.WeightMetricCode] = true
			}
		}
		// ts -> 编码 -> 值(每个指标同一时刻/业务日一条; 权重指标值同表对齐)
		byTs := map[time.Time]map[string]*float64{}
		var tsList []time.Time
		for i := range rows {
			r := &rows[i]
			if !codeSet[r.MetricCode] {
				continue
			}
			m, ok := byTs[r.Ts]
			if !ok {
				m = map[string]*float64{}
				byTs[r.Ts] = m
				tsList = append(tsList, r.Ts)
			}
			m[r.MetricCode] = r.Value
		}
		sort.Slice(tsList, func(i, j int) bool { return tsList[i].Before(tsList[j]) })
		// 模块名: 各叶子同名(如"请求量")时取该名; 不同名时留空, 避免以某一分支的名字代表整个模块
		modName := mg.leaves[0].Name
		for _, l := range mg.leaves[1:] {
			if l.Name != modName {
				modName = ""
				break
			}
		}
		mv := moduleView{
			Module: key, Name: modName, Unit: mg.leaves[0].Unit,
			AggMode: mode, Series: []modPoint{},
		}
		// 各叶子最新值(区间内最后一个非空值), 供明细展示(如"交易/非交易"分开看)
		childLatest := map[string]*float64{}
		childLatestTs := map[string]time.Time{}
		for _, t := range tsList {
			for _, l := range mg.leaves {
				if v, ok := byTs[t][l.MetricCode]; ok && v != nil {
					childLatest[l.MetricCode] = v
					childLatestTs[l.MetricCode] = t
				}
			}
		}
		for _, l := range mg.leaves {
			ch := modChild{
				Code: l.MetricCode, Name: l.Name, Unit: l.Unit,
				StatMode: l.StatMode, Weighted: l.WeightMetricCode != nil,
				Latest: childLatest[l.MetricCode],
			}
			if ts, ok := childLatestTs[l.MetricCode]; ok {
				tsCopy := ts
				ch.LatestTs = &tsCopy
			}
			mv.Children = append(mv.Children, ch)
		}
		for _, t := range tsList {
			pairs := make([]metricagg.Pair, 0, len(mg.leaves))
			for _, l := range mg.leaves {
				p := metricagg.Pair{Value: byTs[t][l.MetricCode]}
				if l.WeightMetricCode != nil && *l.WeightMetricCode != "" {
					p.Weight = byTs[t][*l.WeightMetricCode]
				}
				pairs = append(pairs, p)
			}
			v := metricagg.Aggregate(mode, pairs)
			if v == nil {
				continue
			}
			mv.Series = append(mv.Series, modPoint{Ts: t, Value: v})
			mv.Stats.Count++
			mv.Stats.sum += *v
			if mv.Stats.Max == nil || *v > *mv.Stats.Max {
				mv.Stats.Max = v
			}
			if mv.Stats.Min == nil || *v < *mv.Stats.Min {
				mv.Stats.Min = v
			}
		}
		if mv.Stats.Count > 0 {
			avg := mv.Stats.sum / float64(mv.Stats.Count)
			mv.Stats.Avg = &avg
		}
		for i := len(mv.Series) - 1; i >= 0; i-- {
			if mv.Series[i].Value != nil {
				mv.Latest = mv.Series[i].Value
				ts := mv.Series[i].Ts
				mv.LatestTs = &ts
				break
			}
		}
		out = append(out, mv)
	}
	return out
}
