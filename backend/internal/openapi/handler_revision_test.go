package openapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func callOpenAPIHandler(
	t *testing.T,
	fn func(*gin.Context),
	method string,
	target string,
	params gin.Params,
) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	c.Params = params
	fn(c)

	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v, body=%s", err, w.Body.String())
	}
	return body
}

func decodeOpenAPIData[T any](t *testing.T, body resp.Body) T {
	t.Helper()
	raw, err := json.Marshal(body.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response data: %v, data=%s", err, string(raw))
	}
	return out
}

func TestDeleteKeyRequiresDisabledStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := testutil.NewDBHarness(t)
	harness.Migrate(&persistence.APIKey{})
	h := &Handler{db: harness.DB}

	active := persistence.APIKey{Name: "active", KeyID: "cap_active", KeyHash: "hash", Status: "active"}
	disabled := persistence.APIKey{Name: "disabled", KeyID: "cap_disabled", KeyHash: "hash", Status: "disabled"}
	if err := harness.DB.Create(&active).Error; err != nil {
		t.Fatalf("create active key: %v", err)
	}
	if err := harness.DB.Create(&disabled).Error; err != nil {
		t.Fatalf("create disabled key: %v", err)
	}

	body := callOpenAPIHandler(t, h.DeleteKey, http.MethodDelete,
		"/api/v1/open-api/keys/"+strconv.FormatInt(active.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(active.ID, 10)}})
	if body.Code != resp.CodeConflict {
		t.Fatalf("deleting an active key should conflict: %+v", body)
	}
	var activeCount int64
	if err := harness.DB.Model(&persistence.APIKey{}).Where("id = ?", active.ID).Count(&activeCount).Error; err != nil {
		t.Fatalf("count active key: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active key should remain, count=%d", activeCount)
	}

	body = callOpenAPIHandler(t, h.DeleteKey, http.MethodDelete,
		"/api/v1/open-api/keys/"+strconv.FormatInt(disabled.ID, 10),
		gin.Params{{Key: "id", Value: strconv.FormatInt(disabled.ID, 10)}})
	if body.Code != resp.CodeOK {
		t.Fatalf("deleting a disabled key should succeed: %+v", body)
	}
	var disabledCount int64
	if err := harness.DB.Model(&persistence.APIKey{}).Where("id = ?", disabled.ID).Count(&disabledCount).Error; err != nil {
		t.Fatalf("count disabled key: %v", err)
	}
	if disabledCount != 0 {
		t.Fatalf("disabled key should be deleted, count=%d", disabledCount)
	}
}

func TestListDerivedMetricsPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := testutil.NewDBHarness(t)
	harness.Migrate(&persistence.DerivedMetric{})
	h := &Handler{db: harness.DB}

	base := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	for i, row := range []persistence.DerivedMetric{
		{MetricCode: "dm1", Name: "oldest", Expression: "A + B", SourceMetrics: `["A","B"]`, Status: "active", CreatedAt: base},
		{MetricCode: "dm2", Name: "middle", Expression: "A + B", SourceMetrics: `["A","B"]`, Status: "active", CreatedAt: base.Add(time.Minute)},
		{MetricCode: "dm3", Name: "newest", Expression: "A + B", SourceMetrics: `["A","B"]`, Status: "active", CreatedAt: base.Add(2 * time.Minute)},
	} {
		if err := harness.DB.Create(&row).Error; err != nil {
			t.Fatalf("create derived metric %d: %v", i, err)
		}
	}

	body := callOpenAPIHandler(t, h.ListDerivedMetrics, http.MethodGet,
		"/api/v1/open-api/derived-metrics?page=2&pageSize=2", nil)
	if body.Code != resp.CodeOK {
		t.Fatalf("list derived metrics failed: %+v", body)
	}
	page := decodeOpenAPIData[struct {
		Items    []persistence.DerivedMetric `json:"items"`
		Count    int                         `json:"count"`
		Total    int64                       `json:"total"`
		Page     int                         `json:"page"`
		PageSize int                         `json:"pageSize"`
	}](t, body)
	if page.Total != 3 || page.Count != 1 || page.Page != 2 || page.PageSize != 2 {
		t.Fatalf("unexpected pagination metadata: %+v", page)
	}
	if len(page.Items) != 1 || page.Items[0].MetricCode != "dm1" {
		t.Fatalf("page 2 should contain the oldest metric: %+v", page.Items)
	}
}

func TestListDerivedMetricsKeepsUnpagedCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := testutil.NewDBHarness(t)
	harness.Migrate(&persistence.DerivedMetric{})
	h := &Handler{db: harness.DB}

	for _, code := range []string{"dm1", "dm2", "dm3"} {
		row := persistence.DerivedMetric{
			MetricCode: code, Name: code, Expression: "A + B",
			SourceMetrics: `["A","B"]`, Status: "active",
		}
		if err := harness.DB.Create(&row).Error; err != nil {
			t.Fatalf("create derived metric %s: %v", code, err)
		}
	}

	body := callOpenAPIHandler(t, h.ListDerivedMetrics, http.MethodGet,
		"/api/v1/open-api/derived-metrics", nil)
	if body.Code != resp.CodeOK {
		t.Fatalf("list derived metrics failed: %+v", body)
	}
	got := decodeOpenAPIData[struct {
		Items []persistence.DerivedMetric `json:"items"`
		Count int                         `json:"count"`
	}](t, body)
	if got.Count != 3 || len(got.Items) != 3 {
		t.Fatalf("unpaged compatibility response should contain all rows: %+v", got)
	}
}
