package metric

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

func callCategoryJSON(t *testing.T, fn func(*gin.Context), method, path string, params gin.Params, payload map[string]any, usernames ...string) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	username := "admin"
	if len(usernames) > 0 && usernames[0] != "" {
		username = usernames[0]
	}
	c.Set(middleware.CtxUsername, username)
	fn(c)
	var b resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return b
}

func TestMetricCategoryWritesRespectSystemDataScope(t *testing.T) {
	h, db := newTreeTestHandler(t)
	for _, system := range []persistence.BusinessSystem{
		{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"},
		{SystemCode: "S2", Name: "系统二", Owner: "admin", Status: "normal"},
	} {
		if err := db.DB.Create(&system).Error; err != nil {
			t.Fatal(err)
		}
	}
	user := persistence.SysUser{Username: "scoped", PasswordHash: "x", RealName: "受限用户", Status: "active"}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.SysDataScope{
		UserID: &user.ID, SystemCode: "S1", ScopeType: "manage",
	}).Error; err != nil {
		t.Fatal(err)
	}

	b := callCategoryJSON(t, h.CreateCategory, http.MethodPost, "/api/v1/metric-categories", nil, map[string]any{
		"systemCode": "S2", "name": "无权创建",
	}, "scoped")
	if b.Code != resp.CodeDataForbidden {
		t.Fatalf("create category outside scope should be forbidden: %+v", b)
	}

	b = callCategoryJSON(t, h.CreateCategory, http.MethodPost, "/api/v1/metric-categories", nil, map[string]any{
		"systemCode": "S1", "name": "交易",
	}, "scoped")
	if b.Code != resp.CodeOK {
		t.Fatalf("create category inside scope: %+v", b)
	}
	b = callCategoryJSON(t, h.CreateCategory, http.MethodPost, "/api/v1/metric-categories", nil, map[string]any{
		"systemCode": "S2", "name": "存储",
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create category as platform admin: %+v", b)
	}
	var cat persistence.MetricCategory
	if err := db.DB.Where("system_code = ? AND name = ?", "S2", "存储").First(&cat).Error; err != nil {
		t.Fatal(err)
	}

	b = callCategoryJSON(t, h.UpdateCategory, http.MethodPut, "/api/v1/metric-categories/"+strconv.FormatInt(cat.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(cat.ID, 10)}}, map[string]any{"name": "存储新"}, "scoped")
	if b.Code != resp.CodeDataForbidden {
		t.Fatalf("update category outside scope should be forbidden: %+v", b)
	}
	b = callCategoryJSON(t, h.DeleteCategory, http.MethodDelete, "/api/v1/metric-categories/"+strconv.FormatInt(cat.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(cat.ID, 10)}}, nil, "scoped")
	if b.Code != resp.CodeDataForbidden {
		t.Fatalf("delete category outside scope should be forbidden: %+v", b)
	}
}

func TestCreateMetricDefaultsAndValidatesCategory(t *testing.T) {
	h, db := newTreeTestHandler(t)
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}

	b := createMetricReq(t, h, map[string]any{
		"metricCode": "CPU", "name": "CPU使用率", "systemCode": "S1",
		"unit": "%", "thresholdMode": "percent", "warnLine": 80,
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create without category: %+v", b)
	}
	var m persistence.CapacityMetric
	if err := db.DB.Where("metric_code = ?", "CPU").First(&m).Error; err != nil {
		t.Fatal(err)
	}
	if m.Category != "默认分类" {
		t.Fatalf("default category = %q, want 默认分类", m.Category)
	}

	b = createMetricReq(t, h, map[string]any{
		"metricCode": "BAD", "name": "未知分类", "systemCode": "S1", "category": "不存在",
		"unit": "%", "thresholdMode": "percent", "warnLine": 80,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("unknown category should fail: %+v", b)
	}
}

func TestCreateChildMetricInheritsParentCategory(t *testing.T) {
	h, db := newTreeTestHandler(t)
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.MetricCategory{SystemCode: "S1", Name: "交易"}).Error; err != nil {
		t.Fatal(err)
	}
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "SRV", "name": "服务", "systemCode": "S1", "metricKind": "aggregate",
		"unit": "-", "category": "交易",
	}); b.Code != resp.CodeOK {
		t.Fatalf("create aggregate: %+v", b)
	}
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.ORD", "name": "订单服务", "systemCode": "S1", "metricKind": "group",
		"parentCode": "SRV", "unit": "-",
	}); b.Code != resp.CodeOK {
		t.Fatalf("create group: %+v", b)
	}
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.ORD.COUNT", "name": "请求量", "systemCode": "S1",
		"parentCode": "SRV.ORD", "statModule": "count", "unit": "笔", "statMode": "sum",
		"thresholdMode": "absolute", "warnLine": 1,
	}); b.Code != resp.CodeOK {
		t.Fatalf("create module: %+v", b)
	}

	var got []persistence.CapacityMetric
	if err := db.DB.Where("metric_code IN ?", []string{"SRV", "SRV.ORD", "SRV.ORD.COUNT"}).Order("metric_code").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("metrics = %d", len(got))
	}
	for _, m := range got {
		if m.Category != "交易" {
			t.Fatalf("metric %s category = %q, want 交易", m.MetricCode, m.Category)
		}
	}
}

