package middleware

import (
	"sync"
	"time"

	"capacity-manage/backend/internal/pkg/resp"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RBAC: Casbin 策略 p, role:<code>, <permCode>；g, user:<name>, role:<code>
var defaultModelText = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

type AuthZ struct {
	mu      sync.RWMutex
	enforcer *casbin.Enforcer
	db      *gorm.DB
	last    time.Time
}

func NewAuthZ(db *gorm.DB) (*AuthZ, error) {
	a := &AuthZ{db: db}
	if err := a.Reload(); err != nil {
		return nil, err
	}
	return a, nil
}

// Reload 从 DB 重建策略（角色/权限变更后可调用；启动时调用）
func (a *AuthZ) Reload() error {
	m, err := model.NewModelFromString(defaultModelText)
	if err != nil {
		return err
	}
	e, err := casbin.NewEnforcer(m)
	if err != nil {
		return err
	}
	var gRows []struct {
		Username string
		RoleCode string
	}
	if err := a.db.Raw(`SELECT u.username, r.code AS role_code FROM sys_user_role ur
		JOIN sys_user u ON u.id = ur.user_id
		JOIN sys_role r ON r.id = ur.role_id WHERE r.status='active'`).Scan(&gRows).Error; err != nil {
		return err
	}
	for _, g := range gRows {
		if _, err := e.AddGroupingPolicy("user:"+g.Username, "role:"+g.RoleCode); err != nil {
			return err
		}
	}
	var pRows []struct {
		RoleCode string
		PermCode string
	}
	if err := a.db.Raw(`SELECT r.code AS role_code, p.code AS perm_code FROM sys_role_permission rp
		JOIN sys_role r ON r.id = rp.role_id
		JOIN sys_permission p ON p.id = rp.permission_id WHERE r.status='active'`).Scan(&pRows).Error; err != nil {
		return err
	}
	for _, p := range pRows {
		if _, err := e.AddPolicy("role:"+p.RoleCode, p.PermCode, "require"); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.enforcer = e
	a.last = time.Now()
	a.mu.Unlock()
	return nil
}

// EnsureFresh 策略超过 30s 自动重载（角色变更热生效）
func (a *AuthZ) EnsureFresh() {
	a.mu.RLock()
	stale := time.Since(a.last) > 30*time.Second
	a.mu.RUnlock()
	if stale {
		_ = a.Reload()
	}
}

func (a *AuthZ) Allow(username, permCode string) bool {
	a.mu.RLock()
	e := a.enforcer
	a.mu.RUnlock()
	if e == nil {
		return false
	}
	ok, err := e.Enforce("user:"+username, permCode, "require")
	return err == nil && ok
}

// RequirePerm 功能权限中间件：校验当前用户是否拥有 permCode
func (a *AuthZ) RequirePerm(permCode string) gin.HandlerFunc {
	return func(c *gin.Context) {
		username, _ := c.Get(CtxUsername)
		uname, _ := username.(string)
		a.EnsureFresh()
		if !a.Allow(uname, permCode) {
			c.AbortWithStatusJSON(403, resp.Body{Code: resp.CodeForbidden, Message: "无功能权限: " + permCode})
			return
		}
		c.Next()
	}
}

// IsPlatformAdmin 平台管理员拥有全部数据权限
func (a *AuthZ) IsPlatformAdmin(username string) bool {
	a.mu.RLock()
	e := a.enforcer
	a.mu.RUnlock()
	if e == nil {
		return false
	}
	roles, err := e.GetRolesForUser("user:" + username)
	if err != nil {
		return false
	}
	for _, r := range roles {
		if r == "role:platform_admin" {
			return true
		}
	}
	return false
}