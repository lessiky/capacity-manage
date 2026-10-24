package bizsystem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func i64ptr(v int64) *int64 { return &v }

func sptr(v string) *string { return &v }

func newTestHandler(t *testing.T) (*Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.BusinessSystemResource{},
		&persistence.CapacityMetric{}, &persistence.CollectFlow{}, &persistence.CollectTaskLog{},
		&persistence.CleaningTrace{}, &persistence.ThresholdHistory{}, &persistence.MetricValueRT{},
		&persistence.MetricValueDay{}, &persistence.MetricPeakStat{}, &persistence.DataCorrection{},
		&persistence.AlertRecord{}, &persistence.DataSource{}, &persistence.DataSourceCategory{},
		&persistence.SysDataScope{})
	return &Handler{db: h.DB}, h
}

func callDelete(t *testing.T, h *Handler, code string) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/systems/"+code, nil)
	c.Params = gin.Params{{Key: "code", Value: code}}
	h.Delete(c)
	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return body
}

func mustCreate(t *testing.T, db *testutil.DBHarness, v any) {
	t.Helper()
	if err := db.DB.Create(v).Error; err != nil {
		t.Fatalf("create %T: %v", v, err)
	}
}

func callResources(t *testing.T, h *Handler, code, method, body string) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/v1/systems/"+code+"/resources", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "code", Value: code}}
	if method == http.MethodGet {
		h.ListResources(c)
	} else {
		h.SaveResources(c)
	}
	var out resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return out
}

// 前端资源表单按 resType/address 读写, 字段名一旦漂移会静默丢数据。
func TestResourcesJSONContractUsesResTypeAndAddress(t *testing.T) {
	h, db := newTestHandler(t)
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S1", Name: "系统1", Owner: "u", Status: "normal"})

	saved := callResources(t, h, "S1", http.MethodPut,
		`{"items":[{"resType":"db","name":"核心库","address":"10.0.0.1:5432","remark":"主库"}]}`)
	if saved.Code != resp.CodeOK {
		t.Fatalf("save resources: %+v", saved)
	}

	listed := callResources(t, h, "S1", http.MethodGet, "")
	if listed.Code != resp.CodeOK {
		t.Fatalf("list resources: %+v", listed)
	}
	items, ok := listed.Data.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("want 1 resource, got %#v", listed.Data)
	}
	row, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected item shape: %#v", items[0])
	}
	if row["resType"] != "db" || row["address"] != "10.0.0.1:5432" || row["name"] != "核心库" {
		t.Fatalf("resource json contract broken: %#v", row)
	}
	if _, bad := row["res_type"]; bad {
		t.Fatalf("json should use camelCase resType: %#v", row)
	}
}

func TestDeleteSystemRequiresOffline(t *testing.T) {
	h, db := newTestHandler(t)
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S1", Name: "系统1", Owner: "u", Status: "normal"})
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S2", Name: "系统2", Owner: "u", Status: "maintenance"})

	for _, code := range []string{"S1", "S2"} {
		if body := callDelete(t, h, code); body.Code != resp.CodeConflict {
			t.Fatalf("delete %s should conflict: %+v", code, body)
		}
	}
	if body := callDelete(t, h, "NOPE"); body.Code != resp.CodeNotFound {
		t.Fatalf("delete missing should 404: %+v", body)
	}
}

