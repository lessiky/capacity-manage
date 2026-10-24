package datasource

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// dsImportRow 是 CSV/JSON 批量导入的一行数据源配置（类型专属列扁平化）。
type dsImportRow struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	SystemCode   string `json:"systemCode"`
	CategoryName string `json:"categoryName"`

	Driver   string `json:"driver,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	Database string `json:"database,omitempty"`
	Service  string `json:"service,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	TestSQL  string `json:"testSql,omitempty"`

	Scheme       string `json:"scheme,omitempty"`
	PathTemplate string `json:"pathTemplate,omitempty"`
	GlobTemplate string `json:"globTemplate,omitempty"`
	Encoding     string `json:"encoding,omitempty"`

	OS      string `json:"os,omitempty"`
	TempDir string `json:"tempDir,omitempty"`
}

// dsImportRequired 是 CSV 必需表头（小写比较）。
var dsImportRequired = []string{"name", "type", "systemcode", "categoryname"}

// parseDataSourceCSV 解析批量导入 CSV: 表头大小写不敏感, 列顺序任意, 缺列由表头校验拦截。
func parseDataSourceCSV(r io.Reader) ([]dsImportRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("至少需要表头与一行数据")
	}
	index := map[string]int{}
	for i, h := range records[0] {
		index[strings.TrimSpace(strings.ToLower(h))] = i
	}
	var missing []string
	for _, need := range dsImportRequired {
		if _, ok := index[need]; !ok {
			missing = append(missing, need)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必需表头: %s", strings.Join(missing, ", "))
	}
	get := func(rec []string, key string) string {
		idx, ok := index[key]
		if !ok || idx >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[idx])
	}
	out := make([]dsImportRow, 0, len(records)-1)
	for _, rec := range records[1:] {
		item := dsImportRow{
			Name: get(rec, "name"), Type: get(rec, "type"),
			SystemCode: get(rec, "systemcode"), CategoryName: get(rec, "categoryname"),
			Driver: get(rec, "driver"), Host: get(rec, "host"), Port: get(rec, "port"),
			Database: get(rec, "database"), Service: get(rec, "service"),
			Username: get(rec, "username"), Password: get(rec, "password"), TestSQL: get(rec, "testsql"),
			Scheme: get(rec, "scheme"), PathTemplate: get(rec, "pathtemplate"),
			GlobTemplate: get(rec, "globtemplate"), Encoding: get(rec, "encoding"),
			OS: get(rec, "os"), TempDir: get(rec, "tempdir"),
		}
		if item.Name == "" && item.Type == "" && item.SystemCode == "" && item.CategoryName == "" {
			continue // 跳过表尾空行
		}
		out = append(out, item)
	}
	return out, nil
}

type dsImportFailure struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// Import 批量导入数据源配置（multipart CSV 或 JSON 数组）。
func (h *Handler) Import(c *gin.Context) {
	rows, filename, err := h.readImportRows(c)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", err.Error()))
		return
	}
	if len(rows) == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "导入内容为空"))
		return
	}
	operator := operatorOf(c)
	failures := make([]dsImportFailure, 0)
	success := 0
	for i, r := range rows {
		if reason := h.importDataSourceRow(r, operator); reason != "" {
			failures = append(failures, dsImportFailure{Row: i + 1, Reason: reason})
			continue
		}
		success++
	}
	status := "success"
	switch {
	case success == 0:
		status = "failed"
	case len(failures) > 0:
		status = "partial"
	}
	middleware.Log(c, "datasource", "datasource.import", filename,
		fmt.Sprintf("total=%d success=%d failed=%d", len(rows), success, len(failures)))
	resp.OK(c, gin.H{
		"total": len(rows), "success": success, "failed": len(failures),
		"status": status, "errors": failures,
	})
}

