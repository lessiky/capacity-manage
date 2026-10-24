package flow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"net/http/httptest"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pyclient"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func callScriptHandler(t *testing.T, h *Handler, fn func(*gin.Context), id int64) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+fmt.Sprint(id), nil)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
	fn(c)
	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return body
}

func TestDeleteScriptReferenceBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.PythonScript{}, &persistence.PythonScriptVersion{}, &persistence.CollectFlow{})
	hh := &Handler{db: h.DB}

	script := persistence.PythonScript{Name: "core_collect", ScriptType: "collect"}
	if err := h.DB.Create(&script).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.PythonScriptVersion{ScriptID: script.ID, Version: 1, Content: "print(1)", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	// 被 python 采集流程引用(collectConfig.scriptId) → 拒绝删除
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: "f-ref", MetricCode: "m1", DataSourceID: int64Ptr(1), CollectType: "python",
		CollectConfig: `{"scriptId":` + fmt.Sprint(script.ID) + `,"version":1}`, Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	body := callScriptHandler(t, hh, hh.DeleteScript, script.ID)
	if body.Code != resp.CodeConflict {
		t.Fatalf("delete referenced: %+v", body)
	}

	// 仅清洗配置引用(cleanConfig.script.scriptId) → 同样拒绝
	s2 := persistence.PythonScript{Name: "core_clean", ScriptType: "clean"}
	if err := h.DB.Create(&s2).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: "f-clean", MetricCode: "m2", DataSourceID: int64Ptr(1), CollectType: "sql",
		CollectConfig: `{"sqlTemplate":"SELECT 1"}`,
		CleanConfig: `{"script":{"scriptId":` + fmt.Sprint(s2.ID) + `}}`, Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	body = callScriptHandler(t, hh, hh.DeleteScript, s2.ID)
	if body.Code != resp.CodeConflict {
		t.Fatalf("delete clean-referenced: %+v", body)
	}

	// 无引用 → 删除成功且版本一并清理
	s3 := persistence.PythonScript{Name: "unused", ScriptType: "clean"}
	if err := h.DB.Create(&s3).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.PythonScriptVersion{ScriptID: s3.ID, Version: 1, Content: "print(2)", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	body = callScriptHandler(t, hh, hh.DeleteScript, s3.ID)
	if body.Code != resp.CodeOK {
		t.Fatalf("delete unused: %+v", body)
	}
	var n int64
	_ = h.DB.Model(&persistence.PythonScript{}).Where("id = ?", s3.ID).Count(&n).Error
	if n != 0 {
		t.Fatal("script not deleted")
	}
	_ = h.DB.Model(&persistence.PythonScriptVersion{}).Where("script_id = ?", s3.ID).Count(&n).Error
	if n != 0 {
		t.Fatal("versions not deleted")
	}

	// 不存在的脚本
	body = callScriptHandler(t, hh, hh.DeleteScript, 99999)
	if body.Code != resp.CodeNotFound {
		t.Fatalf("delete missing: %+v", body)
	}
}
func int64Ptr(v int64) *int64 { return &v }

// FR-COL-17: 脚本语言 python/bash/powershell, 缺省 python; 未知语言拒绝
func TestCreateScriptLanguageValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.PythonScript{}, &persistence.PythonScriptVersion{})
	hh := &Handler{db: h.DB}

	create := func(body string) resp.Body {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/scripts", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		hh.CreateScript(c)
		var b resp.Body
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatalf("response not json: %s", w.Body.String())
		}
		return b
	}
	langOf := func(name string) string {
		t.Helper()
		var s persistence.PythonScript
		if err := h.DB.Where("name = ?", name).First(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.Language
	}

	if b := create(`{"name":"disk_bash","scriptType":"collect","language":"bash"}`); b.Code != resp.CodeOK {
		t.Fatalf("bash create: %+v", b)
	}
	if got := langOf("disk_bash"); got != "bash" {
		t.Fatalf("language = %q, want bash", got)
	}

	if b := create(`{"name":"disk_default","scriptType":"collect"}`); b.Code != resp.CodeOK {
		t.Fatalf("default create: %+v", b)
	}
	if got := langOf("disk_default"); got != "python" {
		t.Fatalf("default language = %q, want python", got)
	}

	if b := create(`{"name":"disk_perl","scriptType":"collect","language":"perl"}`); b.Code != resp.CodeBadRequest {
		t.Fatalf("perl should be rejected: %+v", b)
	}

	// 清洗脚本在本地沙箱执行, 仅支持 python
	if b := create(`{"name":"clean_bash","scriptType":"clean","language":"bash"}`); b.Code != resp.CodeBadRequest {
		t.Fatalf("clean+bash should be rejected: %+v", b)
	}
}

