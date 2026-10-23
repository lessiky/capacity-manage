package alert

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func newDashboardHarness(t *testing.T, username string, admin bool) (*Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.SysUser{}, &persistence.SysRole{}, &persistence.SysUserRole{},
		&persistence.SysRolePermission{}, &persistence.SysPermission{}, &persistence.SysDataScope{},
		&persistence.BusinessSystem{}, &persistence.AlertRecord{}, &persistence.AlertDashboardPreference{},
	)
	user := persistence.SysUser{Username: username, PasswordHash: "x", RealName: username, Status: "active"}
	if err := h.DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if admin {
		role := persistence.SysRole{Code: "platform_admin", Name: "平台管理员", Builtin: true, Status: "active"}
		if err := h.DB.Create(&role).Error; err != nil {
			t.Fatalf("seed role: %v", err)
		}
		if err := h.DB.Create(&persistence.SysUserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
			t.Fatalf("seed user role: %v", err)
		}
	}
	az, err := middleware.NewAuthZ(h.DB)
	if err != nil {
		t.Fatalf("new authz: %v", err)
	}
	handler := NewHandler(h.DB, az, system.NewParams(h.DB), nil)
	handler.now = func() time.Time {
		return time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	}
	return handler, h
}

func dashboardContext(t *testing.T, method, target, username string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	c.Request = httptest.NewRequest(method, target, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.CtxUserID, int64(1))
	c.Set(middleware.CtxUsername, username)
	return c, w
}

func dashboardBody(t *testing.T, w *httptest.ResponseRecorder) resp.Body {
	t.Helper()
	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v %s", err, w.Body.String())
	}
	return body
}

func TestDashboardPreferenceDefaultsAndRoundTrip(t *testing.T) {
	h, db := newDashboardHarness(t, "admin", true)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard-preference", "admin", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.DashboardPreference(c)
	first := dashboardBody(t, w)
	if first.Code != resp.CodeOK {
		t.Fatalf("get default: %+v", first)
	}

	c, w = dashboardContext(t, http.MethodPut, "/api/v1/alerts/dashboard-preference", "admin", map[string]any{
		"levels":      []string{"p1", "p2"},
		"statuses":    []string{"active"},
		"alertTypes":  []string{"threshold"},
		"systemCodes": []string{},
		"dateMode":    "recent_days",
		"recentDays":  14,
		"sortRules": []map[string]string{
			{"field": "level", "direction": "asc"},
			{"field": "triggerAt", "direction": "desc"},
		},
	})
	c.Set(middleware.CtxUserID, user.ID)
	h.SaveDashboardPreference(c)
	if got := dashboardBody(t, w); got.Code != resp.CodeOK {
		t.Fatalf("put preference: %+v", got)
	}

	c, w = dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard-preference", "admin", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.DashboardPreference(c)
	again := dashboardBody(t, w)
	if again.Code != resp.CodeOK {
		t.Fatalf("get saved: %+v", again)
	}
	raw, _ := json.Marshal(again.Data)
	if !bytes.Contains(raw, []byte(`"recentDays":14`)) || !bytes.Contains(raw, []byte(`"level"`)) {
		t.Fatalf("saved preference not returned: %s", raw)
	}
}

func TestSaveDashboardPreferenceRejectsSystemOutsidePermission(t *testing.T) {
	h, db := newDashboardHarness(t, "ops", false)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "ops").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_A", Name: "A", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_B", Name: "B", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.SysDataScope{UserID: &user.ID, SystemCode: "SYS_A", ScopeType: "view"}).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodPut, "/api/v1/alerts/dashboard-preference", "ops", map[string]any{
		"systemCodes": []string{"SYS_B"},
		"dateMode":    "today",
		"recentDays":  7,
	})
	c.Set(middleware.CtxUserID, user.ID)
	h.SaveDashboardPreference(c)
	if got := dashboardBody(t, w); got.Code != resp.CodeBadRequest {
		t.Fatalf("save outside scope = %+v", got)
	}
}

