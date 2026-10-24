package bizsystem

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
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

func (h *Handler) List(c *gin.Context) {
	q := h.db.Model(&persistence.BusinessSystem{})
	if kw := c.Query("keyword"); kw != "" {
		q = q.Where("name LIKE ? OR system_code LIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	if v := c.Query("dept"); v != "" {
		q = q.Where("dept = ?", v)
	}
	if v := c.Query("level"); v != "" {
		q = q.Where("level = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	var total int64
	_ = q.Count(&total).Error
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		_, _ = strconv.Atoi(v)
	}
	if v := c.Query("pageSize"); v != "" {
		_, _ = strconv.Atoi(v)
	}
	var items []persistence.BusinessSystem
	_ = q.Order("system_code").Offset((page - 1) * size).Limit(size).Find(&items).Error

	// 指标数摘要
	codes := make([]string, 0, len(items))
	for _, s := range items {
		codes = append(codes, s.SystemCode)
	}
	counts := map[string]int64{}
	if len(codes) > 0 {
		type cnt struct {
			SystemCode string
			N          int64
		}
		var rows []cnt
		_ = h.db.Model(&persistence.CapacityMetric{}).
			Where("system_code IN ?", codes).
			Select("system_code, count(*) as n").Group("system_code").Scan(&rows).Error
		for _, r := range rows {
			counts[r.SystemCode] = r.N
		}
	}
	type sysView struct {
		persistence.BusinessSystem
		MetricCount int64 `json:"metricCount"`
	}
	out := make([]sysView, 0, len(items))
	for _, s := range items {
		out = append(out, sysView{BusinessSystem: s, MetricCount: counts[s.SystemCode]})
	}
	resp.OK(c, resp.Page(out, total, page, size))
}

func (h *Handler) Get(c *gin.Context) {
	var s persistence.BusinessSystem
	if err := h.db.Where("system_code = ?", c.Param("code")).First(&s).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "业务系统不存在"))
		return
	}
	resp.OK(c, s)
}