// readImportRows 优先读 multipart 的 file 字段(CSV), 否则按 JSON 数组解析。
func (h *Handler) readImportRows(c *gin.Context) ([]dsImportRow, string, error) {
	file, header, err := c.Request.FormFile("file")
	if err == nil {
		defer file.Close()
		rows, perr := parseDataSourceCSV(file)
		if perr != nil {
			return nil, "", fmt.Errorf("CSV 解析失败: %v", perr)
		}
		return rows, header.Filename, nil
	}
	var rows []dsImportRow
	if berr := c.ShouldBindJSON(&rows); berr != nil {
		return nil, "", errors.New("需要 CSV 文件或 JSON 数组")
	}
	return rows, "inline", nil
}

// importDataSourceRow 导入单行: 分类按"系统+分类名"定位, 同名数据源更新(类型不可变更)。
func (h *Handler) importDataSourceRow(r dsImportRow, operator string) string {
	name := strings.TrimSpace(r.Name)
	typ := strings.ToLower(strings.TrimSpace(r.Type))
	systemCode := strings.TrimSpace(r.SystemCode)
	categoryName := strings.TrimSpace(r.CategoryName)
	if name == "" || typ == "" || systemCode == "" || categoryName == "" {
		return "name/type/systemCode/categoryName 不能为空"
	}
	if typ != "db" && typ != "log" && typ != "ssh" {
		return "不支持的数据源类型: " + typ
	}
	var cat persistence.DataSourceCategory
	if err := h.db.Where("system_code = ? AND name = ?", systemCode, categoryName).First(&cat).Error; err != nil {
		return fmt.Sprintf("分类不存在: %s/%s", systemCode, categoryName)
	}
	if err := h.validateSystemCategory(systemCode, cat.ID); err != nil {
		return err.Error()
	}
	cfg := Config{
		Type: typ, Driver: strings.TrimSpace(r.Driver), Host: strings.TrimSpace(r.Host),
		Database: strings.TrimSpace(r.Database), Service: strings.TrimSpace(r.Service),
		Username: strings.TrimSpace(r.Username), Password: r.Password, TestSQL: strings.TrimSpace(r.TestSQL),
		Scheme: strings.TrimSpace(r.Scheme), PathTemplate: strings.TrimSpace(r.PathTemplate),
		GlobTemplate: strings.TrimSpace(r.GlobTemplate), Encoding: strings.TrimSpace(r.Encoding),
		OS: strings.TrimSpace(r.OS), TempDir: strings.TrimSpace(r.TempDir),
	}
	if v := strings.TrimSpace(r.Port); v != "" {
		port, perr := strconv.Atoi(v)
		if perr != nil || port <= 0 || port > 65535 {
			return "端口不合法: " + v
		}
		cfg.Port = port
	}
	raw, _ := json.Marshal(cfg)

	var existing persistence.DataSource
	err := h.db.Where("name = ?", name).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		normalized, nerr := h.normalizeConfig(typ, raw)
		if nerr != nil {
			return nerr.Error()
		}
		src := persistence.DataSource{
			Name: name, Type: typ, Status: "available", Config: string(normalized),
			SystemCode: systemCode, CategoryID: &cat.ID, CreatedBy: operator,
		}
		if cerr := h.db.Create(&src).Error; cerr != nil {
			return "创建失败: " + cerr.Error()
		}
		return ""
	}
	if err != nil {
		return "查询失败: " + err.Error()
	}
	if existing.Type != typ {
		return fmt.Sprintf("已存在同名数据源且类型不一致(现有 %s, 导入 %s)", existing.Type, typ)
	}
	merged, merr := h.mergeConfig(&existing, raw)
	if merr != nil {
		return merr.Error()
	}
	if uerr := h.db.Model(&existing).Updates(map[string]any{
		"system_code": systemCode, "category_id": cat.ID, "config": string(merged),
	}).Error; uerr != nil {
		return "更新失败: " + uerr.Error()
	}
	return ""
}

// operatorOf 取当前登录用户名, 用于 CreatedBy 审计; 导入接口不在会话内时回退 system。
func operatorOf(c *gin.Context) string {
	if v, ok := c.Get(middleware.CtxUsername); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "system"
}
