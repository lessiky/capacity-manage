package flow

import (
	"regexp"
	"strings"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var flowVariableNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

var builtinVariableNames = map[string]struct{}{
	"biz_date":           {},
	"prev_trading_day":   {},
	"next_trading_day":   {},
	"trading_day_offset": {},
	"system_code":        {},
	"flow_code":          {},
	"metric_code":        {},
	"hour":               {},
}

func isBuiltinVariableName(name string) bool {
	_, ok := builtinVariableNames[name]
	return ok
}

func validateFlowVariableName(name string) error {
	if name == "" {
		return resp.NewErr(resp.CodeBadRequest, "变量名不能为空")
	}
	if len(name) > 64 {
		return resp.NewErr(resp.CodeBadRequest, "变量名长度不能超过 64")
	}
	if !flowVariableNamePattern.MatchString(name) {
		return resp.NewErr(resp.CodeBadRequest, "变量名仅支持大小写字母、数字和下划线，如 ClusterCode_2026")
	}
	if isBuiltinVariableName(name) {
		return resp.NewErr(resp.CodeBadRequest, "自定义变量名不能与内置变量 ${%s} 重名", name)
	}
	return nil
}

type flowVariableReq struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Value       string `json:"value"`
}

func (h *Handler) ListVariables(c *gin.Context) {
	if page, size, ok := resp.ParsePage(c, 20, 200); ok {
		var total int64
		if err := h.db.Model(&persistence.FlowVariable{}).Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var items []persistence.FlowVariable
		if err := h.db.Order("id").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, resp.Page(items, total, page, size))
		return
	}

	var items []persistence.FlowVariable
	if err := h.db.Order("id").Find(&items).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, items)
}

func (h *Handler) CreateVariable(c *gin.Context) {
	var req flowVariableReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := validateFlowVariableName(req.Name); err != nil {
		resp.Fail(c, err)
		return
	}
	var count int64
	_ = h.db.Model(&persistence.FlowVariable{}).Where("name = ?", req.Name).Count(&count).Error
	if count > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "变量名 %s 已存在", req.Name))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	row := persistence.FlowVariable{
		Name: req.Name, Description: strings.TrimSpace(req.Description), Value: req.Value, CreatedBy: un,
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "变量名 %s 已存在", req.Name))
		return
	}
	middleware.Log(c, "flow", "flow.variable.create", row.Name, "")
	resp.OK(c, row)
}

func (h *Handler) UpdateVariable(c *gin.Context) {
	var row persistence.FlowVariable
	if err := h.db.First(&row, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "变量不存在"))
		return
	}
	var req flowVariableReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := validateFlowVariableName(req.Name); err != nil {
		resp.Fail(c, err)
		return
	}
	var count int64
	_ = h.db.Model(&persistence.FlowVariable{}).
		Where("name = ? AND id <> ?", req.Name, row.ID).Count(&count).Error
	if count > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "变量名 %s 已存在", req.Name))
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"name": req.Name, "description": strings.TrimSpace(req.Description), "value": req.Value,
	}).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "变量名 %s 已存在", req.Name))
		return
	}
	middleware.Log(c, "flow", "flow.variable.update", req.Name, "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteVariable(c *gin.Context) {
	var row persistence.FlowVariable
	if err := h.db.First(&row, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "变量不存在"))
		return
	}
	if err := h.db.Delete(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "flow", "flow.variable.delete", row.Name, "")
	resp.OK(c, nil)
}

func loadPublicVariableValues(db *gorm.DB) (map[string]string, error) {
	var rows []persistence.FlowVariable
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Name] = row.Value
	}
	return out, nil
}

func mergePublicVariables(builtin, public map[string]string) map[string]string {
	out := make(map[string]string, len(builtin)+len(public))
	for k, v := range builtin {
		out[k] = v
	}
	for k, v := range public {
		if isBuiltinVariableName(k) {
			continue
		}
		out[k] = v
	}
	return out
}