func TestUpdateMetricCategoryCascadeRules(t *testing.T) {
	h, db := newTreeTestHandler(t)
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"交易", "基础设施"} {
		if err := db.DB.Create(&persistence.MetricCategory{SystemCode: "S1", Name: name}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, payload := range []map[string]any{
		{"metricCode": "SRV", "name": "服务", "systemCode": "S1", "metricKind": "aggregate", "unit": "-", "category": "交易"},
		{"metricCode": "SRV.ORD", "name": "订单服务", "systemCode": "S1", "metricKind": "group", "parentCode": "SRV", "unit": "-"},
		{"metricCode": "SRV.ORD.COUNT", "name": "请求量", "systemCode": "S1", "parentCode": "SRV.ORD", "statModule": "count", "unit": "笔", "statMode": "sum", "thresholdMode": "absolute", "warnLine": 1},
		{"metricCode": "CPU", "name": "CPU", "systemCode": "S1", "unit": "%", "category": "交易", "thresholdMode": "percent", "warnLine": 80},
	} {
		if b := createMetricReq(t, h, payload); b.Code != resp.CodeOK {
			t.Fatalf("create %s: %+v", payload["metricCode"], b)
		}
	}

	b := callPut(t, h.Update, gin.Params{{Key: "code", Value: "SRV"}}, map[string]any{"category": "基础设施"})
	if b.Code != resp.CodeOK {
		t.Fatalf("update aggregate category: %+v", b)
	}
	for _, code := range []string{"SRV", "SRV.ORD", "SRV.ORD.COUNT"} {
		var m persistence.CapacityMetric
		if err := db.DB.Where("metric_code = ?", code).First(&m).Error; err != nil {
			t.Fatal(err)
		}
		if m.Category != "基础设施" {
			t.Fatalf("%s category = %q, want 基础设施", code, m.Category)
		}
	}

	for _, code := range []string{"SRV.ORD", "SRV.ORD.COUNT"} {
		b = callPut(t, h.Update, gin.Params{{Key: "code", Value: code}}, map[string]any{"category": "交易"})
		if b.Code != resp.CodeBadRequest {
			t.Fatalf("%s category change should fail: %+v", code, b)
		}
	}

	b = callPut(t, h.Update, gin.Params{{Key: "code", Value: "CPU"}}, map[string]any{"category": "基础设施"})
	if b.Code != resp.CodeOK {
		t.Fatalf("standalone metric category change: %+v", b)
	}
}

func TestMetricCategoryCRUDAndRenameCascade(t *testing.T) {
	h, db := newTreeTestHandler(t)
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "S2", Name: "系统二", Owner: "admin", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}

	b := callCategoryJSON(t, h.CreateCategory, http.MethodPost, "/api/v1/metric-categories", nil, map[string]any{
		"systemCode": "S1", "name": "核心", "sort": 1,
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create category: %+v", b)
	}
	var cat persistence.MetricCategory
	if err := db.DB.Where("system_code = ? AND name = ?", "S1", "核心").First(&cat).Error; err != nil {
		t.Fatal(err)
	}
	b = callCategoryJSON(t, h.ListCategories, http.MethodGet, "/api/v1/metric-categories", nil, nil)
	if b.Code != resp.CodeOK {
		t.Fatalf("list categories: %+v", b)
	}
	var listed []persistence.MetricCategory
	raw, err := json.Marshal(b.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("list data: %v %s", err, b.Data)
	}
	if len(listed) != 1 || listed[0].SystemCode != "S1" || listed[0].Name != "核心" {
		t.Fatalf("platform admin should see all categories: %+v", listed)
	}
	b = callCategoryJSON(t, h.CreateCategory, http.MethodPost, "/api/v1/metric-categories", nil, map[string]any{
		"systemCode": "S1", "name": "核心",
	})
	if b.Code != resp.CodeConflict {
		t.Fatalf("duplicate category should fail: %+v", b)
	}
	b = callCategoryJSON(t, h.CreateCategory, http.MethodPost, "/api/v1/metric-categories", nil, map[string]any{
		"systemCode": "S2", "name": "核心",
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("same category in another system should pass: %+v", b)
	}

	if err := db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "C1", Name: "核心指标", SystemCode: "S1", Category: "核心",
		Unit: "%", MetricKind: "normal", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	b = callCategoryJSON(t, h.UpdateCategory, http.MethodPut, "/api/v1/metric-categories/"+strconv.FormatInt(cat.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(cat.ID, 10)}}, map[string]any{"name": "核心交易", "sort": 2})
	if b.Code != resp.CodeOK {
		t.Fatalf("rename category: %+v", b)
	}
	var m persistence.CapacityMetric
	if err := db.DB.Where("metric_code = ?", "C1").First(&m).Error; err != nil {
		t.Fatal(err)
	}
	if m.Category != "核心交易" {
		t.Fatalf("metric category after rename = %q", m.Category)
	}

	b = callCategoryJSON(t, h.DeleteCategory, http.MethodDelete, "/api/v1/metric-categories/"+strconv.FormatInt(cat.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(cat.ID, 10)}}, nil)
	if b.Code != resp.CodeConflict {
		t.Fatalf("delete used category should fail: %+v", b)
	}
	if err := db.DB.Delete(&m).Error; err != nil {
		t.Fatal(err)
	}
	b = callCategoryJSON(t, h.DeleteCategory, http.MethodDelete, "/api/v1/metric-categories/"+strconv.FormatInt(cat.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(cat.ID, 10)}}, nil)
	if b.Code != resp.CodeOK {
		t.Fatalf("delete unused category: %+v", b)
	}
}