func (h *Handler) Create(c *gin.Context) {
	var req struct {
		SystemCode string     `json:"systemCode" binding:"required,min=2,max=64"`
		Name       string     `json:"name" binding:"required"`
		Dept       string     `json:"dept"`
		Owner      string     `json:"owner" binding:"required"`
		Level      string     `json:"level" binding:"omitempty,oneof=core important general"`
		OnlineDate *time.Time `json:"onlineDate"`
		Remark     string     `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if req.Level == "" {
		req.Level = "general"
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	s := persistence.BusinessSystem{
		SystemCode: req.SystemCode, Name: req.Name, Dept: req.Dept,
		Owner: req.Owner, Level: req.Level, Status: "normal",
		OnlineDate: req.OnlineDate, Remark: req.Remark, CreatedBy: un,
	}
	if err := h.db.Create(&s).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "创建失败(编码或名称已存在?)"))
		return
	}
	middleware.Log(c, "system", "system.create", s.SystemCode, "")
	resp.OK(c, s)
}

func (h *Handler) Update(c *gin.Context) {
	var req struct {
		Name   string `json:"name"`
		Dept   string `json:"dept"`
		Owner  string `json:"owner"`
		Level  string `json:"level"`
		Remark string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	code := c.Param("code")
	var s persistence.BusinessSystem
	if err := h.db.Where("system_code = ?", code).First(&s).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "业务系统不存在"))
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Dept != "" {
		updates["dept"] = req.Dept
	}
	if req.Owner != "" {
		updates["owner"] = req.Owner
	}
	if req.Level != "" {
		updates["level"] = req.Level
	}
	if req.Remark != "" {
		updates["remark"] = req.Remark
	}
	_ = h.db.Model(&s).Updates(updates).Error
	middleware.Log(c, "system", "system.update", code, "")
	resp.OK(c, nil)
}

// SetStatus 状态管理: maintenance=静默, offline=冻结级联（FR-SYS-04/06）
func (h *Handler) SetStatus(c *gin.Context) {
	var req struct {
		Status string `json:"status" binding:"required,oneof=normal maintenance offline"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	code := c.Param("code")
	var s persistence.BusinessSystem
	if err := h.db.Where("system_code = ?", code).First(&s).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "业务系统不存在"))
		return
	}
	if s.Status == req.Status {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "状态未变化"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	tx := h.db.Begin()
	if err := tx.Model(&s).Update("status", req.Status).Error; err != nil {
		tx.Rollback()
		resp.Fail(c, err)
		return
	}
	// 下线级联: 流程停用 + 活动告警关闭（FR-SYS-06）
	if req.Status == "offline" {
		_ = tx.Model(&persistence.CollectFlow{}).
			Where("metric_code IN (SELECT metric_code FROM capacity_metric WHERE system_code = ?)", code).
			Where("status IN ('created','active')").
			Updates(map[string]any{"status": "disabled", "enabled": false}).Error
		var alerts []persistence.AlertRecord
		_ = tx.Where("system_code = ? AND status IN ('active','processing')", code).Find(&alerts).Error
		now := time.Now()
		for i := range alerts {
			alerts[i].Status = "closed"
			alerts[i].ClosedBy = un
			alerts[i].ClosedAt = &now
			alerts[i].CloseResult = "系统下线"
			_ = tx.Save(&alerts[i]).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "system", "system.status", code, "to="+req.Status)
	resp.OK(c, nil)
}

// Delete 删除业务系统（仅「已下线」可删）。
// 语义: 下线是冻结/停止采集, 删除是彻底清理该系统自有的配置与数据(采集流程/指标/数据源/分类/历史值/告警/资源/数据权限)。
// 存在跨系统引用时拒绝删除, 避免破坏其它系统的配置完整性。
func (h *Handler) Delete(c *gin.Context) {
	code := c.Param("code")
	var s persistence.BusinessSystem
	if err := h.db.Where("system_code = ?", code).First(&s).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "业务系统不存在"))
		return
	}
	if s.Status != "offline" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅「已下线」的业务系统可删除, 请先下线"))
		return
	}

	// 该系统自有的指标/数据源/分类
	var metricCodes []string
	_ = h.db.Model(&persistence.CapacityMetric{}).
		Where("system_code = ?", code).Pluck("metric_code", &metricCodes).Error
	var dsIDs []int64
	_ = h.db.Model(&persistence.DataSource{}).
		Where("system_code = ?", code).Pluck("id", &dsIDs).Error
	var catIDs []int64
	_ = h.db.Model(&persistence.DataSourceCategory{}).
		Where("system_code = ?", code).Pluck("id", &catIDs).Error

	// 跨系统引用校验(不满足时给出可操作的提示)
	// 该系统原因: metric_code 属于本系统, 已随删除一并清理; 仅统计引用本系统数据源/分类的"外来"流程
	externalFlow := func(col string, ids []any) *gorm.DB {
		q := h.db.Model(&persistence.CollectFlow{}).Where(col+" IN ?", ids)
		if len(metricCodes) > 0 {
			q = q.Where("metric_code NOT IN ?", metricCodes)
		}
		return q
	}
	guardFlow := func(col string, ids []any) (bool, string) {
		if len(ids) == 0 {
			return false, ""
		}
		var n int64
		_ = externalFlow(col, ids).Count(&n).Error
		if n == 0 {
			return false, ""
		}
		var names []string
		_ = externalFlow(col, ids).Order("id").Limit(5).Pluck("flow_code", &names).Error
		return true, strings.Join(names, ", ")
	}
	dsRef := make([]any, 0, len(dsIDs))
	for _, id := range dsIDs {
		dsRef = append(dsRef, id)
	}
	if blocked, names := guardFlow("data_source_id", dsRef); blocked {
		resp.Fail(c, resp.NewErr(resp.CodeConflict,
			"该系统的数据源仍被其它系统的采集流程引用(%s)，请先调整相关流程", names))
		return
	}
	catRef := make([]any, 0, len(catIDs))
	for _, id := range catIDs {
		catRef = append(catRef, id)
	}
	if blocked, names := guardFlow("data_source_category_id", catRef); blocked {
		resp.Fail(c, resp.NewErr(resp.CodeConflict,
			"该系统的数据源分类仍被其它系统的采集流程引用(%s)，请先调整相关流程", names))
		return
	}
	if len(metricCodes) > 0 {
		var childN int64
		_ = h.db.Model(&persistence.CapacityMetric{}).
			Where("system_code <> ?", code).
			Where("parent_code IN ? OR weight_metric_code IN ?", metricCodes, metricCodes).
			Count(&childN).Error
		if childN > 0 {
			resp.Fail(c, resp.NewErr(resp.CodeConflict,
				"该系统的指标仍被其它系统的指标引用(子指标/加权指标) %d 处，请先解除引用", childN))
			return
		}
	}

	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)

	tx := h.db.Begin()
	if tx.Error != nil {
		resp.Fail(c, tx.Error)
		return
	}
	rollback := func(err error) {
		tx.Rollback()
		resp.Fail(c, err)
	}

	// 采集流程(含执行日志, cleaning_trace 随任务日志级联)
	if len(metricCodes) > 0 {
		var flowIDs []int64
		_ = tx.Model(&persistence.CollectFlow{}).
			Where("metric_code IN ?", metricCodes).Pluck("id", &flowIDs).Error
		if len(flowIDs) > 0 {
			if err := tx.Where("flow_id IN ?", flowIDs).
				Delete(&persistence.CollectTaskLog{}).Error; err != nil {
				rollback(err)
				return
			}
		}
	}
	if err := tx.Where("metric_code IN ?", metricCodes).Delete(&persistence.CollectFlow{}).Error; err != nil {
		rollback(err)
		return
	}

	// 指标相关记录: 阈值历史 + 落库数据(实时/日/峰谷/修正)
	for _, del := range []struct {
		model any
		col   string
	}{
		{&persistence.ThresholdHistory{}, "metric_code"},
		{&persistence.MetricValueRT{}, "metric_code"},
		{&persistence.MetricValueDay{}, "metric_code"},
		{&persistence.MetricPeakStat{}, "metric_code"},
		{&persistence.DataCorrection{}, "metric_code"},
	} {
		if err := tx.Where(del.col+" IN ?", metricCodes).Delete(del.model).Error; err != nil {
			rollback(err)
			return
		}
	}

	// 告警记录(历史)
	if err := tx.Where("system_code = ?", code).Delete(&persistence.AlertRecord{}).Error; err != nil {
		rollback(err)
		return
	}

	// 指标(先解除组内父子引用, parent_code 为 RESTRICT)
	if len(metricCodes) > 0 {
		if err := tx.Model(&persistence.CapacityMetric{}).
			Where("parent_code IN ?", metricCodes).
			Update("parent_code", nil).Error; err != nil {
			rollback(err)
			return
		}
	}
	if err := tx.Where("system_code = ?", code).Delete(&persistence.CapacityMetric{}).Error; err != nil {
		rollback(err)
		return
	}

	// 数据源 → 数据源分类(分类被数据源 RESTRICT 引用, 顺序不可颠倒)
	if len(dsIDs) > 0 {
		if err := tx.Where("id IN ?", dsIDs).Delete(&persistence.DataSource{}).Error; err != nil {
			rollback(err)
			return
		}
	}
	if len(catIDs) > 0 {
		if err := tx.Where("id IN ?", catIDs).Delete(&persistence.DataSourceCategory{}).Error; err != nil {
			rollback(err)
			return
		}
	}

	// 资源信息 + 数据权限授权
	if err := tx.Where("system_code = ?", code).Delete(&persistence.BusinessSystemResource{}).Error; err != nil {
		rollback(err)
		return
	}
	if err := tx.Where("system_code = ?", code).Delete(&persistence.SysDataScope{}).Error; err != nil {
		rollback(err)
		return
	}

	if err := tx.Delete(&s).Error; err != nil {
		rollback(err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "system", "system.delete", code,
		"metrics="+itoa(len(metricCodes))+" datasources="+itoa(len(dsIDs))+" by="+un)
	resp.OK(c, nil)
}

