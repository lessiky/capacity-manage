package metric

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

// newTreeTestHandler 带数据权限(平台管理员)的测试 handler
func newTreeTestHandler(t *testing.T) (*Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.BusinessSystem{},
		&persistence.MetricValueDay{}, &persistence.MetricValueRT{},
		&persistence.SysUser{}, &persistence.SysRole{}, &persistence.SysUserRole{},
		&persistence.SysRolePermission{}, &persistence.SysPermission{})
	if err := h.DB.Create(&persistence.SysRole{Code: "platform_admin", Name: "平台管理员", Builtin: true, Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.SysUser{Username: "admin", PasswordHash: "x", RealName: "管理员", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	var u persistence.SysUser
	if err := h.DB.Where("username = ?", "admin").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	var r persistence.SysRole
	if err := h.DB.Where("code = ?", "platform_admin").First(&r).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.SysUserRole{UserID: u.ID, RoleID: r.ID}).Error; err != nil {
		t.Fatal(err)
	}
	az, err := middleware.NewAuthZ(h.DB)
	if err != nil {
		t.Fatalf("new authz: %v", err)
	}
	return &Handler{db: h.DB, az: az}, h
}

// dataJSON 将响应 Data(已反序列化为 map) 重新序列化为 JSON 供强类型解析
func dataJSON(t *testing.T, b resp.Body) []byte {
	t.Helper()
	raw, err := json.Marshal(b.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	return raw
}

func createMetricReq(t *testing.T, h *Handler, payload map[string]any) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(payload)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.CtxUsername, "admin")
	h.Create(c)
	var b resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return b
}

func callGet(t *testing.T, h *Handler, fn func(*gin.Context), params gin.Params, rawQuery string) resp.Body {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	rq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	if rawQuery != "" {
		rq.URL.RawQuery = rawQuery
	}
	c.Request = rq
	c.Params = params
	c.Set(middleware.CtxUsername, "admin")
	fn(c)
	var b resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("response not json: %v %s", err, w.Body.String())
	}
	return b
}

func fptr(v float64) *float64 { return &v }

// TestCreateAggregateTreeValidation 汇总指标树形创建校验
func TestCreateAggregateTreeValidation(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S2", Name: "系统二", Owner: "admin", Status: "normal"}).Error

	// 汇总指标(无需阈值)
	b := createMetricReq(t, h, map[string]any{
		"metricCode": "SRV", "name": "服务请求", "systemCode": "S1",
		"metricKind": "aggregate", "unit": "-",
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create aggregate: %+v", b)
	}
	// 子指标分组(父必须是汇总)
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.ORD", "name": "订单服务请求", "systemCode": "S1",
		"metricKind": "group", "parentCode": "SRV", "unit": "-",
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create group: %+v", b)
	}
	// 分组无父
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "G1", "name": "无父分组", "systemCode": "S1",
		"metricKind": "group", "unit": "-",
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("group without parent should fail: %+v", b)
	}
	// 分组的父是分组
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "G2", "name": "子分组", "systemCode": "S1",
		"metricKind": "group", "parentCode": "SRV.ORD", "unit": "-",
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("group under group should fail: %+v", b)
	}
	// 统计模块挂在分组下, 必须带 statModule
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.ORD.COUNT", "name": "请求量", "systemCode": "S1",
		"parentCode": "SRV.ORD", "unit": "笔", "statMode": "sum",
		"thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("module without statModule should fail: %+v", b)
	}
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.ORD.COUNT", "name": "请求量", "systemCode": "S1",
		"parentCode": "SRV.ORD", "statModule": "count", "unit": "笔", "statMode": "sum",
		"thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create module: %+v", b)
	}
	// 普通指标不能作为父
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.ORD.MAX", "name": "最大耗时", "systemCode": "S1",
		"parentCode": "SRV.ORD.COUNT", "statModule": "max_latency", "unit": "ms", "statMode": "max",
		"thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("normal under normal should fail: %+v", b)
	}
	// 跨系统父指标
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "X.G", "name": "跨系统分组", "systemCode": "S2",
		"metricKind": "group", "parentCode": "SRV", "unit": "-",
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("cross-system parent should fail: %+v", b)
	}
	// 汇总指标不能有父
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "A2", "name": "子汇总", "systemCode": "S1",
		"metricKind": "aggregate", "parentCode": "SRV", "unit": "-",
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("aggregate with parent should fail: %+v", b)
	}
	// 同名模块允许挂在不同分组下
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.PAY", "name": "支付服务请求", "systemCode": "S1",
		"metricKind": "group", "parentCode": "SRV", "unit": "-",
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("create group pay: %+v", b)
	}
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "SRV.PAY.COUNT", "name": "请求量", "systemCode": "S1",
		"parentCode": "SRV.PAY", "statModule": "count", "unit": "笔", "statMode": "sum",
		"thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("same-name module under another group should pass: %+v", b)
	}
}

