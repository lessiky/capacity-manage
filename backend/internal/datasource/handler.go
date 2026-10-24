package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/masking"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	cred   *Registry
	az     *middleware.AuthZ
	cfg    *config.Config
	log    *slog.Logger
}

func NewHandler(db *gorm.DB, cred *Registry, az *middleware.AuthZ, cfg *config.Config, log *slog.Logger) *Handler {
	return &Handler{db: db, cred: cred, az: az, cfg: cfg, log: log}
}

type dsReq struct {
	Name       string          `json:"name" binding:"required"`
	Type       string          `json:"type" binding:"required,oneof=db log ssh"`
	SystemCode string          `json:"systemCode" binding:"required"`
	CategoryID int64           `json:"categoryId" binding:"required"`
	Config     json.RawMessage `json:"config" binding:"required"`
}

// List 列表（凭据恒脱敏）
func (h *Handler) List(c *gin.Context) {
	q := h.db.Model(&persistence.DataSource{})
	if v := c.Query("type"); v != "" {
		q = q.Where("type = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("systemCode"); v != "" {
		q = q.Where("system_code = ?", v)
	}
	if v := c.Query("categoryId"); v != "" {
		q = q.Where("category_id = ?", v)
	}
	var items []persistence.DataSource
	_ = q.Order("id").Find(&items).Error
	for i := range items {
		items[i].Config = masking.RedactJSON(items[i].Config)
	}
	resp.OK(c, h.enrichDS(items))
}

// Get 详情（脱敏）
func (h *Handler) Get(c *gin.Context) {
	var src persistence.DataSource
	if err := h.db.First(&src, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "数据源不存在"))
		return
	}
	src.Config = masking.RedactJSON(src.Config)
	resp.OK(c, h.enrichDS([]persistence.DataSource{src})[0])
}

// Create 创建（密码 AES-256-GCM 加密存储）
func (h *Handler) Create(c *gin.Context) {
	var req dsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	var n int64
	_ = h.db.Model(&persistence.DataSource{}).Where("name = ?", req.Name).Count(&n).Error
	if n > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "数据源名称已存在"))
		return
	}
	cfg, err := h.normalizeConfig(req.Type, req.Config)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.validateSystemCategory(req.SystemCode, req.CategoryID); err != nil {
		resp.Fail(c, err)
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	src := persistence.DataSource{
		Name: req.Name, Type: req.Type, Status: "available",
		Config: string(cfg), CreatedBy: un,
		SystemCode: req.SystemCode, CategoryID: &req.CategoryID,
	}
	if err := h.db.Create(&src).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "datasource", "datasource.create", src.Name, "type="+src.Type)
	// 创建后自动执行一次连接测试（异步, 不阻塞响应）
	go h.runTest(&src)
	resp.OK(c, h.enrichDS([]persistence.DataSource{src})[0])
}