func TestMetricListAndTreeFilterByCategory(t *testing.T) {
	h, db := newTreeTestHandler(t)
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"交易", "基础设施"} {
		if err := db.DB.Create(&persistence.MetricCategory{SystemCode: "S1", Name: name}).Error; err != nil {
			t.Fatal(err)
		}
	}
	parent := "A1"
	rows := []persistence.CapacityMetric{
		{MetricCode: "A1", Name: "交易汇总", SystemCode: "S1", Category: "交易", MetricKind: "aggregate", Unit: "-", Status: "active"},
		{MetricCode: "A1.COUNT", Name: "交易量", SystemCode: "S1", Category: "交易", MetricKind: "normal", ParentCode: &parent, StatModule: "count", Unit: "笔", Status: "active"},
		{MetricCode: "B1", Name: "基础设施汇总", SystemCode: "S1", Category: "基础设施", MetricKind: "aggregate", Unit: "-", Status: "active"},
	}
	if err := db.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	b := callGet(t, h, h.List, nil, url.Values{"category": {"交易"}}.Encode())
	if b.Code != resp.CodeOK {
		t.Fatalf("list category filter: %+v", b)
	}
	var page struct {
		Total int `json:"total"`
		Items []struct {
			MetricCode string `json:"metricCode"`
		} `json:"items"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &page); err != nil {
		t.Fatalf("list data: %v %s", err, b.Data)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("filtered list = %+v", page)
	}

	b = callGet(t, h, h.Tree, nil, "")
	if b.Code != resp.CodeOK {
		t.Fatalf("tree: %+v", b)
	}
	var tree struct {
		Categories []struct {
			Category string `json:"category"`
			Count    int    `json:"count"`
			Roots    []struct {
				MetricCode string `json:"metricCode"`
			} `json:"roots"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &tree); err != nil {
		t.Fatalf("tree data: %v %s", err, b.Data)
	}
	if len(tree.Categories) != 2 {
		t.Fatalf("tree categories = %+v", tree.Categories)
	}
	if tree.Categories[0].Category != "交易" || tree.Categories[0].Count != 2 || len(tree.Categories[0].Roots) != 1 {
		t.Fatalf("交易 group = %+v", tree.Categories[0])
	}

	b = callGet(t, h, h.Tree, nil, url.Values{"category": {"基础设施"}}.Encode())
	if err := json.Unmarshal(dataJSON(t, b), &tree); err != nil {
		t.Fatalf("filtered tree data: %v %s", err, b.Data)
	}
	if len(tree.Categories) != 1 || tree.Categories[0].Category != "基础设施" || tree.Categories[0].Count != 1 {
		t.Fatalf("filtered categories = %+v", tree.Categories)
	}
}
