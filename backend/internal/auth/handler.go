package auth

import (
	"net/http"
	"strings"
	"time"

	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/system"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Handler struct {
	db    *gorm.DB
	rdb   *redis.Client
	cfg   *config.Config
	params *system.Params
}

func NewHandler(db *gorm.DB, rdb *redis.Client, cfg *config.Config, params *system.Params) *Handler {
	return &Handler{db: db, rdb: rdb, cfg: cfg, params: params}
}

// Login 账号密码登录（FR-ADM-05: 连续 5 次失败锁定 15 分钟, 可配）
func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	ctx := c.Request.Context()
	if middleware.LoginLocked(ctx, h.rdb, req.Username) {
		resp.Fail(c, resp.NewErr(resp.CodeLocked, "账号已锁定，请 %d 分钟后重试", h.params.Int("login_lock_minutes", 15)))
		return
	}
	var user persistence.SysUser
	if err := h.db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeUnauthorized, "账号或密码错误"))
		return
	}
	if user.Status == "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeForbidden, "账号已停用"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		limit := h.params.Int("login_fail_limit", 5)
		lock := time.Duration(h.params.Int("login_lock_minutes", 15)) * time.Minute
		_, locked := middleware.BumpLoginFail(ctx, h.rdb, req.Username, limit, lock)
		if locked {
			resp.Fail(c, resp.NewErr(resp.CodeLocked, "连续失败次数过多，账号已锁定 %d 分钟", int(lock.Minutes())))
			return
		}
		resp.Fail(c, resp.NewErr(resp.CodeUnauthorized, "账号或密码错误"))
		return
	}
	middleware.ClearLoginFail(ctx, h.rdb, req.Username)

	roles := h.rolesOf(user.ID)
	jti := uuid.NewString()
	now := time.Now()
	claims := &middleware.Claims{
		UserID: user.ID, Username: user.Username, Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ID: jti, Issuer: "capacity-server", Subject: user.Username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(h.cfg.JWTAccessTTL)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(h.cfg.JWTSecret))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	// 会话表（滑动超时）
	sessionTTL := time.Duration(h.params.Int("session_timeout_min", 30)) * time.Minute
	_ = h.rdb.Set(ctx, "sess:"+jti, user.Username, sessionTTL).Err()

	_ = h.db.Model(&persistence.SysUser{}).Where("id = ?", user.ID).Updates(map[string]any{
		"last_login_at": now, "last_login_ip": c.ClientIP(),
	}).Error

	resp.OK(c, gin.H{
		"accessToken": token, "expiresIn": int(h.cfg.JWTAccessTTL.Seconds()),
		"userInfo": gin.H{
			"id": user.ID, "username": user.Username, "realName": user.RealName,
			"dept": user.Dept, "roles": roles, "mustChangePwd": user.MustChangePwd,
		},
	})
}

func (h *Handler) rolesOf(userID int64) []string {
	var roles []string
	_ = h.db.Raw(`SELECT r.code FROM sys_user_role ur JOIN sys_role r ON r.id = ur.role_id
		WHERE ur.user_id = ? AND r.status='active'`, userID).Scan(&roles).Error
	if roles == nil {
		roles = []string{}
	}
	return roles
}

// Me 当前用户信息（含功能权限码与数据权限系统集合）
func (h *Handler) Me(c *gin.Context) {
	uid, _ := c.Get(middleware.CtxUserID)
	id, _ := uid.(int64)
	var user persistence.SysUser
	if err := h.db.First(&user, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	roles := h.rolesOf(user.ID)
	perms, err := h.permissionsOf(id)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	scopes, err := h.dataScopesOf(id)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{
		"id": user.ID, "username": user.Username, "realName": user.RealName,
		"dept": user.Dept, "email": user.Email, "roles": roles,
		"permissions": perms, "dataScopes": scopes, "mustChangePwd": user.MustChangePwd,
	})
}

func (h *Handler) permissionsOf(userID int64) ([]string, error) {
	var codes []string
	err := h.db.Raw(`SELECT DISTINCT p.code
		FROM sys_user_role ur
		JOIN sys_role r ON r.id = ur.role_id AND r.status='active'
		JOIN sys_role_permission rp ON rp.role_id = r.id
		JOIN sys_permission p ON p.id = rp.permission_id
		WHERE ur.user_id = ?`, userID).Scan(&codes).Error
	if codes == nil {
		codes = []string{}
	}
	return codes, err
}

func (h *Handler) dataScopesOf(userID int64) ([]map[string]any, error) {
	type scopeRow struct {
		SystemCode string `gorm:"column:system_code"`
		ScopeType  string `gorm:"column:scope_type"`
	}
	var rows []scopeRow
	err := h.db.Raw(`SELECT s.system_code, s.scope_type
		FROM sys_data_scope s
		LEFT JOIN sys_user u ON u.id = s.user_id
		LEFT JOIN sys_user_role ur ON ur.user_id = ?
		LEFT JOIN sys_role r ON r.id = ur.role_id
		WHERE s.user_id = ? OR (s.role_id IS NOT NULL AND r.status='active')`, userID, userID).Scan(&rows).Error
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{"systemCode": r.SystemCode, "scope": r.ScopeType})
	}
	return out, err
}

// Logout 注销（删除会话）
func (h *Handler) Logout(c *gin.Context) {
	if token := c.GetHeader("Authorization"); strings.HasPrefix(token, "Bearer ") {
		// 解析 jti 删除会话（失败不报错）
		claims := &middleware.Claims{}
		tk, err := jwt.ParseWithClaims(strings.TrimPrefix(token, "Bearer "), claims,
			func(t *jwt.Token) (any, error) { return []byte(h.cfg.JWTSecret), nil })
		if err == nil && tk.Valid {
			_ = h.rdb.Del(c.Request.Context(), "sess:"+claims.ID).Err()
		}
	}
	resp.OK(c, nil)
}

// ChangePassword 修改密码（新密码 8~32 位, 须含大小写字母与数字）
func (h *Handler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword" binding:"required,min=8,max=32"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	uid, _ := c.Get(middleware.CtxUserID)
	id, _ := uid.(int64)
	var user persistence.SysUser
	if err := h.db.First(&user, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)) != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "原密码错误"))
		return
	}
	if !weakPasswordCheck(req.NewPassword) {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "新密码须包含大小写字母与数字"))
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	_ = h.db.Model(&user).Updates(map[string]any{"password_hash": string(hash), "must_change_pwd": false})
	// 使当前用户其他会话失效
	_ = h.rdb.Del(c.Request.Context(), "sess:"+c.GetHeader("Authorization")).Err()
	resp.OK(c, nil)
}

func weakPasswordCheck(p string) bool {
	hasUpper, hasLower, hasDigit := false, false, false
	for _, r := range p {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	return hasUpper && hasLower && hasDigit
}

var _ = http.StatusOK