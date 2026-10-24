package system

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Handler struct {
	db          *gorm.DB
	params      *Params
	az          *middleware.AuthZ
	log         *slog.Logger
	healthExtra func(c *gin.Context) gin.H
}

func NewHandler(db *gorm.DB, params *Params, az *middleware.AuthZ, log *slog.Logger) *Handler {
	return &Handler{db: db, params: params, az: az, log: log}
}

// ---------- 用户 ----------

func (h *Handler) ListUsers(c *gin.Context) {
	page, size := pageOf(c)
	q := h.db.Model(&persistence.SysUser{})
	if kw := c.Query("keyword"); kw != "" {
		q = q.Where("username LIKE ? OR real_name LIKE ?", "%"+kw+"%", "%"+kw+"%")
	}
	var total int64
	_ = q.Count(&total).Error
	var users []persistence.SysUser
	_ = q.Order("id").Offset((page - 1) * size).Limit(size).Find(&users).Error
	resp.OK(c, resp.Page(users, total, page, size))
}

func (h *Handler) CreateUser(c *gin.Context) {
	var req struct {
		Username  string  `json:"username" binding:"required,min=3,max=64"`
		RealName  string  `json:"realName" binding:"required"`
		Password  string  `json:"password" binding:"required,min=8,max=32"`
		Dept      string  `json:"dept"`
		Email     string  `json:"email"`
		Phone     string  `json:"phone"`
		RoleCodes []string `json:"roleCodes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	var n int64
	_ = h.db.Model(&persistence.SysUser{}).Where("username = ?", req.Username).Count(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "用户名已存在"))
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	user := persistence.SysUser{
		Username: req.Username, PasswordHash: string(hash), RealName: req.RealName,
		Dept: req.Dept, Email: req.Email, Phone: req.Phone, Status: "active", MustChangePwd: false,
	}
	tx := h.db.Begin()
	if err := tx.Create(&user).Error; err != nil {
		tx.Rollback()
		resp.Fail(c, err)
		return
	}
	for _, rc := range req.RoleCodes {
		var role persistence.SysRole
		if err := tx.Where("code = ?", rc).First(&role).Error; err == nil {
			_ = tx.Create(&persistence.SysUserRole{UserID: user.ID, RoleID: role.ID}).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	_ = h.az.Reload()
	middleware.Log(c, "system", "user.create", req.Username, "")
	resp.OK(c, user)
}

func (h *Handler) UpdateUser(c *gin.Context) {
	var req struct {
		RealName string  `json:"realName"`
		Dept     string  `json:"dept"`
		Email    string  `json:"email"`
		Phone    string  `json:"phone"`
		Status   string  `json:"status" binding:"omitempty,oneof=active disabled"`
		Disabled *bool   `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	id := c.Param("id")
	var user persistence.SysUser
	if err := h.db.First(&user, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "用户不存在"))
		return
	}
	updates := map[string]any{}
	if req.RealName != "" {
		updates["real_name"] = req.RealName
	}
	if req.Dept != "" {
		updates["dept"] = req.Dept
	}
	if req.Email != "" {
		updates["email"] = req.Email
	}
	if req.Phone != "" {
		updates["phone"] = req.Phone
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Disabled != nil {
		if *req.Disabled {
			updates["status"] = "disabled"
		} else {
			updates["status"] = "active"
		}
	}
	if len(updates) > 0 {
		if err := h.db.Model(&user).Updates(updates).Error; err != nil {
			resp.Fail(c, err)
			return
		}
	}
	middleware.Log(c, "system", "user.update", user.Username, "")
	resp.OK(c, user)
}

