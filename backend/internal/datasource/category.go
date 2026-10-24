package datasource

import (
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

// ---------- 数据源分类 CRUD（分类必须归属业务系统, 系统内名称唯一） ----------

type catReq struct {
	SystemCode string `json:"systemCode" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Remark     string `json:"remark"`
	Sort       int    `json:"sort"`
}

// ListCategories 分类列表（可按系统筛选）
func (h *Handler) ListCategories(c *gin.Context) {
	q := h.db.Model(&persistence.DataSourceCategory{})
	if v := c.Query("systemCode"); v != "" {
		q = q.Where("system_code = ?", v)
	}
	var items []persistence.DataSourceCategory
	_ = q.Order("system_code, sort, id").Find(&items).Error
	resp.OK(c, items)
}

// CreateCategory 新建分类（须选择对应系统）
func (h *Handler) CreateCategory(c *gin.Context) {
	var req catReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if !systemExists(h.db, req.SystemCode) {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "业务系统不存在: %s", req.SystemCode))
		return
	}
	var n int64
	_ = h.db.Model(&persistence.DataSourceCategory{}).
		Where("system_code = ? AND name = ?", req.SystemCode, req.Name).Count(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该系统下已存在同名分类"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	cat := persistence.DataSourceCategory{
		SystemCode: req.SystemCode, Name: req.Name, Remark: req.Remark,
		Sort: req.Sort, CreatedBy: un,
	}
	if err := h.db.Create(&cat).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "datasource", "ds.category.create", req.SystemCode+"/"+req.Name, "")
	resp.OK(c, cat)
}

// UpdateCategory 编辑分类（系统归属不可变更）
func (h *Handler) UpdateCategory(c *gin.Context) {
	var req struct {
		Name   string `json:"name" binding:"required"`
		Remark string `json:"remark"`
		Sort   *int   `json:"sort"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	var cat persistence.DataSourceCategory
	if err := h.db.First(&cat, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "分类不存在"))
		return
	}
	if req.Name != cat.Name {
		var n int64
		_ = h.db.Model(&persistence.DataSourceCategory{}).
			Where("system_code = ? AND name = ? AND id <> ?", cat.SystemCode, req.Name, cat.ID).Count(&n).Error
		if n > 0 {
			resp.Fail(c, resp.NewErr(resp.CodeConflict, "该系统下已存在同名分类"))
			return
		}
	}
	updates := map[string]any{"name": req.Name, "remark": req.Remark}
	if req.Sort != nil {
		updates["sort"] = *req.Sort
	}
	if err := h.db.Model(&cat).Updates(updates).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "datasource", "ds.category.update", cat.SystemCode+"/"+req.Name, "")
	resp.OK(c, nil)
}

// DeleteCategory 删除分类（仍有数据源归属时禁止）
func (h *Handler) DeleteCategory(c *gin.Context) {
	var cat persistence.DataSourceCategory
	if err := h.db.First(&cat, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "分类不存在"))
		return
	}
	var n int64
	_ = h.db.Model(&persistence.DataSource{}).Where("category_id = ?", cat.ID).Count(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该分类下仍有 %d 个数据源, 请先调整归属后再删除", n))
		return
	}
	if err := h.db.Delete(&cat).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "datasource", "ds.category.delete", cat.SystemCode+"/"+cat.Name, "")
	resp.OK(c, nil)
}