// TestMetricTree 指标树结构
func TestMetricTree(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	for _, p := range []map[string]any{
		{"metricCode": "SRV", "name": "服务请求", "systemCode": "S1", "metricKind": "aggregate", "unit": "-"},
		{"metricCode": "SRV.ORD", "name": "订单服务请求", "systemCode": "S1", "metricKind": "group", "parentCode": "SRV", "unit": "-"},
		{"metricCode": "SRV.PAY", "name": "支付服务请求", "systemCode": "S1", "metricKind": "group", "parentCode": "SRV", "unit": "-"},
	} {
		if b := createMetricReq(t, h, p); b.Code != resp.CodeOK {
			t.Fatalf("create %s: %+v", p["metricCode"], b)
		}
	}
	for _, mc := range []string{"SRV.ORD.COUNT", "SRV.ORD.MAX", "SRV.ORD.AVG", "SRV.PAY.COUNT", "SRV.PAY.MAX", "SRV.PAY.AVG"} {
		mod := "count"
		if mc[len(mc)-4:] == ".MAX" {
			mod = "max_latency"
		} else if mc[len(mc)-4:] == ".AVG" {
			mod = "avg_latency"
		}
		p := map[string]any{
			"metricCode": mc, "name": "模块", "systemCode": "S1",
			"parentCode": mc[:strings.LastIndex(mc, ".")], "statModule": mod,
			"unit": "笔", "statMode": "sum", "thresholdMode": "absolute", "warnLine": 1,
		}
		if b := createMetricReq(t, h, p); b.Code != resp.CodeOK {
			t.Fatalf("create %s: %+v", mc, b)
		}
	}
	// 独立指标
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "CPU", "name": "CPU使用率", "systemCode": "S1",
		"unit": "%", "statMode": "latest", "thresholdMode": "percent", "warnLine": 80,
	}); b.Code != resp.CodeOK {
		t.Fatalf("create standalone: %+v", b)
	}

	b := callGet(t, h, h.Tree, nil, "")
	if b.Code != resp.CodeOK {
		t.Fatalf("tree: %+v", b)
	}
	var tree struct {
		Roots []struct {
			MetricCode string `json:"metricCode"`
			MetricKind string `json:"metricKind"`
			Children   []struct {
				MetricCode string `json:"metricCode"`
				Children   []struct {
					MetricCode string `json:"metricCode"`
				} `json:"children"`
			} `json:"children"`
		} `json:"roots"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &tree); err != nil {
		t.Fatalf("tree data: %v %s", err, b.Data)
	}
	if tree.Count != 10 {
		t.Fatalf("count = %d", tree.Count)
	}
	if len(tree.Roots) != 2 {
		t.Fatalf("roots = %d", len(tree.Roots))
	}
	// 汇总指标排在独立指标之前
	if tree.Roots[0].MetricCode != "SRV" || tree.Roots[0].MetricKind != "aggregate" {
		t.Fatalf("first root = %+v", tree.Roots[0])
	}
	if len(tree.Roots[0].Children) != 2 {
		t.Fatalf("srv children = %d", len(tree.Roots[0].Children))
	}
	if len(tree.Roots[0].Children[0].Children) != 3 {
		t.Fatalf("group children = %d", len(tree.Roots[0].Children[0].Children))
	}
	// 系统过滤
	b = callGet(t, h, h.Tree, nil, url.Values{"systemCode": {"S2"}}.Encode())
	if b.Code != resp.CodeOK {
		t.Fatalf("tree filter: %+v", b)
	}
	var tree2 struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(dataJSON(t, b), &tree2)
	if tree2.Count != 0 {
		t.Fatalf("filtered count = %d", tree2.Count)
	}
}

// seedDayValues 生成模块演示日数据
func seedDayValues(t *testing.T, db *testutil.DBHarness, code string, days []float64) {
	t.Helper()
	today := time.Now().Truncate(24 * time.Hour)
	for i := range days {
		row := persistence.MetricValueDay{
			MetricCode: code, BizDate: today.AddDate(0, 0, -(len(days) - 1 - i)),
			StatValue: fptr(days[i]), Level: "normal", PointCount: 10,
		}
		if err := db.DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestModulesAggregationDay(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate", Unit: "-", Status: "active",
	}).Error
	for _, g := range []struct{ code, name string }{
		{"SRV.ORD", "订单服务请求"}, {"SRV.PAY", "支付服务请求"},
	} {
		pc := "SRV"
		_ = db.DB.Create(&persistence.CapacityMetric{
			MetricCode: g.code, Name: g.name, SystemCode: "S1", MetricKind: "group",
			ParentCode: &pc, Unit: "-", Status: "active",
		}).Error
	}
	type mod struct{ code, name, module, unit, statMode string }
	mods := []mod{
		{"SRV.ORD.COUNT", "请求量", "count", "笔", "sum"},
		{"SRV.ORD.MAX", "最大耗时", "max_latency", "ms", "max"},
		{"SRV.ORD.AVG", "平均耗时", "avg_latency", "ms", "avg"},
		{"SRV.PAY.COUNT", "请求量", "count", "笔", "sum"},
		{"SRV.PAY.MAX", "最大耗时", "max_latency", "ms", "max"},
		{"SRV.PAY.AVG", "平均耗时", "avg_latency", "ms", "avg"},
	}
	for _, m := range mods {
		pc := m.code[:strings.LastIndex(m.code, ".")]
		if err := db.DB.Create(&persistence.CapacityMetric{
			MetricCode: m.code, Name: m.name, SystemCode: "S1", MetricKind: "normal",
			ParentCode: &pc, StatModule: m.module, Unit: m.unit, StatMode: m.statMode, Status: "active",
		}).Error; err != nil {
			t.Fatalf("create module %s: %v", m.code, err)
		}
	}
	seedDayValues(t, db, "SRV.ORD.COUNT", []float64{100, 200, 300})
	seedDayValues(t, db, "SRV.PAY.COUNT", []float64{10, 20, 30})
	seedDayValues(t, db, "SRV.ORD.MAX", []float64{50, 60, 70})
	seedDayValues(t, db, "SRV.PAY.MAX", []float64{65, 55, 75})
	seedDayValues(t, db, "SRV.ORD.AVG", []float64{10, 20, 30})
	seedDayValues(t, db, "SRV.PAY.AVG", []float64{20, 20, 20})

	b := callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "SRV"}}, "granularity=day&days=30")
	if b.Code != resp.CodeOK {
		t.Fatalf("modules: %+v", b)
	}
	var data struct {
		MetricKind       string `json:"metricKind"`
		ModuleCount      int    `json:"moduleCount"`
		ChildMetricCount int    `json:"childMetricCount"`
		Modules          []struct {
			Module   string `json:"module"`
			AggMode  string `json:"aggMode"`
			Children []struct {
				Code string `json:"code"`
			} `json:"children"`
			Latest *float64 `json:"latest"`
			Series []struct {
				Value *float64 `json:"value"`
			} `json:"series"`
			Stats struct {
				Count int      `json:"count"`
				Max   *float64 `json:"max"`
				Min   *float64 `json:"min"`
				Avg   *float64 `json:"avg"`
			} `json:"stats"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &data); err != nil {
		t.Fatalf("modules data: %v %s", err, b.Data)
	}
	if data.MetricKind != "aggregate" || data.ModuleCount != 3 || data.ChildMetricCount != 6 {
		t.Fatalf("overview = %+v", data)
	}
	expect := map[string]struct {
		mode  string
		agg   []float64
		max   float64
		min   float64
		avg   float64
		nkids int
	}{
		"count":       {"sum", []float64{110, 220, 330}, 330, 110, 220, 2},
		"max_latency": {"max", []float64{65, 60, 75}, 75, 60, 66.66666666666667, 2},
		"avg_latency": {"avg", []float64{15, 20, 25}, 25, 15, 20, 2},
	}
	if len(data.Modules) != 3 {
		t.Fatalf("module count = %d", len(data.Modules))
	}
	seen := map[string]bool{}
	for _, m := range data.Modules {
		e, ok := expect[m.Module]
		if !ok {
			t.Fatalf("unexpected module %s", m.Module)
		}
		seen[m.Module] = true
		if m.AggMode != e.mode {
			t.Fatalf("module %s aggMode = %s, want %s", m.Module, m.AggMode, e.mode)
		}
		if len(m.Children) != e.nkids {
			t.Fatalf("module %s children = %d", m.Module, len(m.Children))
		}
		if len(m.Series) != len(e.agg) {
			t.Fatalf("module %s series len = %d", m.Module, len(m.Series))
		}
		for i := range e.agg {
			if *m.Series[i].Value != e.agg[i] {
				t.Fatalf("module %s series[%d] = %v, want %v", m.Module, i, *m.Series[i].Value, e.agg[i])
			}
		}
		if m.Latest == nil || *m.Latest != e.agg[len(e.agg)-1] {
			t.Fatalf("module %s latest = %v", m.Module, m.Latest)
		}
		if *m.Stats.Max != e.max || *m.Stats.Min != e.min || m.Stats.Count != len(e.agg) {
			t.Fatalf("module %s stats = %+v", m.Module, m.Stats)
		}
		if m.Stats.Avg == nil || *m.Stats.Avg < e.avg-0.001 || *m.Stats.Avg > e.avg+0.001 {
			t.Fatalf("module %s stats.avg = %v, want ~%v", m.Module, m.Stats.Avg, e.avg)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("seen modules = %v", seen)
	}

	// 子指标分组: 自身模块, aggMode=self
	b = callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "SRV.ORD"}}, "granularity=day&days=30")
	if b.Code != resp.CodeOK {
		t.Fatalf("group modules: %+v", b)
	}
	var gdata struct {
		ModuleCount int `json:"moduleCount"`
		Modules     []struct {
			Module  string `json:"module"`
			AggMode string `json:"aggMode"`
			Series  []struct {
				Value *float64 `json:"value"`
			} `json:"series"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &gdata); err != nil {
		t.Fatalf("group modules data: %v %s", err, b.Data)
	}
	if gdata.ModuleCount != 3 {
		t.Fatalf("group module count = %d", gdata.ModuleCount)
	}
	for _, m := range gdata.Modules {
		if m.AggMode != "self" {
			t.Fatalf("group module %s aggMode = %s", m.Module, m.AggMode)
		}
		if m.Module == "count" {
			if *m.Series[2].Value != 300 {
				t.Fatalf("group count series = %v", m.Series)
			}
		}
	}
	// 普通指标不支持
	b = callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "SRV.ORD.COUNT"}}, "granularity=day")
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("normal modules should 400: %+v", b)
	}
	// 不存在
	b = callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "NOPE"}}, "")
	if b.Code != resp.CodeNotFound {
		t.Fatalf("missing modules should 404: %+v", b)
	}
	// 空聚合体(无子指标)
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "EMPTY", Name: "空汇总", SystemCode: "S1", MetricKind: "aggregate", Unit: "-", Status: "active",
	}).Error
	b = callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "EMPTY"}}, "granularity=day")
	if b.Code != resp.CodeOK {
		t.Fatalf("empty aggregate should ok: %+v", b)
	}
	var edata struct {
		ModuleCount int `json:"moduleCount"`
	}
	_ = json.Unmarshal(dataJSON(t, b), &edata)
	if edata.ModuleCount != 0 {
		t.Fatalf("empty module count = %d", edata.ModuleCount)
	}
}

// TestModulesAggregationRT 日内粒度按同一时间戳对齐聚合
func TestModulesAggregationRT(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate", Unit: "-", Status: "active",
	}).Error
	base := time.Now().Truncate(time.Hour)
	type leaf struct{ code, module, statMode string }
	for _, l := range []leaf{
		{"SRV.A.COUNT", "count", "sum"},
		{"SRV.B.COUNT", "count", "sum"},
	} {
		pc := "SRV"
		_ = db.DB.Create(&persistence.CapacityMetric{
			MetricCode: l.code, Name: "请求量", SystemCode: "S1", MetricKind: "normal",
			ParentCode: &pc, StatModule: l.module, Unit: "笔", StatMode: l.statMode, Status: "active",
		}).Error
	}
	for h := 0; h < 3; h++ {
		ts := base.Add(time.Duration(h) * time.Hour)
		_ = db.DB.Create(&persistence.MetricValueRT{
			MetricCode: "SRV.A.COUNT", Ts: ts, RawValue: fptr(10), CleanValue: fptr(10), JudgeLevel: "normal",
		}).Error
		_ = db.DB.Create(&persistence.MetricValueRT{
			MetricCode: "SRV.B.COUNT", Ts: ts, RawValue: fptr(5), CleanValue: fptr(5), JudgeLevel: "normal",
		}).Error
	}
	b := callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "SRV"}}, "granularity=rt&days=7")
	if b.Code != resp.CodeOK {
		t.Fatalf("rt modules: %+v", b)
	}
	var data2 struct {
		Modules []struct {
			AggMode string `json:"aggMode"`
			Series  []struct {
				Value *float64 `json:"value"`
			} `json:"series"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &data2); err != nil {
		t.Fatalf("rt modules data2: %v %s", err, b.Data)
	}
	if len(data2.Modules) != 1 || data2.Modules[0].AggMode != "sum" {
		t.Fatalf("rt modules = %+v", data2.Modules)
	}
	if len(data2.Modules[0].Series) != 3 {
		t.Fatalf("rt series len = %d", len(data2.Modules[0].Series))
	}
	for i, sp := range data2.Modules[0].Series {
		if *sp.Value != 15 {
			t.Fatalf("rt series[%d] = %v, want 15", i, *sp.Value)
		}
	}
}