func (h *Handler) ResetPassword(c *gin.Context) {
	var req struct {
		Password string `json:"password" binding:"required,min=8,max=32"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	id := c.Param("id")
	var user persistence.SysUser
	if err := h.db.First(&user, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "用户不存在"))
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	_ = h.db.Model(&user).Updates(map[string]any{"password_hash": string(hash), "must_change_pwd": true})
	middleware.Log(c, "system", "user.reset_password", user.Username, "")
	resp.OK(c, nil)
}

func (h *Handler) SetUserRoles(c *gin.Context) {
	var req struct {
		RoleCodes []string `json:"roleCodes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	id := c.Param("id")
	var user persistence.SysUser
	if err := h.db.First(&user, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "用户不存在"))
		return
	}
	tx := h.db.Begin()
	if err := tx.Where("user_id = ?", user.ID).Delete(&persistence.SysUserRole{}).Error; err != nil {
		tx.Rollback()
		resp.Fail(c, err)
		return
	}
	for _, rc := range req.RoleCodes {
		var role persistence.SysRole
		if err := tx.Where("code = ?", rc).First(&role).Error; err == nil {
			_ = tx.Create(&persistence.SysUserRole{UserID: user.ID, RoleID: role.ID}).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	_ = h.az.Reload()
	middleware.Log(c, "system", "user.roles", user.Username, "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteUser(c *gin.Context) {
	id := c.Param("id")
	var user persistence.SysUser
	if err := h.db.First(&user, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "用户不存在"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	if user.Username == un {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "不能删除当前登录用户"))
		return
	}
	if user.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅已停用的用户可删除"))
		return
	}
	var n int64
	_ = h.db.Raw(`SELECT count(*) FROM sys_user_role ur
		JOIN sys_role r ON r.id = ur.role_id
		WHERE ur.user_id = ? AND r.code = 'platform_admin'`, user.ID).Scan(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该用户持有平台管理员角色，请先移除角色"))
		return
	}
	if err := h.db.Delete(&user).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	_ = h.az.Reload()
	middleware.Log(c, "system", "user.delete", user.Username, "")
	resp.OK(c, nil)
}

// ---------- 角色 ----------

func (h *Handler) ListRoles(c *gin.Context) {
	var roles []persistence.SysRole
	_ = h.db.Order("id").Find(&roles).Error
	type roleView struct {
		persistence.SysRole
		Permissions []string `json:"permissions"`
		UserCount   int      `json:"userCount"`
	}
	out := make([]roleView, 0, len(roles))
	for _, r := range roles {
		var codes []string
		_ = h.db.Raw(`SELECT p.code FROM sys_role_permission rp
			JOIN sys_permission p ON p.id = rp.permission_id WHERE rp.role_id = ?`, r.ID).Scan(&codes).Error
		if codes == nil {
			codes = []string{}
		}
		var n int64
		_ = h.db.Raw(`SELECT count(*) FROM sys_user_role ur
			JOIN sys_user u ON u.id = ur.user_id
			WHERE ur.role_id = ? AND u.status = 'active'`, r.ID).Scan(&n).Error
		out = append(out, roleView{SysRole: r, Permissions: codes, UserCount: int(n)})
	}
	resp.OK(c, out)
}

