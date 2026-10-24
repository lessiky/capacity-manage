package datasource

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type importResult struct {
	Code int `json:"code"`
	Data struct {
		Total   int    `json:"total"`
		Success int    `json:"success"`
		Failed  int    `json:"failed"`
		Status  string `json:"status"`
		Errors  []struct {
			Row    int    `json:"row"`
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"data"`
}

func newImportHandler(t *testing.T) (*Handler, *gorm.DB, *aesgcm.Cipher) {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.DataSourceCategory{}, &persistence.DataSource{})
	if err := h.DB.Create(&persistence.BusinessSystem{SystemCode: "PAY", Name: "支付系统"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.DataSourceCategory{SystemCode: "PAY", Name: "核心库"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.DataSourceCategory{SystemCode: "PAY", Name: "日志目录"}).Error; err != nil {
		t.Fatal(err)
	}
	cipher, err := aesgcm.New(testCredKey)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(h.DB, cipher, nil, nil)
	return &Handler{db: h.DB, cred: reg}, h.DB, cipher
}

// csvLine 按 CSV 规则拼一行, 含逗号/引号/换行的字段自动加引号
func csvLine(fields ...string) string {
	for i, f := range fields {
		if strings.ContainsAny(f, ",\"\n") {
			fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		}
	}
	return strings.Join(fields, ",")
}

const dsCSVHeader = "name,type,systemCode,categoryName,driver,host,port,database,service,username,password,scheme,pathTemplate,globTemplate,os,tempDir"

func runImport(t *testing.T, h *Handler, csvDoc string) importResult {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "data-sources.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(csvDoc)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/data-sources/import", &body)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	c.Set(middleware.CtxUsername, "tester")
	h.Import(c)
	var out importResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return out
}

func runImportJSON(t *testing.T, h *Handler, payload string) importResult {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/data-sources/import", strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.CtxUsername, "tester")
	h.Import(c)
	var out importResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return out
}

// CSV 缺少必需表头时拒绝, 并指出缺哪一列
func TestParseDataSourceCSVMissingHeader(t *testing.T) {
	_, err := parseDataSourceCSV(strings.NewReader("name,type\nfoo,db\n"))
	if err == nil {
		t.Fatal("缺少 systemCode/categoryName 表头应报错")
	}
	if !strings.Contains(err.Error(), "systemcode") && !strings.Contains(err.Error(), "categoryname") {
		t.Fatalf("错误信息应指出缺失表头, got %q", err.Error())
	}
}

// 表头大小写不敏感, 类型专属字段按行读取
func TestParseDataSourceCSVReadsTypeSpecificFields(t *testing.T) {
	doc := strings.Join([]string{
		strings.ToUpper(dsCSVHeader),
		csvLine("pay-db-01", "db", "PAY", "核心库", "oracle", "10.0.0.1", "1521", "", "ORCL", "cap_read", "p@ss,word", "", "", "", "", ""),
	}, "\n")
	rows, err := parseDataSourceCSV(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Name != "pay-db-01" || r.Type != "db" || r.SystemCode != "PAY" || r.CategoryName != "核心库" {
		t.Fatalf("identity fields: %+v", r)
	}
	if r.Driver != "oracle" || r.Host != "10.0.0.1" || r.Port != "1521" || r.Service != "ORCL" || r.Username != "cap_read" {
		t.Fatalf("db fields: %+v", r)
	}
	if r.Password != "p@ss,word" {
		t.Fatalf("quoted password parsed as %q", r.Password)
	}
}

// 批量导入: 分类按名称定位, 凭据加密落库, 失败行返回行号与原因
func TestImportDataSourcesCreatesAndReportsFailures(t *testing.T) {
	h, db, cipher := newImportHandler(t)
	doc := strings.Join([]string{
		dsCSVHeader,
		csvLine("pay-db-01", "db", "PAY", "核心库", "oracle", "10.0.0.1", "1521", "", "ORCL", "cap_read", "p@ss,word", "", "", "", "", ""),
		csvLine("pay-log-01", "log", "PAY", "日志目录", "", "", "", "", "", "", "", "local", "/data/cap", "cap_*.log", "", ""),
		csvLine("bad-ds-01", "db", "PAY", "不存在的分类", "oracle", "10.0.0.3", "1521", "", "ORCL", "cap_read", "x", "", "", "", "", ""),
	}, "\n")

	out := runImport(t, h, doc)
	if out.Code != resp.CodeOK {
		t.Fatalf("code = %d", out.Code)
	}
	if out.Data.Total != 3 || out.Data.Success != 2 || out.Data.Failed != 1 || out.Data.Status != "partial" {
		t.Fatalf("summary: %+v", out.Data)
	}
	if len(out.Data.Errors) != 1 || out.Data.Errors[0].Row != 3 {
		t.Fatalf("errors: %+v", out.Data.Errors)
	}
	if !strings.Contains(out.Data.Errors[0].Reason, "分类") {
		t.Fatalf("failure reason should mention category, got %q", out.Data.Errors[0].Reason)
	}

	var rows []persistence.DataSource
	if err := db.Order("name").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("created %d rows, want 2", len(rows))
	}
	var dbRow persistence.DataSource
	if err := db.Where("name = ?", "pay-db-01").First(&dbRow).Error; err != nil {
		t.Fatal(err)
	}
	if dbRow.Type != "db" || dbRow.Status != "available" || dbRow.SystemCode != "PAY" || dbRow.CategoryID == nil {
		t.Fatalf("row fields: %+v", dbRow)
	}
	var saved Config
	if err := json.Unmarshal([]byte(dbRow.Config), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Host != "10.0.0.1" || saved.Service != "ORCL" {
		t.Fatalf("config: %+v", saved)
	}
	plain, err := cipher.Decrypt(saved.Password)
	if err != nil || plain != "p@ss,word" {
		t.Fatalf("password not encrypted: %q err=%v", saved.Password, err)
	}
}

// 同名数据源按 upsert 处理: 配置覆盖、密码留空不覆盖旧密文、类型变更被拒绝
func TestImportDataSourcesUpsertKeepsPasswordAndRejectsTypeChange(t *testing.T) {
	h, db, cipher := newImportHandler(t)
	oldEnc, err := cipher.Encrypt("old-secret")
	if err != nil {
		t.Fatal(err)
	}
	var cat persistence.DataSourceCategory
	if err := db.Where("name = ?", "核心库").First(&cat).Error; err != nil {
		t.Fatal(err)
	}
	existing := persistence.DataSource{
		Name: "pay-db-01", Type: "db", Status: "available", SystemCode: "PAY", CategoryID: &cat.ID,
		Config: `{"type":"db","driver":"oracle","host":"10.0.0.9","port":1521,"service":"OLD","username":"cap_read","password":"` + oldEnc + `"}`,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	doc := strings.Join([]string{
		dsCSVHeader,
		csvLine("pay-db-01", "db", "PAY", "核心库", "oracle", "10.0.0.2", "1521", "", "NEW", "cap_read", "", "", "", "", "", ""),
		csvLine("pay-db-01", "log", "PAY", "日志目录", "", "", "", "", "", "", "", "local", "/data/cap", "cap_*.log", "", ""),
	}, "\n")
	out := runImport(t, h, doc)
	if out.Data.Success != 1 || out.Data.Failed != 1 {
		t.Fatalf("summary: %+v", out.Data)
	}
	if !strings.Contains(out.Data.Errors[0].Reason, "类型") {
		t.Fatalf("type change should be rejected, got %q", out.Data.Errors[0].Reason)
	}

	var n int64
	_ = db.Model(&persistence.DataSource{}).Count(&n).Error
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (upsert, not insert)", n)
	}
	var row persistence.DataSource
	if err := db.Where("name = ?", "pay-db-01").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := json.Unmarshal([]byte(row.Config), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Host != "10.0.0.2" || saved.Service != "NEW" {
		t.Fatalf("config not updated: %+v", saved)
	}
	if saved.Password != oldEnc {
		t.Fatalf("blank password must keep old ciphertext")
	}
}

// 兼容 JSON 数组请求体, 与指标导入保持一致
func TestImportDataSourcesAcceptsJSONArray(t *testing.T) {
	h, db, _ := newImportHandler(t)
	payload := `[{"name":"pay-ssh-01","type":"ssh","systemCode":"PAY","categoryName":"核心库","host":"10.0.0.5","username":"cap","password":"pw","os":"linux","tempDir":"/data/tmp"}]`
	out := runImportJSON(t, h, payload)
	if out.Data.Success != 1 || out.Data.Failed != 0 {
		t.Fatalf("summary: %+v errors=%+v", out.Data, out.Data.Errors)
	}
	var row persistence.DataSource
	if err := db.Where("name = ?", "pay-ssh-01").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Type != "ssh" {
		t.Fatalf("type = %q", row.Type)
	}
}

// Import 未上传文件且请求体不是 JSON 数组时返回 400 而非 panic
func TestImportDataSourcesRejectsEmptyRequest(t *testing.T) {
	h, _, _ := newImportHandler(t)
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/data-sources/import", strings.NewReader("not-json"))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Import(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