// 版本保存的语法校验只对 python 生效: bash/powershell 由目标机执行,
// 不能拿 Python 解析器去校验(否则 bash 脚本会被误判为 "invalid decimal literal")。
func TestCreateVersionSyntaxCheckByLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.PythonScript{}, &persistence.PythonScriptVersion{})

	// 假 worker: /v1/check 一律判失败, 模拟"把 bash 当 Python 解析"的返回
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid decimal literal","line":48}`))
	}))
	defer srv.Close()
	hh := &Handler{db: h.DB, py: pyclient.New(srv.URL, time.Second)}

	bash := persistence.PythonScript{Name: "disk_bash", ScriptType: "collect", Language: "bash"}
	if err := h.DB.Create(&bash).Error; err != nil {
		t.Fatal(err)
	}
	py := persistence.PythonScript{Name: "disk_py", ScriptType: "collect", Language: "python"}
	if err := h.DB.Create(&py).Error; err != nil {
		t.Fatal(err)
	}

	post := func(id int64, content string) resp.Body {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"content": content})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+fmt.Sprint(id)+"/versions", strings.NewReader(string(payload)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
		hh.CreateVersion(c)
		var b resp.Body
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatalf("response not json: %v %s", err, w.Body.String())
		}
		return b
	}

	if b := post(bash.ID, "printf '[{\"metric\":\"DISK_USAGE_PCT\",\"value\":%s,\"unit\":\"%%\"}]\\n' \"$PCT\""); b.Code != resp.CodeOK {
		t.Fatalf("bash 版本不应被 Python 校验拦截, 实际: %+v", b)
	}
	if b := post(py.ID, "print(1)"); b.Code != resp.CodeBadRequest {
		t.Fatalf("python 版本仍须通过校验, 实际: %+v", b)
	}
}