func (h *Handler) CreateRole(c *gin.Context) {
	var req struct {
		Code        string   `json:"code" binding:"required,min=2,max=64"`
		Name        string   `json:"name" binding:"required"`
		Remark      string   `json:"remark"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	var n int64
	_ = h.db.Model(&persistence.SysRole{}).Where("code = ?", req.Code).Count(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "角色编码已存在"))
		return
	}
	role := persistence.SysRole{Code: req.Code, Name: req.Name, Builtin: false, Remark: req.Remark}
	tx := h.db.Begin()
	if err := tx.Create(&role).Error; err != nil {
		tx.Rollback()
		resp.Fail(c, err)
		return
	}
	for _, pc := range req.Permissions {
		var perm persistence.SysPermission
		if err := tx.Where("code = ?", pc).First(&perm).Error; err == nil {
			_ = tx.Create(&persistence.SysRolePermission{RoleID: role.ID, PermissionID: perm.ID}).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	_ = h.az.Reload()
	middleware.Log(c, "system", "role.create", req.Code, "")
	resp.OK(c, role)
}

func (h *Handler) UpdateRole(c *gin.Context) {
	var req struct {
		Name        string   `json:"name"`
		Remark      string   `json:"remark"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	id := c.Param("id")
	var role persistence.SysRole
	if err := h.db.First(&role, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "角色不存在"))
		return
	}
	if role.Builtin && req.Permissions != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "内置角色权限不可修改"))
		return
	}
	tx := h.db.Begin()
	if req.Name != "" {
		_ = tx.Model(&role).Update("name", req.Name).Error
	}
	if req.Remark != "" {
		_ = tx.Model(&role).Update("remark", req.Remark).Error
	}
	if req.Permissions != nil {
		_ = tx.Where("role_id = ?", role.ID).Delete(&persistence.SysRolePermission{}).Error
		for _, pc := range req.Permissions {
			var perm persistence.SysPermission
			if err := tx.Where("code = ?", pc).First(&perm).Error; err == nil {
				_ = tx.Create(&persistence.SysRolePermission{RoleID: role.ID, PermissionID: perm.ID}).Error
			}
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	_ = h.az.Reload()
	middleware.Log(c, "system", "role.update", role.Code, "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteRole(c *gin.Context) {
	id := c.Param("id")
	var role persistence.SysRole
	if err := h.db.First(&role, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "角色不存在"))
		return
	}
	if role.Builtin {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "内置角色不可删除"))
		return
	}
	var n int64
	_ = h.db.Raw(`SELECT count(*) FROM sys_user_role ur
		JOIN sys_user u ON u.id = ur.user_id
		WHERE ur.role_id = ? AND u.status = 'active'`, role.ID).Scan(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该角色仍分配给 %d 个启用用户，请先解除分配", n))
		return
	}
	if err := h.db.Delete(&role).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	_ = h.az.Reload()
	middleware.Log(c, "system", "role.delete", role.Code, "")
	resp.OK(c, nil)
}

// ---------- 权限/数据权限/参数/日志 ----------

func (h *Handler) ListPermissions(c *gin.Context) {
	var perms []persistence.SysPermission
	_ = h.db.Order("sort, id").Find(&perms).Error
	resp.OK(c, perms)
}

func (h *Handler) GetDataScope(c *gin.Context) {
	var rows []persistence.SysDataScope
	_ = h.db.Order("id").Find(&rows).Error
	type scopeView struct {
		persistence.SysDataScope
		Username string `json:"username"`
		RoleName string `json:"roleName"`
	}
	var uids, rids []int64
	for _, r := range rows {
		if r.UserID != nil {
			uids = append(uids, *r.UserID)
		}
		if r.RoleID != nil {
			rids = append(rids, *r.RoleID)
		}
	}
	unames := map[int64]string{}
	if len(uids) > 0 {
		var us []persistence.SysUser
		_ = h.db.Where("id IN ?", uids).Find(&us).Error
		for _, u := range us {
			unames[u.ID] = u.Username
		}
	}
	rnames := map[int64]string{}
	if len(rids) > 0 {
		var rs []persistence.SysRole
		_ = h.db.Where("id IN ?", rids).Find(&rs).Error
		for _, r := range rs {
			rnames[r.ID] = r.Name
		}
	}
	out := make([]scopeView, 0, len(rows))
	for _, r := range rows {
		v := scopeView{SysDataScope: r}
		if r.UserID != nil {
			v.Username = unames[*r.UserID]
		}
		if r.RoleID != nil {
			v.RoleName = rnames[*r.RoleID]
		}
		out = append(out, v)
	}
	resp.OK(c, out)
}