// TestDeleteAggregateWithChildren 有子指标时禁止删除
func TestDeleteAggregateWithChildren(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate", Unit: "-", Status: "disabled",
	}).Error
	pc := "SRV"
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV.ORD", Name: "订单服务请求", SystemCode: "S1", MetricKind: "group",
		ParentCode: &pc, Unit: "-", Status: "active",
	}).Error

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/metrics/SRV", nil)
	c.Params = gin.Params{{Key: "code", Value: "SRV"}}
	c.Set(middleware.CtxUsername, "admin")
	h.Delete(c)
	var b resp.Body
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if b.Code != resp.CodeConflict {
		t.Fatalf("delete aggregate with children should 409: %+v", b)
	}
}

// TestCreateWeightValidation 权重指标创建校验(须为同一父级下的同级普通指标)
func TestCreateWeightValidation(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S2", Name: "系统二", Owner: "admin", Status: "normal"}).Error
	// 树: AGX -> G1 -> {W1, W1B}
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "AGX", "name": "汇总", "systemCode": "S1", "metricKind": "aggregate", "unit": "-",
	}); b.Code != resp.CodeOK {
		t.Fatalf("create AGX: %+v", b)
	}
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "G1", "name": "分组", "systemCode": "S1", "metricKind": "group", "parentCode": "AGX", "unit": "-",
	}); b.Code != resp.CodeOK {
		t.Fatalf("create G1: %+v", b)
	}
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "W1", "name": "请求量", "systemCode": "S1", "parentCode": "G1", "statModule": "count",
		"unit": "笔", "statMode": "sum", "thresholdMode": "absolute", "warnLine": 1,
	}); b.Code != resp.CodeOK {
		t.Fatalf("create W1: %+v", b)
	}
	// 权重=自身
	b := createMetricReq(t, h, map[string]any{
		"metricCode": "M_SELF", "name": "自身权重", "systemCode": "S1", "parentCode": "G1", "statModule": "avg_latency", "weightMetricCode": "M_SELF",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("self weight should 400: %+v", b)
	}
	// 权重不存在
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "M_NOPE", "name": "无权重", "systemCode": "S1", "parentCode": "G1", "statModule": "avg_latency", "weightMetricCode": "NOPE",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("missing weight should 400: %+v", b)
	}
	// 无父级指标配置权重
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "M_NOPAR", "name": "无父级权重", "systemCode": "S1", "weightMetricCode": "W1",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("parentless weight should 400: %+v", b)
	}
	// 跨系统权重
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "W2", "name": "跨系统请求量", "systemCode": "S2",
		"unit": "笔", "statMode": "sum", "thresholdMode": "absolute", "warnLine": 1,
	}); b.Code != resp.CodeOK {
		t.Fatalf("create W2: %+v", b)
	}
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "M_XSYS", "name": "跨系统权重", "systemCode": "S1", "parentCode": "G1", "statModule": "avg_latency", "weightMetricCode": "W2",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("cross-system weight should 400: %+v", b)
	}
	// 权重必须是普通指标(不能是汇总/分组)
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "M_AGGW", "name": "权重为汇总", "systemCode": "S1", "parentCode": "G1", "statModule": "avg_latency", "weightMetricCode": "AGX",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("aggregate-as-weight should 400: %+v", b)
	}
	// 非同级(不同父级)
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "W3", "name": "其它请求量", "systemCode": "S1", "statModule": "count",
		"unit": "笔", "statMode": "sum", "thresholdMode": "absolute", "warnLine": 1,
	}); b.Code != resp.CodeOK {
		t.Fatalf("create W3: %+v", b)
	}
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "M_XPAR", "name": "跨父级权重", "systemCode": "S1", "parentCode": "G1", "statModule": "avg_latency", "weightMetricCode": "W3",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeBadRequest {
		t.Fatalf("cross-parent weight should 400: %+v", b)
	}
	// 合法: 同一父级下的同级普通指标
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "W1B", "name": "请求量B", "systemCode": "S1", "parentCode": "G1", "statModule": "count",
		"unit": "笔", "statMode": "sum", "thresholdMode": "absolute", "warnLine": 1,
	}); b.Code != resp.CodeOK {
		t.Fatalf("create W1B: %+v", b)
	}
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "M_OK", "name": "平均耗时", "systemCode": "S1", "parentCode": "G1", "statModule": "avg_latency", "weightMetricCode": "W1",
		"unit": "ms", "statMode": "avg", "thresholdMode": "absolute", "warnLine": 1,
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("valid weight should pass: %+v", b)
	}
	var m persistence.CapacityMetric
	if err := db.DB.Where("metric_code = ?", "M_OK").First(&m).Error; err != nil {
		t.Fatal(err)
	}
	if m.WeightMetricCode == nil || *m.WeightMetricCode != "W1" {
		t.Fatalf("weight_metric_code = %v, want W1", m.WeightMetricCode)
	}
}