func TestDeleteSystemCascadesOwnedData(t *testing.T) {
	h, db := newTestHandler(t)
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S1", Name: "系统1", Owner: "u", Status: "offline"})

	cat := persistence.DataSourceCategory{SystemCode: "S1", Name: "日志分类"}
	mustCreate(t, db, &cat)
	ds := persistence.DataSource{Name: "日志源", Type: "log", SystemCode: "S1", CategoryID: i64ptr(cat.ID)}
	mustCreate(t, db, &ds)
	mustCreate(t, db, &persistence.BusinessSystemResource{SystemCode: "S1", ResType: "server", Name: "srv"})
	mustCreate(t, db, &persistence.SysDataScope{UserID: i64ptr(1), SystemCode: "S1", ScopeType: "manage"})

	m := persistence.CapacityMetric{MetricCode: "m1", Name: "M1", SystemCode: "S1", Unit: "%", Status: "disabled"}
	mustCreate(t, db, &m)
	child := persistence.CapacityMetric{
		MetricCode: "m2", Name: "M2", SystemCode: "S1", Unit: "%", Status: "disabled", ParentCode: sptr("m1"),
	}
	mustCreate(t, db, &child)
	fl := persistence.CollectFlow{
		FlowCode: "f1", MetricCode: "m1", DataSourceID: i64ptr(ds.ID),
		CollectType: "sql", Status: "disabled", Enabled: false,
	}
	mustCreate(t, db, &fl)
	mustCreate(t, db, &persistence.CollectTaskLog{FlowID: fl.ID, FlowCode: "f1", MetricCode: "m1"})
	mustCreate(t, db, &persistence.ThresholdHistory{MetricCode: "m1", Operator: "u"})
	mustCreate(t, db, &persistence.MetricValueDay{MetricCode: "m1"})
	mustCreate(t, db, &persistence.MetricPeakStat{MetricCode: "m1"})
	mustCreate(t, db, &persistence.DataCorrection{MetricCode: "m1", Field: "value"})
	mustCreate(t, db, &persistence.AlertRecord{AlertNo: "a1", MetricCode: "m1", SystemCode: "S1", Level: "p1", AlertType: "threshold"})

	// 其它系统的数据不得被牵连
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S9", Name: "系统9", Owner: "u", Status: "offline"})
	keepMetric := persistence.CapacityMetric{MetricCode: "m9", Name: "M9", SystemCode: "S9", Unit: "%", Status: "disabled"}
	mustCreate(t, db, &keepMetric)
	keepDs := persistence.DataSource{Name: "他源", Type: "db", SystemCode: "S9"}
	mustCreate(t, db, &keepDs)
	mustCreate(t, db, &persistence.AlertRecord{AlertNo: "a9", MetricCode: "m9", SystemCode: "S9", Level: "p1", AlertType: "threshold"})

	if body := callDelete(t, h, "S1"); body.Code != resp.CodeOK {
		t.Fatalf("delete offline system: %+v", body)
	}

	gone := []struct {
		model any
		where string
		arg   any
	}{
		{&persistence.BusinessSystem{}, "system_code = ?", "S1"},
		{&persistence.BusinessSystemResource{}, "system_code = ?", "S1"},
		{&persistence.SysDataScope{}, "system_code = ?", "S1"},
		{&persistence.CapacityMetric{}, "system_code = ?", "S1"},
		{&persistence.CollectFlow{}, "metric_code = ?", "m1"},
		{&persistence.CollectTaskLog{}, "metric_code = ?", "m1"},
		{&persistence.ThresholdHistory{}, "metric_code = ?", "m1"},
		{&persistence.MetricValueDay{}, "metric_code = ?", "m1"},
		{&persistence.MetricPeakStat{}, "metric_code = ?", "m1"},
		{&persistence.DataCorrection{}, "metric_code = ?", "m1"},
		{&persistence.AlertRecord{}, "system_code = ?", "S1"},
		{&persistence.DataSource{}, "system_code = ?", "S1"},
		{&persistence.DataSourceCategory{}, "system_code = ?", "S1"},
	}
	for _, g := range gone {
		var n int64
		_ = db.DB.Model(g.model).Where(g.where, g.arg).Count(&n).Error
		if n != 0 {
			t.Fatalf("%T should be deleted, left %d", g.model, n)
		}
	}

	for _, keep := range []struct {
		model any
		where string
		arg   any
	}{
		{&persistence.BusinessSystem{}, "system_code = ?", "S9"},
		{&persistence.CapacityMetric{}, "metric_code = ?", "m9"},
		{&persistence.DataSource{}, "system_code = ?", "S9"},
		{&persistence.AlertRecord{}, "system_code = ?", "S9"},
	} {
		var n int64
		_ = db.DB.Model(keep.model).Where(keep.where, keep.arg).Count(&n).Error
		if n != 1 {
			t.Fatalf("%T of other system must remain, got %d", keep.model, n)
		}
	}
}

func TestDeleteSystemBlockedByExternalReferences(t *testing.T) {
	h, db := newTestHandler(t)
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S1", Name: "系统1", Owner: "u", Status: "offline"})
	mustCreate(t, db, &persistence.BusinessSystem{SystemCode: "S9", Name: "系统9", Owner: "u", Status: "offline"})

	cat := persistence.DataSourceCategory{SystemCode: "S1", Name: "日志分类"}
	mustCreate(t, db, &cat)
	ds := persistence.DataSource{Name: "日志源", Type: "log", SystemCode: "S1", CategoryID: i64ptr(cat.ID)}
	mustCreate(t, db, &ds)
	mustCreate(t, db, &persistence.CapacityMetric{MetricCode: "m1", Name: "M1", SystemCode: "S1", Unit: "%", Status: "disabled"})
	mustCreate(t, db, &persistence.CapacityMetric{MetricCode: "m9", Name: "M9", SystemCode: "S9", Unit: "%", Status: "disabled"})

	// 其它系统的流程引用了本系统的数据源 → 拒绝
	fl := persistence.CollectFlow{
		FlowCode: "f9", MetricCode: "m9", DataSourceID: i64ptr(ds.ID), CollectType: "sql", Status: "disabled",
	}
	mustCreate(t, db, &fl)
	if body := callDelete(t, h, "S1"); body.Code != resp.CodeConflict {
		t.Fatalf("external data source reference should block: %+v", body)
	}
	if err := db.DB.Delete(&fl).Error; err != nil {
		t.Fatal(err)
	}

	// 其它系统的指标以本系统指标为父/加权 → 拒绝
	child := persistence.CapacityMetric{
		MetricCode: "m9c", Name: "M9C", SystemCode: "S9", Unit: "%", Status: "disabled", ParentCode: sptr("m1"),
	}
	mustCreate(t, db, &child)
	if body := callDelete(t, h, "S1"); body.Code != resp.CodeConflict {
		t.Fatalf("external metric reference should block: %+v", body)
	}
	if err := db.DB.Delete(&child).Error; err != nil {
		t.Fatal(err)
	}

	if body := callDelete(t, h, "S1"); body.Code != resp.CodeOK {
		t.Fatalf("delete should succeed after references removed: %+v", body)
	}
}