func TestUpdateFlowRebindDSCategory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.DataSourceCategory{}, &persistence.DataSource{}, &persistence.CollectFlow{})
	hh := &Handler{db: h.DB}

	_ = h.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "sys1"}).Error
	cat := persistence.DataSourceCategory{SystemCode: "S1", Name: "cat1"}
	if err := h.DB.Create(&cat).Error; err != nil {
		t.Fatal(err)
	}
	catDS := persistence.DataSource{Name: "log1", Type: "log", Status: "available", Config: "{}", SystemCode: "S1", CategoryID: int64Ptr(cat.ID)}
	if err := h.DB.Create(&catDS).Error; err != nil {
		t.Fatal(err)
	}
	single := persistence.DataSource{Name: "log2", Type: "log", Status: "available", Config: "{}", SystemCode: "S1"}
	if err := h.DB.Create(&single).Error; err != nil {
		t.Fatal(err)
	}
	fl := persistence.CollectFlow{
		FlowCode: "f-cat", MetricCode: "m1", DataSourceCategoryID: int64Ptr(cat.ID),
		CollectType: "logparse", CollectConfig: "{\"mode\":\"regex\",\"pattern\":\"x\",\"valueField\":\"v\"}",
		Status: "active", Version: 1,
	}
	if err := h.DB.Create(&fl).Error; err != nil {
		t.Fatal(err)
	}

	put := func(id int64, body string) resp.Body {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/flows/"+fmt.Sprint(id), strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
		hh.Update(c)
		var b resp.Body
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatalf("response not json: %s", w.Body.String())
		}
		return b
	}
	getFl := func() persistence.CollectFlow {
		var f persistence.CollectFlow
		_ = h.DB.First(&f, fl.ID).Error
		return f
	}

	// 1) 分类 -> 单数据源重绑定: dataSourceId 数字 + dataSourceCategoryId 显式 null
	b := put(fl.ID, fmt.Sprintf(`{"dataSourceId": %d, "dataSourceCategoryId": null}`, single.ID))
	if b.Code != resp.CodeOK {
		t.Fatalf("rebind cat->ds: %+v", b)
	}
	f := getFl()
	if f.DataSourceID == nil || *f.DataSourceID != single.ID || f.DataSourceCategoryID != nil {
		t.Fatalf("binding after cat->ds: %+v", f)
	}
	if f.Version != 2 || f.Status != "created" {
		t.Fatalf("version/status: v=%d s=%s", f.Version, f.Status)
	}

	// 2) 两字段缺省: 沿用当前绑定(仅改名)
	b = put(fl.ID, `{"name": "renamed"}`)
	if b.Code != resp.CodeOK {
		t.Fatalf("name-only update: %+v", b)
	}
	f = getFl()
	if f.DataSourceID == nil || *f.DataSourceID != single.ID || f.DataSourceCategoryID != nil {
		t.Fatalf("binding after name-only: %+v", f)
	}

	// 3) 单数据源 -> 分类重绑定: dataSourceId 显式 null + dataSourceCategoryId 数字
	b = put(fl.ID, fmt.Sprintf(`{"dataSourceId": null, "dataSourceCategoryId": %d}`, cat.ID))
	if b.Code != resp.CodeOK {
		t.Fatalf("rebind ds->cat: %+v", b)
	}
	f = getFl()
	if f.DataSourceID != nil || f.DataSourceCategoryID == nil || *f.DataSourceCategoryID != cat.ID {
		t.Fatalf("binding after ds->cat: %+v", f)
	}

	// 4) 同时设置两项: 拒绝
	b = put(fl.ID, fmt.Sprintf(`{"dataSourceId": %d, "dataSourceCategoryId": %d}`, single.ID, cat.ID))
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("both set should be rejected: %+v", b)
	}
}

// python 采集在平台沙箱内执行, 不访问数据源 → 允许不绑定数据源/分类;
// 其他采集方式仍必须且只能绑定一项。
func TestValidatePythonFlowWithoutDataSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.PythonScript{}, &persistence.CollectFlow{})
	hh := &Handler{db: h.DB}

	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "M1", Name: "m1", SystemCode: "S1", MetricType: "usage", Unit: "%", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	script := persistence.PythonScript{Name: "c1", ScriptType: "collect", Language: "python"}
	if err := h.DB.Create(&script).Error; err != nil {
		t.Fatal(err)
	}
	pyCfg := fmt.Sprintf(`{"scriptId":%d,"version":1}`, script.ID)
	req := func(collectType, cfg string, dsID *int64) *flowReq {
		return &flowReq{
			FlowCode: "f1", Name: "f1", MetricCode: "M1", DataSourceID: dsID,
			CollectType: collectType, CollectConfig: json.RawMessage(cfg),
			ScheduleMode: "normal", CronExpr: "0 * * * *",
		}
	}

	if err := hh.validate(req("python", pyCfg, nil)); err != nil {
		t.Fatalf("python 采集不绑数据源应通过: %v", err)
	}
	if err := hh.validate(req("sql", `{"sqlTemplate":"SELECT 1"}`, nil)); err == nil || !strings.Contains(err.Error(), "须且仅可选择一项") {
		t.Fatalf("sql 采集不绑数据源应被拒: %v", err)
	}
}