// TestCreateAggregateThreshold 汇总指标阈值(可选)校验
func TestCreateAggregateThreshold(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	// 不带阈值: 允许
	if b := createMetricReq(t, h, map[string]any{
		"metricCode": "AG_NO", "name": "无阈值汇总", "systemCode": "S1", "metricKind": "aggregate", "unit": "-",
	}); b.Code != resp.CodeOK {
		t.Fatalf("aggregate without threshold: %+v", b)
	}
	// 合法绝对值阈值
	b := createMetricReq(t, h, map[string]any{
		"metricCode": "AG_OK", "name": "有阈值汇总", "systemCode": "S1", "metricKind": "aggregate", "unit": "-",
		"thresholdMode": "absolute", "judgeDirection": "higher_worse", "warnLine": 80.0, "dangerLine": 90.0,
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("aggregate with threshold: %+v", b)
	}
	var m persistence.CapacityMetric
	if err := db.DB.Where("metric_code = ?", "AG_OK").First(&m).Error; err != nil {
		t.Fatal(err)
	}
	if m.WarnLine == nil || *m.WarnLine != 80 || m.ThresholdMode != "absolute" || m.JudgeDirection != "higher_worse" {
		t.Fatalf("aggregate threshold = %+v", m)
	}
	// 非法: 越高越差须 warn<danger
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "AG_BAD", "name": "非法阈值", "systemCode": "S1", "metricKind": "aggregate", "unit": "-",
		"thresholdMode": "absolute", "warnLine": 90.0, "dangerLine": 80.0,
	})
	if b.Code != resp.CodeUnprocessable {
		t.Fatalf("invalid threshold should 422: %+v", b)
	}
	// 主统计模块落库
	b = createMetricReq(t, h, map[string]any{
		"metricCode": "AG_MM", "name": "主模块汇总", "systemCode": "S1", "metricKind": "aggregate", "unit": "-",
		"mainModule": "count",
	})
	if b.Code != resp.CodeOK {
		t.Fatalf("aggregate with mainModule: %+v", b)
	}
	var m2 persistence.CapacityMetric
	if err := db.DB.Where("metric_code = ?", "AG_MM").First(&m2).Error; err != nil {
		t.Fatal(err)
	}
	if m2.MainModule != "count" {
		t.Fatalf("main_module = %q, want count", m2.MainModule)
	}
}

