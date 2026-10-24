// Package scope 数据权限（业务系统维度）查询工具（FR-ADM-03）
//
// 约定: 返回值 nil = 不限制（平台管理员）; 空切片 = 用户无任何数据权限
package scope

import (
	"context"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Username 从 gin 上下文取当前登录用户名
func Username(c *gin.Context) string {
	uname, _ := c.Get(middleware.CtxUsername)
	un, _ := uname.(string)
	return un
}

// Systems 用户可查看的业务系统编码（用户直接授权 + 有效角色授权）
func Systems(ctx context.Context, db *gorm.DB, az *middleware.AuthZ, username string) []string {
	az.EnsureFresh()
	if az.IsPlatformAdmin(username) {
		return nil
	}
	var codes []string
	_ = db.Table("sys_data_scope s").
		Joins("JOIN sys_user u ON u.id = s.user_id").
		Joins("LEFT JOIN sys_user_role ur ON ur.user_id = u.id").
		Joins("LEFT JOIN sys_role r ON r.id = ur.role_id").
		Where("u.username = ?", username).
		Where("s.user_id IS NOT NULL OR r.status = 'active'").
		Distinct().Pluck("s.system_code", &codes).Error
	return codes
}

// Apply 按 system_code 列限制查询: nil=不限制; 空=无结果
func Apply(q *gorm.DB, codes []string) *gorm.DB {
	if codes == nil {
		return q
	}
	if len(codes) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where("system_code IN ?", codes)
}

// Contains 系统编码是否在可见集合内（codes=nil 视为全部可见）
func Contains(codes []string, systemCode string) bool {
	if codes == nil {
		return true
	}
	for _, c := range codes {
		if c == systemCode {
			return true
		}
	}
	return false
}

// Forbidden 数据权限 403 响应
func Forbidden(c *gin.Context, target string) {
	c.AbortWithStatusJSON(403, resp.Body{Code: resp.CodeDataForbidden, Message: "无该业务系统数据权限: " + target})
}