func (h *Handler) ListResources(c *gin.Context) {
	var items []persistence.BusinessSystemResource
	_ = h.db.Where("system_code = ?", c.Param("code")).Order("id").Find(&items).Error
	resp.OK(c, items)
}

func (h *Handler) SaveResources(c *gin.Context) {
	var req struct {
		Items []persistence.BusinessSystemResource `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	code := c.Param("code")
	tx := h.db.Begin()
	_ = tx.Where("system_code = ?", code).Delete(&persistence.BusinessSystemResource{}).Error
	for i := range req.Items {
		req.Items[i].SystemCode = code
		_ = tx.Create(&req.Items[i]).Error
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, nil)
}

// Export 系统清单导出 Excel（FR-SYS-07, P2 提前实现）
func (h *Handler) Export(c *gin.Context) {
	var items []persistence.BusinessSystem
	q := h.db.Model(&persistence.BusinessSystem{})
	if kw := c.Query("keyword"); kw != "" {
		q = q.Where("name LIKE ? OR system_code LIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	_ = q.Order("system_code").Find(&items).Error
	f := excelize.NewFile()
	sheet := "系统清单"
	f.SetSheetName(f.GetSheetName(0), sheet)
	headers := []string{"系统编码", "系统名称", "部门", "负责人", "级别", "状态", "备注"}
	for i, htxt := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, htxt)
	}
	for r, s := range items {
		row := r + 2
		f.SetCellValue(sheet, cellName(1, row), s.SystemCode)
		f.SetCellValue(sheet, cellName(2, row), s.Name)
		f.SetCellValue(sheet, cellName(3, row), s.Dept)
		f.SetCellValue(sheet, cellName(4, row), s.Owner)
		f.SetCellValue(sheet, cellName(5, row), s.Level)
		f.SetCellValue(sheet, cellName(6, row), s.Status)
		f.SetCellValue(sheet, cellName(7, row), s.Remark)
	}
	middleware.Log(c, "system", "system.export", "", "rows="+itoa(len(items)))
	c.Header("Content-Disposition", `attachment; filename="systems.xlsx"`)
	c.Writer.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_ = f.Write(c.Writer)
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