func (h *Handler) PutDataScope(c *gin.Context) {
	var req struct {
		UserID  *int64 `json:"userId"`
		RoleID  *int64 `json:"roleId"`
		Systems []struct {
			Code    string `json:"systemCode"`
			Scope   string `json:"scope"` // manage/view
		} `json:"systems" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if req.UserID == nil && req.RoleID == nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "userId/roleId 至少一个"))
		return
	}
	tx := h.db.Begin()
	if req.UserID != nil {
		_ = tx.Where("user_id = ?", *req.UserID).Delete(&persistence.SysDataScope{}).Error
	} else {
		_ = tx.Where("role_id = ?", *req.RoleID).Delete(&persistence.SysDataScope{}).Error
	}
	for _, s := range req.Systems {
		scope := "view"
		if s.Scope == "manage" {
			scope = "manage"
		}
		_ = tx.Create(&persistence.SysDataScope{UserID: req.UserID, RoleID: req.RoleID, SystemCode: s.Code, ScopeType: scope}).Error
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "system", "datascope.put", "", "")
	resp.OK(c, nil)
}

func (h *Handler) DeleteDataScope(c *gin.Context) {
	id := c.Param("id")
	var row persistence.SysDataScope
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "授权记录不存在"))
		return
	}
	subject := ""
	if row.UserID != nil {
		subject = fmt.Sprintf("用户#%d", *row.UserID)
	} else if row.RoleID != nil {
		subject = fmt.Sprintf("角色#%d", *row.RoleID)
	}
	if err := h.db.Delete(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "system", "datascope.delete", subject+" "+row.SystemCode, "")
	resp.OK(c, nil)
}

func (h *Handler) ListParams(c *gin.Context) {
	var rows []persistence.SysParam
	_ = h.db.Where("category <> 'ai'").Order("category, key").Find(&rows).Error
	resp.OK(c, rows)
}

func (h *Handler) PutParam(c *gin.Context) {
	var req struct {
		Key   string `json:"key" binding:"required"`
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if strings.HasPrefix(req.Key, "ai.") {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "AI 配置项请通过「AI 配置」页签修改"))
		return
	}
	var p persistence.SysParam
	if err := h.db.Where("key = ?", req.Key).First(&p).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "参数不存在: %s", req.Key))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	_ = h.db.Model(&p).Updates(map[string]any{"value": req.Value, "updated_by": un, "updated_at": time.Now()}).Error
	h.params.Invalidate()
	middleware.Log(c, "system", "param.put", req.Key, "new="+req.Value)
	resp.OK(c, nil)
}

func (h *Handler) ListOperateLogs(c *gin.Context) {
	page, size := pageOf(c)
	q := h.db.Model(&persistence.SysOperateLog{})
	if u := c.Query("username"); u != "" {
		q = q.Where("username = ?", u)
	}
	if m := c.Query("module"); m != "" {
		q = q.Where("module = ?", m)
	}
	if f := c.Query("from"); f != "" {
		if t, err := time.Parse("2006-01-02", f); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if t := c.Query("to"); t != "" {
		if tt, err := time.Parse("2006-01-02", t); err == nil {
			q = q.Where("created_at < ?", tt.AddDate(0, 0, 1))
		}
	}
	var total int64
	_ = q.Count(&total).Error
	var logs []persistence.SysOperateLog
	_ = q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&logs).Error
	resp.OK(c, resp.Page(logs, total, page, size))
}

// Health 平台自检（FR-ADM-08 基础版）
// SetHealthProvider injects extended platform self-check data (scheduler/Worker/storage/daily summary, FR-ADM-08 basic version)
func (h *Handler) SetHealthProvider(f func(c *gin.Context) gin.H) { h.healthExtra = f }

func (h *Handler) Health(c *gin.Context) {
	var schedRunning int64
	_ = h.db.Model(&struct{}{}).Table("collect_task_log").
		Where("status = 'running'").Count(&schedRunning)
	var dsTotal, dsAbnormal int64
	_ = h.db.Model(&persistence.DataSource{}).Count(&dsTotal)
	_ = h.db.Model(&persistence.DataSource{}).Where("status = 'abnormal'").Count(&dsAbnormal)
	var lastSummary persistence.DailySummaryLog
	_ = h.db.Order("biz_date DESC").First(&lastSummary).Error
	var partCount int64
	_ = h.db.Raw(`SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND (c.relname LIKE 'metric_value_rt_y%m%' OR c.relname LIKE 'metric_value_day_y%m%')`).Scan(&partCount).Error
	out := gin.H{
		"runningTasks":     schedRunning,
		"dataSources":      gin.H{"total": dsTotal, "abnormal": dsAbnormal},
		"lastDailySummary": lastSummary,
		"partitions":       partCount,
		"time":             time.Now().Format(time.RFC3339),
	}
	if h.healthExtra != nil {
		for k, v := range h.healthExtra(c) {
			out[k] = v
		}
	}
	resp.OK(c, out)
}

func pageOf(c *gin.Context) (int, int) {
	page := 1
	size := 20
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	return page, size
}
