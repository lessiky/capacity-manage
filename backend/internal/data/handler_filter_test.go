package data

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func newFilterHandlerHarness(t *testing.T) (*Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.SysUser{}, &persistence.SysRole{}, &persistence.SysUserRole{},
		&persistence.SysPermission{}, &persistence.SysRolePermission{}, &persistence.SysDataScope{},
		&persistence.MetricValueRT{}, &persistence.MetricValueDay{}, &persistence.MetricQualityDay{},
	)
	user := persistence.SysUser{Username: "filter-admin", PasswordHash: "x", RealName: "filter-admin", Status: "active"}
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
	return &Handler{store: NewStore(h.DB), db: h.DB, az: az, params: system.NewParams(h.DB)}, h
}

func filterContext(t *testing.T, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Set(middleware.CtxUsername, "filter-admin")
	return c, w
}

func decodeArray[T any](t *testing.T, w *httptest.ResponseRecorder) []T {
	t.Helper()
	var body struct {
		Data []T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v %s", err, w.Body.String())
	}
	return body.Data
}

func decodePageItems[T any](t *testing.T, w *httptest.ResponseRecorder) []T {
	t.Helper()
	var body struct {
		Data struct {
			Items []T `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v %s", err, w.Body.String())
	}
	return body.Data.Items
}

func TestQueryDayFiltersByLevel(t *testing.T) {
	h, db := newFilterHandlerHarness(t)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	for _, row := range []persistence.MetricValueDay{
		{MetricCode: "M_DAY_FILTER", BizDate: day, Level: "normal", PointCount: 1},
		{MetricCode: "M_DAY_FILTER", BizDate: day.AddDate(0, 0, 1), Level: "warn", PointCount: 1},
		{MetricCode: "M_DAY_FILTER", BizDate: day.AddDate(0, 0, 2), Level: "missing"},
	} {
		if err := db.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed day row: %v", err)
		}
	}

	c, w := filterContext(t,
		"/api/v1/data/day?metricCodes=M_DAY_FILTER&from=2026-10-19&to=2026-10-23&level=missing&page=1")
	h.QueryDay(c)

	rows := decodePageItems[persistence.MetricValueDay](t, w)
	if len(rows) != 1 || rows[0].Level != "missing" {
		t.Fatalf("rows = %+v, want one missing row", rows)
	}
}

func TestQueryRTFiltersByJudgeLevel(t *testing.T) {
	h, db := newFilterHandlerHarness(t)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	value := 61.5
	for i, level := range []string{"normal", "warn", "danger"} {
		row := persistence.MetricValueRT{
			MetricCode: "M_RT_FILTER", Ts: day.Add(time.Duration(i) * time.Hour),
			RawValue: &value, CleanValue: &value, JudgeLevel: level,
		}
		if err := db.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed rt row: %v", err)
		}
	}

	c, w := filterContext(t,
		"/api/v1/data/rt?metricCodes=M_RT_FILTER&from=2026-10-20&to=2026-10-20&level=normal&page=1")
	h.QueryRT(c)

	rows := decodePageItems[persistence.MetricValueRT](t, w)
	if len(rows) != 1 || rows[0].JudgeLevel != "normal" {
		t.Fatalf("rows = %+v, want one normal row", rows)
	}
}

func TestListQualityFiltersByFlag(t *testing.T) {
	h, db := newFilterHandlerHarness(t)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	for i, flags := range []string{`[]`, `["no_data","missing_days"]`, `["late_days"]`} {
		row := persistence.MetricQualityDay{
			MetricCode: "M_QUALITY_FILTER", BizDate: day.AddDate(0, 0, i),
			SystemCode: "S1", Score: 0.8, Flags: flags,
		}
		if err := db.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed quality row: %v", err)
		}
	}

	for _, tc := range []struct {
		flag string
		want string
	}{
		{flag: "no_data", want: `["no_data","missing_days"]`},
		{flag: "normal", want: `[]`},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			c, w := filterContext(t,
				"/api/v1/data/quality?from=2026-10-20&to=2026-10-23&flag="+tc.flag)
			h.ListQuality(c)

			rows := decodeArray[persistence.MetricQualityDay](t, w)
			if len(rows) != 1 || rows[0].Flags != tc.want {
				t.Fatalf("rows = %+v, want one row with flags %s", rows, tc.want)
			}
		})
	}
}

// TestListQualityNormalMatchesWritePathEncoding guards against the regression
// where "normal" was filtered by flags = "[]" but the persistence path stored
// json.Marshal(nil), i.e. "null", for clean days. Selecting 正常 then returned
// no rows even though healthy data existed.
func TestListQualityNormalMatchesWritePathEncoding(t *testing.T) {
	h, db := newFilterHandlerHarness(t)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)

	// Reproduce exactly what the write path produces for a clean day.
	clean, err := json.Marshal(CalculateQuality(QualityInput{
		RangeDays: 1, ValidDays: 1, TimelyDays: 1, TotalPoints: 3,
		LatestBizDate: day, RangeEnd: day,
	}).Flags)
	if err != nil {
		t.Fatalf("marshal clean flags: %v", err)
	}
	if string(clean) != "null" && string(clean) != "[]" {
		t.Fatalf("clean flags = %s, want a no-flag encoding", clean)
	}

	seed := []struct {
		code  string
		flags string
	}{
		{code: "M_Q_NULL", flags: `null`},   // legacy rows written before normalisation
		{code: "M_Q_EMPTY", flags: `[]`},    // default column value
		{code: "M_Q_WRITE", flags: string(clean)}, // current write path
	}
	for i, s := range seed {
		row := persistence.MetricQualityDay{
			MetricCode: s.code, BizDate: day.AddDate(0, 0, i),
			SystemCode: "S1", Score: 1, Flags: s.flags,
		}
		if err := db.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed quality row %s: %v", s.code, err)
		}
	}

	c, w := filterContext(t,
		"/api/v1/data/quality?from=2026-10-20&to=2026-10-23&flag=normal")
	h.ListQuality(c)

	rows := decodeArray[persistence.MetricQualityDay](t, w)
	if len(rows) != len(seed) {
		t.Fatalf("rows = %+v, want %d normal rows", rows, len(seed))
	}
}
