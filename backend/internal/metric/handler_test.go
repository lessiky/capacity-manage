package metric

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func i64ptr(v int64) *int64 { return &v }

func newTestHandler(t *testing.T) (*Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.MetricValueDay{}, &persistence.MetricValueRT{},
		&persistence.AlertRecord{}, &persistence.ThresholdHistory{})
	return &Handler{db: h.DB}, h
}

func callHandler(t *testing.T, h *Handler, fn func(*gin.Context), code string) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/metrics/"+code, nil)
	c.Params = gin.Params{{Key: "code", Value: code}}
	fn(c)
	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return body
}

func TestEnableMetricAndFlowCascade(t *testing.T) {
	h, db := newTestHandler(t)
	m := persistence.CapacityMetric{MetricCode: "m1", Name: "M1", SystemCode: "S1", Unit: "%", Status: "disabled"}
	fl := persistence.CollectFlow{FlowCode: "f1", MetricCode: "m1", DataSourceID: i64ptr(1), CollectType: "sql", Status: "disabled", Enabled: false}
	if err := db.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&fl).Error; err != nil {
		t.Fatal(err)
	}
	// 模拟停用指标时的联动结果: status=disabled 且 enabled=false 由 UPDATE 显式写入
	// (Create 时 Go 零值 false 会被 GORM 忽略, 落库走 DB 默认 true)
	if err := db.DB.Model(&fl).Updates(map[string]any{"status": "disabled", "enabled": false}).Error; err != nil {
		t.Fatal(err)
	}

	body := callHandler(t, h, h.Enable, "m1")
	if body.Code != resp.CodeOK {
		t.Fatalf("enable: %+v", body)
	}
	var m2 persistence.CapacityMetric
	_ = db.DB.Where("metric_code = ?", "m1").First(&m2).Error
	if m2.Status != "active" {
		t.Fatalf("status = %s", m2.Status)
	}
	var fl2 persistence.CollectFlow
	_ = db.DB.Where("flow_code = ?", "f1").First(&fl2).Error
	if fl2.Status != "active" || !fl2.Enabled {
		t.Fatalf("flow not re-enabled: %+v", fl2)
	}

	body = callHandler(t, h, h.Enable, "m1")
	if body.Code != resp.CodeBadRequest {
		t.Fatalf("re-enable should fail: %+v", body)
	}
}

func TestDeleteMetricRules(t *testing.T) {
	h, db := newTestHandler(t)
	active := persistence.CapacityMetric{MetricCode: "a1", Name: "A", SystemCode: "S1", Unit: "%", Status: "active"}
	_ = db.DB.Create(&active).Error
	body := callHandler(t, h, h.Delete, "a1")
	if body.Code != resp.CodeConflict {
		t.Fatalf("delete active: %+v", body)
	}

	fresh := persistence.CapacityMetric{MetricCode: "f1", Name: "F", SystemCode: "S1", Unit: "%", Status: "disabled"}
	_ = db.DB.Create(&fresh).Error
	body = callHandler(t, h, h.Delete, "f1")
	if body.Code != resp.CodeOK {
		t.Fatalf("delete fresh: %+v", body)
	}
	var n int64
	_ = db.DB.Model(&persistence.CapacityMetric{}).Where("metric_code = ?", "f1").Count(&n).Error
	if n != 0 {
		t.Fatal("metric not deleted")
	}

	d := persistence.CapacityMetric{MetricCode: "d1", Name: "D", SystemCode: "S1", Unit: "%", Status: "disabled"}
	_ = db.DB.Create(&d).Error
	_ = db.DB.Create(&persistence.CollectFlow{FlowCode: "fd1", MetricCode: "d1", DataSourceID: i64ptr(1), CollectType: "sql", Status: "disabled", Enabled: false}).Error
	body = callHandler(t, h, h.Delete, "d1")
	if body.Code != resp.CodeConflict {
		t.Fatalf("delete with flow: %+v", body)
	}

	body = callHandler(t, h, h.Delete, "nope")
	if body.Code != resp.CodeNotFound {
		t.Fatalf("delete missing: %+v", body)
	}
}