// TestModulesWeightedAvg 加权均值: 平均耗时模块按请求量加权
func TestModulesWeightedAvg(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate", Unit: "-", Status: "active",
	}).Error
	for _, g := range []string{"SRV.ORD", "SRV.PAY"} {
		pc := "SRV"
		_ = db.DB.Create(&persistence.CapacityMetric{
			MetricCode: g, Name: "子指标", SystemCode: "S1", MetricKind: "group",
			ParentCode: &pc, Unit: "-", Status: "active",
		}).Error
	}
	type leaf struct {
		code, module, statMode, weight string
	}
	for _, l := range []leaf{
		{"SRV.ORD.COUNT", "count", "sum", ""},
		{"SRV.ORD.AVG", "avg_latency", "avg", "SRV.ORD.COUNT"},
		{"SRV.PAY.COUNT", "count", "sum", ""},
		{"SRV.PAY.AVG", "avg_latency", "avg", "SRV.PAY.COUNT"},
	} {
		pc := l.code[:strings.LastIndex(l.code, ".")]
		m := persistence.CapacityMetric{
			MetricCode: l.code, Name: "模块", SystemCode: "S1", MetricKind: "normal",
			ParentCode: func() *string { s := pc; return &s }(),
			StatModule: l.module, Unit: "x", StatMode: l.statMode, Status: "active",
		}
		if l.weight != "" {
			w := l.weight
			m.WeightMetricCode = &w
		}
		if err := db.DB.Create(&m).Error; err != nil {
			t.Fatalf("create %s: %v", l.code, err)
		}
	}
	seedDayValues(t, db, "SRV.ORD.AVG", []float64{100, 200, 300})
	seedDayValues(t, db, "SRV.PAY.AVG", []float64{200, 200, 200})
	seedDayValues(t, db, "SRV.ORD.COUNT", []float64{10, 10, 10})
	seedDayValues(t, db, "SRV.PAY.COUNT", []float64{10, 30, 60})

	b := callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "SRV"}}, "granularity=day&days=30")
	if b.Code != resp.CodeOK {
		t.Fatalf("modules: %+v", b)
	}
	var data struct {
		Modules []struct {
			Module   string `json:"module"`
			AggMode  string `json:"aggMode"`
			Children []struct {
				Code     string `json:"code"`
				Weighted bool   `json:"weighted"`
			} `json:"children"`
			Series []struct {
				Value *float64 `json:"value"`
			} `json:"series"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &data); err != nil {
		t.Fatalf("modules data: %v %s", err, b.Data)
	}
	var avgSeries, countSeries []float64
	for i := range data.Modules {
		m := &data.Modules[i]
		switch m.Module {
		case "avg_latency":
			if m.AggMode != "weighted_avg" {
				t.Fatalf("avg_latency aggMode = %s, want weighted_avg", m.AggMode)
			}
			for _, ch := range m.Children {
				if !ch.Weighted {
					t.Fatalf("avg_latency child %s should be weighted", ch.Code)
				}
			}
			for _, p := range m.Series {
				avgSeries = append(avgSeries, *p.Value)
			}
		case "count":
			if m.AggMode != "sum" {
				t.Fatalf("count aggMode = %s, want sum", m.AggMode)
			}
			for _, ch := range m.Children {
				if ch.Weighted {
					t.Fatalf("count child %s should not be weighted", ch.Code)
				}
			}
			for _, p := range m.Series {
				countSeries = append(countSeries, *p.Value)
			}
		}
	}
	// 加权均值: (100*10+200*10)/20=150; (200*10+200*30)/40=200; (300*10+200*60)/70≈214.2857
	wantAvg := []float64{150, 200, 15000.0 / 70}
	if len(avgSeries) != 3 {
		t.Fatalf("avg_latency series = %v", avgSeries)
	}
	for i, w := range wantAvg {
		d := avgSeries[i] - w
		if d < 0 {
			d = -d
		}
		if d > 0.001 {
			t.Fatalf("avg_latency series[%d] = %v, want %v", i, avgSeries[i], w)
		}
	}
	// count 模块不受影响: 普通求和 20/40/70
	wantSum := []float64{20, 40, 70}
	if len(countSeries) != 3 {
		t.Fatalf("count series = %v", countSeries)
	}
	for i, w := range wantSum {
		if countSeries[i] != w {
			t.Fatalf("count series[%d] = %v, want %v", i, countSeries[i], w)
		}
	}
}

// TestModulesChildrenLatestAndNameCollision 覆盖分支命名场景(如 A5"后台请求数"):
// 同一统计模块下各叶子取自不同分支(交易/非交易), 名称不同 → 模块名留空, 不冒用某一分支的名字;
// 每个子指标给出区间内最新值, 使分母/分子各分支都可见。
func TestModulesChildrenLatestAndNameCollision(t *testing.T) {
	h, db := newTreeTestHandler(t)
	_ = db.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = db.DB.Create(&persistence.CapacityMetric{
		MetricCode: "A5_AGG", Name: "后台请求数", SystemCode: "S1", MetricKind: "aggregate", Unit: "-", Status: "active",
	}).Error
	for _, g := range []struct{ code, name string }{
		{"A5_AGG.JY", "后台交易请求"}, {"A5_AGG.FJY", "后台非交易请求"},
	} {
		pc := "A5_AGG"
		_ = db.DB.Create(&persistence.CapacityMetric{
			MetricCode: g.code, Name: g.name, SystemCode: "S1", MetricKind: "group",
			ParentCode: &pc, Unit: "-", Status: "active",
		}).Error
	}
	for _, m := range []struct{ code, name string }{
		{"A5_AGG.JY.BS", "后台交易请求笔数"},
		{"A5_AGG.FJY.BS", "后台非交易请求笔数"},
	} {
		pc := m.code[:strings.LastIndex(m.code, ".")]
		if err := db.DB.Create(&persistence.CapacityMetric{
			MetricCode: m.code, Name: m.name, SystemCode: "S1", MetricKind: "normal",
			ParentCode: &pc, StatModule: "count", Unit: "笔", StatMode: "sum", Status: "active",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedDayValues(t, db, "A5_AGG.JY.BS", []float64{4})
	seedDayValues(t, db, "A5_AGG.FJY.BS", []float64{3838993})

	b := callGet(t, h, h.Modules, gin.Params{{Key: "code", Value: "A5_AGG"}}, "granularity=day&days=30")
	if b.Code != resp.CodeOK {
		t.Fatalf("modules: %+v", b)
	}
	var data struct {
		Modules []struct {
			Module   string   `json:"module"`
			Name     string   `json:"name"`
			AggMode  string   `json:"aggMode"`
			Latest   *float64 `json:"latest"`
			Children []struct {
				Code   string   `json:"code"`
				Unit   string   `json:"unit"`
				Latest *float64 `json:"latest"`
			} `json:"children"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(dataJSON(t, b), &data); err != nil {
		t.Fatalf("modules data: %v %s", err, b.Data)
	}
	if len(data.Modules) != 1 {
		t.Fatalf("modules = %+v", data.Modules)
	}
	m := data.Modules[0]
	if m.Module != "count" || m.AggMode != "sum" {
		t.Fatalf("module = %+v", m)
	}
	if m.Name != "" {
		t.Fatalf("branch-specific names must not become the module name, got %q", m.Name)
	}
	if m.Latest == nil || *m.Latest != 3838997 {
		t.Fatalf("module latest = %v, want 3838997", m.Latest)
	}
	if len(m.Children) != 2 {
		t.Fatalf("children = %+v", m.Children)
	}
	want := map[string]float64{"A5_AGG.JY.BS": 4, "A5_AGG.FJY.BS": 3838993}
	for _, ch := range m.Children {
		w, ok := want[ch.Code]
		if !ok {
			t.Fatalf("unexpected child %s", ch.Code)
		}
		if ch.Latest == nil || *ch.Latest != w {
			t.Fatalf("child %s latest = %v, want %v", ch.Code, ch.Latest, w)
		}
		if ch.Unit != "笔" {
			t.Fatalf("child %s unit = %q", ch.Code, ch.Unit)
		}
	}
}