// Update 编辑（password 留空=不修改）
func (h *Handler) Update(c *gin.Context) {
	var req struct {
		Name       string          `json:"name"`
		SystemCode string          `json:"systemCode" binding:"required"`
		CategoryID int64           `json:"categoryId" binding:"required"`
		Config     json.RawMessage `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if err := h.validateSystemCategory(req.SystemCode, req.CategoryID); err != nil {
		resp.Fail(c, err)
		return
	}
	var src persistence.DataSource
	if err := h.db.First(&src, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "数据源不存在"))
		return
	}
	_ = h.db.Model(&src).Updates(map[string]any{
		"system_code": req.SystemCode, "category_id": req.CategoryID,
	}).Error
	src.SystemCode = req.SystemCode
	src.CategoryID = &req.CategoryID
	if req.Name != "" {
		src.Name = req.Name
		_ = h.db.Model(&src).Update("name", req.Name).Error
	}
	if len(req.Config) > 0 {
		merged, err := h.mergeConfig(&src, req.Config)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		src.Config = string(merged) // 同步内存, 保证后续健康测试用新配置
		_ = h.db.Model(&src).Update("config", string(merged)).Error
	}
	middleware.Log(c, "datasource", "datasource.update", src.Name, "")
	go h.runTest(&src)
	resp.OK(c, nil)
}

// Test 连接测试
func (h *Handler) Test(c *gin.Context) {
	var src persistence.DataSource
	if err := h.db.First(&src, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "数据源不存在"))
		return
	}
	result, _ := h.cred.Test(c.Request.Context(), &src)
	h.cred.UpdateHealth(&src, result.OK, result.Message)
	resp.OK(c, result)
}

func (h *Handler) runTest(src *persistence.DataSource) {
	result, _ := h.cred.Test(context.Background(), src)
	h.cred.UpdateHealth(src, result.OK, result.Message)
}

// Disable 停用
func (h *Handler) Disable(c *gin.Context) {
	var src persistence.DataSource
	if err := h.db.First(&src, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "数据源不存在"))
		return
	}
	_ = h.db.Model(&src).Update("status", "disabled").Error
	middleware.Log(c, "datasource", "datasource.disable", src.Name, "")
	resp.OK(c, nil)
}

// Enable 启用（停用后恢复）
func (h *Handler) Enable(c *gin.Context) {
	var src persistence.DataSource
	if err := h.db.First(&src, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "数据源不存在"))
		return
	}
	_ = h.db.Model(&src).Update("status", "available").Error
	src.Status = "available"
	middleware.Log(c, "datasource", "datasource.enable", src.Name, "")
	// 启用后异步刷新一次健康状态
	go h.runTest(&src)
	resp.OK(c, nil)
}

// Delete 删除（仅允许删除已停用的数据源, 且无启用中的采集流程引用）
func (h *Handler) Delete(c *gin.Context) {
	var src persistence.DataSource
	if err := h.db.First(&src, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "数据源不存在"))
		return
	}
	if src.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅已停用的数据源可删除, 请先停用"))
		return
	}
	var refCount int64
	_ = h.db.Model(&persistence.CollectFlow{}).Where("data_source_id = ?", src.ID).Count(&refCount).Error
	if refCount > 0 {
		var flows []string
		_ = h.db.Model(&persistence.CollectFlow{}).
			Where("data_source_id = ?", src.ID).
			Order("id").Limit(5).
			Pluck("flow_code", &flows).Error
		extra := ""
		if refCount > int64(len(flows)) {
			extra = fmt.Sprintf(" 等 %d 个", refCount)
		}
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该数据源仍被采集流程引用（%s%s），请先删除相关流程", strings.Join(flows, ", "), extra))
		return
	}
	if err := h.db.Delete(&src).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "datasource", "datasource.delete", src.Name, "type="+src.Type)
	resp.OK(c, nil)
}

// normalizeConfig 校验字段 + 加密凭据
func (h *Handler) normalizeConfig(t string, raw json.RawMessage) ([]byte, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, resp.NewErr(resp.CodeBadRequest, "config 解析失败: %v", err)
	}
	cfg.Type = t
	switch t {
	case "db":
		if cfg.Driver == "" || cfg.Host == "" || cfg.Username == "" {
			return nil, resp.NewErr(resp.CodeBadRequest, "db 数据源须配置 driver/host/username")
		}
		if cfg.Driver == "mysql" && cfg.Database == "" {
			return nil, resp.NewErr(resp.CodeBadRequest, "mysql 须配置 database")
		}
		if cfg.Driver == "oracle" && cfg.Service == "" && cfg.Database == "" {
			return nil, resp.NewErr(resp.CodeBadRequest, "oracle 须配置 service 或 database")
		}
		if cfg.Port == 0 {
			if cfg.Driver == "oracle" {
				cfg.Port = 1521
			} else {
				cfg.Port = 3306
			}
		}
		if cfg.Password != "" {
			enc, err := h.cred.cipher.Encrypt(cfg.Password)
			if err != nil {
				return nil, resp.NewErr(resp.CodeInternal, "凭据加密失败")
			}
			cfg.Password = enc
		}
	case "log":
		if cfg.Scheme == "" {
			cfg.Scheme = "local"
		}
		if cfg.PathTemplate == "" && cfg.GlobTemplate == "" {
			return nil, resp.NewErr(resp.CodeBadRequest, "log 数据源须配置 pathTemplate 或 globTemplate")
		}
		if cfg.Scheme == "sftp" && (cfg.Host == "" || cfg.Username == "") {
			return nil, resp.NewErr(resp.CodeBadRequest, "sftp 须配置 host/username")
		}
		if cfg.Scheme == "sftp" && cfg.Port == 0 {
			cfg.Port = 22
		}
		if cfg.Scheme == "sftp" && cfg.Password != "" {
			enc, err := h.cred.cipher.Encrypt(cfg.Password)
			if err != nil {
				return nil, resp.NewErr(resp.CodeInternal, "凭据加密失败")
			}
			cfg.Password = enc
		}
	case "ssh":
		// 远程脚本采集的执行目标: 脚本与参数经 SFTP 落临时目录, 执行后删除
		if cfg.Host == "" || cfg.Username == "" {
			return nil, resp.NewErr(resp.CodeBadRequest, "ssh 执行目标须配置 host/username")
		}
		if cfg.Port == 0 {
			cfg.Port = 22
		}
		if cfg.OS == "" {
			cfg.OS = "linux"
		}
		if cfg.OS != "linux" && cfg.OS != "windows" {
			return nil, resp.NewErr(resp.CodeBadRequest, "ssh 执行目标 os 仅支持 linux/windows")
		}
		if cfg.TempDir == "" {
			if cfg.OS == "windows" {
				cfg.TempDir = "C:/Windows/Temp"
			} else {
				cfg.TempDir = "/tmp"
			}
		}
		if cfg.Password != "" {
			enc, err := h.cred.cipher.Encrypt(cfg.Password)
			if err != nil {
				return nil, resp.NewErr(resp.CodeInternal, "凭据加密失败")
			}
			cfg.Password = enc
		}
	}
	if cfg.Encoding == "" {
		cfg.Encoding = "utf-8"
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, resp.NewErr(resp.CodeInternal, "config 序列化失败")
	}
	return out, nil
}

// mergeConfig 合并更新: 新密码为空(或回显的掩码值)则沿用旧密文, 且不得再次加密
// (旧实现把旧密文当作新明文传入 normalizeConfig 二次加密, 导致解密后仍是密文, 连接报
// ORA-01017 / Access denied)
func (h *Handler) mergeConfig(src *persistence.DataSource, raw json.RawMessage) ([]byte, error) {
	var old, neu Config
	if err := json.Unmarshal([]byte(src.Config), &old); err != nil {
		return nil, resp.NewErr(resp.CodeBadRequest, "旧配置解析失败")
	}
	if err := json.Unmarshal(raw, &neu); err != nil {
		return nil, resp.NewErr(resp.CodeBadRequest, "config 解析失败: %v", err)
	}
	// 请求体未携带 globTemplate 字段 => 沿用旧值(旧版编辑表单/脚本不知道该字段, 整体覆盖会
	// 把"采集实际读取的文件匹配模式"抹掉, 退化成只读 pathTemplate 目录导致无匹配文件)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err == nil {
		if _, ok := probe["globTemplate"]; !ok && old.GlobTemplate != "" {
			neu.GlobTemplate = old.GlobTemplate
		}
	}
	// 密码留空或为展示层掩码值 => 本次不修改密码
	if neu.Password == "" || neu.Password == "******" {
		neu.Password = ""
	}
	merged, err := h.normalizeConfig(src.Type, mustMarshal(neu))
	if err != nil {
		return nil, err
	}
	if old.Password != "" {
		var out Config
		if err := json.Unmarshal(merged, &out); err != nil {
			return nil, resp.NewErr(resp.CodeInternal, "config 序列化失败")
		}
		if out.Password == "" {
			out.Password = old.Password // 密文原样沿用, 禁止二次加密
		}
		return mustMarshal(out), nil
	}
	return merged, nil
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// systemExists 校验业务系统是否存在
func systemExists(db *gorm.DB, systemCode string) bool {
	var n int64
	_ = db.Model(&persistence.BusinessSystem{}).Where("system_code = ?", systemCode).Count(&n).Error
	return n > 0
}

// validateSystemCategory 校验: 系统存在 + 分类存在 + 分类归属该系统
func (h *Handler) validateSystemCategory(systemCode string, categoryID int64) error {
	if !systemExists(h.db, systemCode) {
		return resp.NewErr(resp.CodeBadRequest, "业务系统不存在: %s", systemCode)
	}
	var cat persistence.DataSourceCategory
	if err := h.db.First(&cat, categoryID).Error; err != nil {
		return resp.NewErr(resp.CodeBadRequest, "数据源分类不存在")
	}
	if cat.SystemCode != systemCode {
		return resp.NewErr(resp.CodeBadRequest, "所选分类不属于该业务系统")
	}
	return nil
}

// dsView 列表视图: 附加系统名称/分类名称
type dsView struct {
	persistence.DataSource
	SystemName   string `json:"systemName"`
	CategoryName string `json:"categoryName"`
}

// enrichDS 批量回填系统名称与分类名称
func (h *Handler) enrichDS(items []persistence.DataSource) []dsView {
	sysCodes := map[string]struct{}{}
	catIDs := map[int64]struct{}{}
	for i := range items {
		if items[i].SystemCode != "" {
			sysCodes[items[i].SystemCode] = struct{}{}
		}
		if items[i].CategoryID != nil {
			catIDs[*items[i].CategoryID] = struct{}{}
		}
	}
	sysName := map[string]string{}
	if len(sysCodes) > 0 {
		codes := make([]string, 0, len(sysCodes))
		for c := range sysCodes {
			codes = append(codes, c)
		}
		var sysList []persistence.BusinessSystem
		_ = h.db.Where("system_code IN ?", codes).Find(&sysList).Error
		for _, s := range sysList {
			sysName[s.SystemCode] = s.Name
		}
	}
	catName := map[int64]string{}
	if len(catIDs) > 0 {
		ids := make([]int64, 0, len(catIDs))
		for id := range catIDs {
			ids = append(ids, id)
		}
		var cats []persistence.DataSourceCategory
		_ = h.db.Where("id IN ?", ids).Find(&cats).Error
		for _, ct := range cats {
			catName[ct.ID] = ct.Name
		}
	}
	out := make([]dsView, 0, len(items))
	for _, it := range items {
		v := dsView{DataSource: it, SystemName: sysName[it.SystemCode]}
		if it.CategoryID != nil {
			v.CategoryName = catName[*it.CategoryID]
		}
		out = append(out, v)
	}
	return out
}
