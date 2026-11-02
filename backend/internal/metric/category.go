package metric

import (
	"strings"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pkg/scope"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const DefaultMetricCategoryName = "默认分类"

const defaultMetricCategoryName = DefaultMetricCategoryName

type metricCategoryReq struct {
	SystemCode string `json:"systemCode" binding:"required"`
	Name       string `json:"name" binding:"required,max=32"`
	Remark     string `json:"remark" binding:"max=512"`
	Sort       int    `json:"sort"`
}

func (h *Handler) ensureDefaultMetricCategory(systemCode string) error {
	cat := persistence.MetricCategory{SystemCode: systemCode, Name: defaultMetricCategoryName}
	return h.db.Where("system_code = ? AND name = ?", systemCode, defaultMetricCategoryName).
		FirstOrCreate(&cat).Error
}

func (h *Handler) requireMetricCategory(systemCode, name string) (string, error) {
	return ResolveMetricCategory(h.db, systemCode, name)
}

func (h *Handler) hasSystemScope(c *gin.Context, systemCode string) bool {
	visible := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c))
	return scope.Contains(visible, systemCode)
}

// ResolveMetricCategory 返回该系统下可用的分类名称，空分类归入默认分类。
func ResolveMetricCategory(db *gorm.DB, systemCode, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultMetricCategoryName
	}
	if name == DefaultMetricCategoryName {
		cat := persistence.MetricCategory{SystemCode: systemCode, Name: DefaultMetricCategoryName}
		if err := db.Where("system_code = ? AND name = ?", systemCode, DefaultMetricCategoryName).
			FirstOrCreate(&cat).Error; err != nil {
			return "", err
		}
		return name, nil
	}
	var n int64
	if err := db.Model(&persistence.MetricCategory{}).
		Where("system_code = ? AND name = ?", systemCode, name).Count(&n).Error; err != nil {
		return "", err
	}
	if n == 0 {
		return "", resp.NewErr(resp.CodeBadRequest, "指标分类不存在: %s", name)
	}
	return name, nil
}

// ListCategories 指标分类列表（可按业务系统筛选）。
func (h *Handler) ListCategories(c *gin.Context) {
	q := h.db.Model(&persistence.MetricCategory{})
	systemCode := strings.TrimSpace(c.Query("systemCode"))
	if systemCode != "" {
		visible := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c))
		if !scope.Contains(visible, systemCode) {
			scope.Forbidden(c, systemCode)
			return
		}
		if err := h.ensureDefaultMetricCategory(systemCode); err != nil {
			resp.Fail(c, err)
			return
		}
		q = q.Where("system_code = ?", systemCode)
	} else {
		visible := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c))
		if visible != nil && len(visible) == 0 {
			resp.OK(c, []persistence.MetricCategory{})
			return
		}
		if visible != nil {
			q = q.Where("system_code IN ?", visible)
		}
	}
	var items []persistence.MetricCategory
	if err := q.Order("system_code, sort, id").Find(&items).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, items)
}

// CreateCategory 新建指标分类（须选择对应业务系统）。
func (h *Handler) CreateCategory(c *gin.Context) {
	var req metricCategoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	req.SystemCode = strings.TrimSpace(req.SystemCode)
	if !h.hasSystemScope(c, req.SystemCode) {
		scope.Forbidden(c, req.SystemCode)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "分类名称不能为空"))
		return
	}
	var systemN int64
	if err := h.db.Model(&persistence.BusinessSystem{}).
		Where("system_code = ?", req.SystemCode).Count(&systemN).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if systemN == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "业务系统不存在: %s", req.SystemCode))
		return
	}
	var n int64
	if err := h.db.Model(&persistence.MetricCategory{}).
		Where("system_code = ? AND name = ?", req.SystemCode, req.Name).Count(&n).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该系统下已存在同名指标分类"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	cat := persistence.MetricCategory{
		SystemCode: req.SystemCode, Name: req.Name, Remark: req.Remark,
		Sort: req.Sort, CreatedBy: un,
	}
	if err := h.db.Create(&cat).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "创建失败(该系统下名称已存在?)"))
		return
	}
	middleware.Log(c, "metric", "metric.category.create", req.SystemCode+"/"+req.Name, "")
	resp.OK(c, cat)
}

// UpdateCategory 编辑指标分类；改名时同步更新该系统内全部指标。
func (h *Handler) UpdateCategory(c *gin.Context) {
	var req struct {
		Name   string `json:"name" binding:"required,max=32"`
		Remark string `json:"remark" binding:"max=512"`
		Sort   *int   `json:"sort"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "分类名称不能为空"))
		return
	}
	var cat persistence.MetricCategory
	if err := h.db.First(&cat, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标分类不存在"))
		return
	}
	if !h.hasSystemScope(c, cat.SystemCode) {
		scope.Forbidden(c, cat.SystemCode)
		return
	}
	oldName := cat.Name
	if oldName == defaultMetricCategoryName && req.Name != oldName {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "默认分类不可重命名"))
		return
	}
	if req.Name != oldName {
		var n int64
		if err := h.db.Model(&persistence.MetricCategory{}).
			Where("system_code = ? AND name = ? AND id <> ?", cat.SystemCode, req.Name, cat.ID).Count(&n).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		if n > 0 {
			resp.Fail(c, resp.NewErr(resp.CodeConflict, "该系统下已存在同名指标分类"))
			return
		}
	}
	updates := map[string]any{"name": req.Name, "remark": req.Remark}
	if req.Sort != nil {
		updates["sort"] = *req.Sort
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&cat).Updates(updates).Error; err != nil {
			return err
		}
		if req.Name == oldName {
			return nil
		}
		return tx.Model(&persistence.CapacityMetric{}).
			Where("system_code = ? AND category = ?", cat.SystemCode, oldName).
			Update("category", req.Name).Error
	}); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "metric.category.update", cat.SystemCode+"/"+req.Name, "")
	resp.OK(c, nil)
}

// DeleteCategory 删除指标分类（仍有指标归属时禁止）。
func (h *Handler) DeleteCategory(c *gin.Context) {
	var cat persistence.MetricCategory
	if err := h.db.First(&cat, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标分类不存在"))
		return
	}
	if !h.hasSystemScope(c, cat.SystemCode) {
		scope.Forbidden(c, cat.SystemCode)
		return
	}
	if cat.Name == defaultMetricCategoryName {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "默认分类不可删除"))
		return
	}
	var n int64
	if err := h.db.Model(&persistence.CapacityMetric{}).
		Where("system_code = ? AND category = ?", cat.SystemCode, cat.Name).Count(&n).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该指标分类下仍有 %d 个指标, 请先调整归属后再删除", n))
		return
	}
	if err := h.db.Delete(&cat).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "metric.category.delete", cat.SystemCode+"/"+cat.Name, "")
	resp.OK(c, nil)
}
