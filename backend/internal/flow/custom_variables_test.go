package flow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func callFlowVariableHandler(t *testing.T, h *Handler, fn func(*gin.Context), method, path, id, body string) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if id != "" {
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	fn(c)

	var out resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return out
}

func TestCreateFlowVariableRejectsBuiltinName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.FlowVariable{})
	hh := &Handler{db: h.DB}

	body := callFlowVariableHandler(t, hh, hh.CreateVariable, http.MethodPost, "/api/v1/flow-variables", "",
		`{"name":"biz_date","description":"覆盖业务日期","value":"1999-01-01"}`)
	if body.Code != resp.CodeBadRequest {
		t.Fatalf("builtin variable name should be rejected: %+v", body)
	}
	if !strings.Contains(body.Message, "内置变量") {
		t.Fatalf("error should explain builtin conflict: %q", body.Message)
	}
}

func TestFlowVariableCRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.FlowVariable{})
	hh := &Handler{db: h.DB}

	created := callFlowVariableHandler(t, hh, hh.CreateVariable, http.MethodPost, "/api/v1/flow-variables", "",
		`{"name":"cluster_code","description":"公共集群编码","value":"prod-sh"}`)
	if created.Code != resp.CodeOK {
		t.Fatalf("create: %+v", created)
	}
	raw, _ := json.Marshal(created.Data)
	var row persistence.FlowVariable
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row.Name != "cluster_code" || row.Description != "公共集群编码" || row.Value != "prod-sh" {
		t.Fatalf("created row: %+v", row)
	}

	duplicate := callFlowVariableHandler(t, hh, hh.CreateVariable, http.MethodPost, "/api/v1/flow-variables", "",
		`{"name":"cluster_code","description":"重复","value":"other"}`)
	if duplicate.Code != resp.CodeConflict {
		t.Fatalf("duplicate name should conflict: %+v", duplicate)
	}

	updated := callFlowVariableHandler(t, hh, hh.UpdateVariable, http.MethodPut, "/api/v1/flow-variables/"+fmt.Sprint(row.ID), fmt.Sprint(row.ID),
		`{"name":"cluster_code","description":"更新说明","value":"prod-bj"}`)
	if updated.Code != resp.CodeOK {
		t.Fatalf("update: %+v", updated)
	}
	var got persistence.FlowVariable
	if err := h.DB.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Description != "更新说明" || got.Value != "prod-bj" {
		t.Fatalf("updated row: %+v", got)
	}

	listed := callFlowVariableHandler(t, hh, hh.ListVariables, http.MethodGet, "/api/v1/flow-variables", "", "")
	if listed.Code != resp.CodeOK {
		t.Fatalf("list: %+v", listed)
	}
	items, ok := listed.Data.([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("list data: %#v", listed.Data)
	}

	deleted := callFlowVariableHandler(t, hh, hh.DeleteVariable, http.MethodDelete, "/api/v1/flow-variables/"+fmt.Sprint(row.ID), fmt.Sprint(row.ID), "")
	if deleted.Code != resp.CodeOK {
		t.Fatalf("delete: %+v", deleted)
	}
	var count int64
	_ = h.DB.Model(&persistence.FlowVariable{}).Where("id = ?", row.ID).Count(&count).Error
	if count != 0 {
		t.Fatal("flow variable was not deleted")
	}
}

func TestFlowVariableAllowsUppercaseAndDigits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.FlowVariable{})
	hh := &Handler{db: h.DB}

	body := callFlowVariableHandler(t, hh, hh.CreateVariable, http.MethodPost, "/api/v1/flow-variables", "",
		`{"name":"ClusterCode_2026","description":"集群编码","value":"prod-sh"}`)
	if body.Code != resp.CodeOK {
		t.Fatalf("uppercase and digit variable name should be accepted: %+v", body)
	}
}

func TestListFlowVariablesPaginatesByID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.FlowVariable{})
	hh := &Handler{db: h.DB}

	for _, row := range []persistence.FlowVariable{
		{Name: "zeta", Description: "first inserted", Value: "1"},
		{Name: "alpha", Description: "second inserted", Value: "2"},
		{Name: "middle", Description: "third inserted", Value: "3"},
	} {
		if err := h.DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	listed := callFlowVariableHandler(t, hh, hh.ListVariables, http.MethodGet,
		"/api/v1/flow-variables?page=1&pageSize=2", "", "")
	if listed.Code != resp.CodeOK {
		t.Fatalf("list: %+v", listed)
	}
	data, ok := listed.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("paginated list data: %#v", listed.Data)
	}
	if data["total"] != float64(3) || data["page"] != float64(1) || data["pageSize"] != float64(2) {
		t.Fatalf("pagination metadata: %#v", data)
	}
	items, ok := data["items"].([]interface{})
	if !ok || len(items) != 2 {
		t.Fatalf("pagination items: %#v", data["items"])
	}
	first, _ := items[0].(map[string]interface{})
	second, _ := items[1].(map[string]interface{})
	if first["name"] != "zeta" || second["name"] != "alpha" {
		t.Fatalf("items should be ordered by id ascending: %#v", items)
	}
}

func TestRenderTemplateAllowsUppercaseAndDigitVariableName(t *testing.T) {
	out, err := RenderTemplate("cluster=${ClusterCode_2026}", map[string]string{"ClusterCode_2026": "prod-sh"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "cluster=prod-sh" {
		t.Fatalf("render = %q", out)
	}
}

func TestMergePublicVariablesKeepsBuiltinsAuthoritative(t *testing.T) {
	builtin := map[string]string{"biz_date": "2026-10-30", "system_code": "CORE"}
	public := map[string]string{"biz_date": "1999-01-01", "cluster_code": "prod-sh", "system_code": "OTHER"}

	got := mergePublicVariables(builtin, public)
	if got["biz_date"] != "2026-10-30" {
		t.Fatalf("public variable overrode biz_date: %q", got["biz_date"])
	}
	if got["system_code"] != "CORE" {
		t.Fatalf("public variable overrode system_code: %q", got["system_code"])
	}
	if got["cluster_code"] != "prod-sh" {
		t.Fatalf("public variable missing: %+v", got)
	}
}