func TestDashboardOptionsReturnsOnlyVisibleSystems(t *testing.T) {
	h, db := newDashboardHarness(t, "ops", false)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "ops").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"SYS_A", "SYS_B"} {
		if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: code, Name: code, Owner: "ops", Status: "normal"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DB.Create(&persistence.SysDataScope{UserID: &user.ID, SystemCode: "SYS_A", ScopeType: "view"}).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard-options", "ops", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.DashboardOptions(c)
	body := dashboardBody(t, w)
	raw, _ := json.Marshal(body.Data)
	if !bytes.Contains(raw, []byte(`"SYS_A"`)) || bytes.Contains(raw, []byte(`"SYS_B"`)) {
		t.Fatalf("options = %s", raw)
	}
}

func TestDashboardAppliesMultiFilterAndMultiSort(t *testing.T) {
	h, db := newDashboardHarness(t, "admin", true)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	rows := []persistence.AlertRecord{
		{AlertNo: "A1", MetricCode: "M1", SystemCode: "SYS_A", Level: "p2", AlertType: "threshold", Status: "active", TriggerAt: base.Add(-2 * time.Hour)},
		{AlertNo: "A2", MetricCode: "M1", SystemCode: "SYS_A", Level: "p1", AlertType: "threshold", Status: "active", TriggerAt: base.Add(-1 * time.Hour)},
		{AlertNo: "A3", MetricCode: "M1", SystemCode: "SYS_A", Level: "p1", AlertType: "threshold", Status: "active", TriggerAt: base.Add(-3 * time.Hour)},
		{AlertNo: "A4", MetricCode: "M1", SystemCode: "SYS_A", Level: "p1", AlertType: "missing", Status: "active", TriggerAt: base},
		{AlertNo: "A5", MetricCode: "M1", SystemCode: "SYS_B", Level: "p1", AlertType: "threshold", Status: "active", TriggerAt: base},
	}
	for i := range rows {
		rows[i].DurationMin = intPtr(0)
		if err := db.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed alert: %v", err)
		}
	}
	if err := db.DB.Save(&persistence.AlertDashboardPreference{
		UserID: user.ID, Levels: persistence.StringList{"p1", "p2"},
		Statuses: persistence.StringList{"active"}, AlertTypes: persistence.StringList{"threshold"},
		SystemCodes: persistence.StringList{"SYS_A"}, DateMode: "today", RecentDays: 7,
		SortRules: persistence.AlertDashboardSortRules{
			{Field: "level", Direction: "asc"},
			{Field: "triggerAt", Direction: "desc"},
		},
	}).Error; err != nil {
		t.Fatalf("save preference: %v", err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard?page=1&pageSize=20", "admin", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.Dashboard(c)
	body := dashboardBody(t, w)
	if body.Code != resp.CodeOK {
		t.Fatalf("dashboard: %+v", body)
	}
	raw, _ := json.Marshal(body.Data)
	var page struct {
		Items []persistence.AlertRecord `json:"items"`
		Total int64                     `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("total = %d, want 3", page.Total)
	}
	if page.Items[0].AlertNo != "A2" || page.Items[1].AlertNo != "A3" || page.Items[2].AlertNo != "A1" {
		t.Fatalf("order = [%s %s %s]", page.Items[0].AlertNo, page.Items[1].AlertNo, page.Items[2].AlertNo)
	}
}

func TestDashboardReturnsEmptyWhenSavedSystemsAreAllRevoked(t *testing.T) {
	h, db := newDashboardHarness(t, "ops", false)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "ops").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_A", Name: "A", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_B", Name: "B", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.SysDataScope{UserID: &user.ID, SystemCode: "SYS_B", ScopeType: "view"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Save(&persistence.AlertDashboardPreference{
		UserID: user.ID, Levels: persistence.StringList{"p1"},
		Statuses: persistence.StringList{"active"}, AlertTypes: persistence.StringList{"threshold"},
		SystemCodes: persistence.StringList{"SYS_A"}, DateMode: "today", RecentDays: 7,
		SortRules: persistence.AlertDashboardSortRules{{Field: "triggerAt", Direction: "desc"}},
	}).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard?page=1&pageSize=20", "ops", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.Dashboard(c)
	body := dashboardBody(t, w)
	raw, _ := json.Marshal(body.Data)
	var page struct {
		Items []persistence.AlertRecord `json:"items"`
		Total int64                     `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("revoked scope returned rows: %+v", page)
	}
}
