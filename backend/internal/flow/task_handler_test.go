package flow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func newTaskListHarness(t *testing.T) (*TaskHandler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.SysUser{}, &persistence.SysRole{}, &persistence.SysUserRole{},
		&persistence.SysPermission{}, &persistence.SysRolePermission{}, &persistence.SysDataScope{},
		&persistence.CapacityMetric{}, &persistence.CollectTaskLog{},
	)

	user := persistence.SysUser{Username: "task-admin", PasswordHash: "x", RealName: "任务监控管理员", Status: "active"}
	if err := h.DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	role := persistence.SysRole{Code: "platform_admin", Name: "平台管理员", Builtin: true, Status: "active"}
	if err := h.DB.Create(&role).Error; err != nil {
		t.Fatalf("seed role: %v", err)
	}
	if err := h.DB.Create(&persistence.SysUserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("seed user role: %v", err)
	}
	az, err := middleware.NewAuthZ(h.DB)
	if err != nil {
		t.Fatalf("new authz: %v", err)
	}
	return NewTaskHandler(h.DB, az), h
}

func seedTaskLogs(t *testing.T, h *testutil.DBHarness) {
	t.Helper()
	base := time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local)
	rows := []persistence.CollectTaskLog{
		{FlowID: 1, FlowCode: "daily_pay", MetricCode: "PAY.CPU", TriggeredAt: base, Status: "success", Source: "auto"},
		{FlowID: 2, FlowCode: "core_hourly", MetricCode: "CORE.DB", TriggeredAt: base.Add(time.Minute), Status: "success", Source: "auto"},
		{FlowID: 3, FlowCode: "unrelated", MetricCode: "LATENCY.P99", TriggeredAt: base.Add(2 * time.Minute), Status: "success", Source: "auto"},
	}
	for i := range rows {
		if err := h.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed task log: %v", err)
		}
	}
}

func callTaskList(t *testing.T, handler *TaskHandler, target string) []persistence.CollectTaskLog {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Set(middleware.CtxUsername, "task-admin")
	handler.List(c)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Items []persistence.CollectTaskLog `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v %s", err, w.Body.String())
	}
	if body.Code != 0 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	return body.Data.Items
}

func TestTaskListKeywordMatchesFlowCodeOrMetricCode(t *testing.T) {
	handler, h := newTaskListHarness(t)
	seedTaskLogs(t, h)

	for _, tc := range []struct {
		name   string
		target string
		want   string
	}{
		{name: "flow code", target: "/api/v1/tasks?keyword=core_hourly", want: "core_hourly"},
		{name: "metric code", target: "/api/v1/tasks?keyword=LATENCY.P99", want: "unrelated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := callTaskList(t, handler, tc.target)
			if len(items) != 1 || items[0].FlowCode != tc.want {
				t.Fatalf("items = %+v, want only flow %q", items, tc.want)
			}
		})
	}
}

func TestTaskListMetricCodeFilter(t *testing.T) {
	handler, h := newTaskListHarness(t)
	seedTaskLogs(t, h)

	items := callTaskList(t, handler, "/api/v1/tasks?metricCode=PAY.CPU")
	if len(items) != 1 || items[0].FlowCode != "daily_pay" {
		t.Fatalf("items = %+v, want only metric PAY.CPU", items)
	}
}
