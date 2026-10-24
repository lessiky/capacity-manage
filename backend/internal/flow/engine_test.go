package flow

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/data"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
	"capacity-manage/backend/internal/tradingday"
)

// stubCollector 采集 stub: 前 fails 次返回错误, 之后成功; 记录调用与脚本清洗次数
type stubCollector struct {
	fails       int
	calls       int
	scriptCalls int
	runLog      string
	lastVars    map[string]string
}

func (s *stubCollector) Collect(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource,
	metric *persistence.CapacityMetric, vars map[string]string, base time.Time) ([]RawRow, *CollectMeta, error) {
	s.calls++
	s.lastVars = vars
	if s.calls <= s.fails {
		return nil, &CollectMeta{RunLog: s.runLog}, fmt.Errorf("模拟失败 #%d", s.calls)
	}
	return []RawRow{{Raw: "60"}}, &CollectMeta{RowsTotal: 1, RunLog: s.runLog}, nil
}

func (s *stubCollector) CleanByScript(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric,
	rows []CleanRow, ref ScriptRef, vars map[string]string) ([]CleanRow, error) {
	s.scriptCalls++
	return rows, nil
}

type engineHarness struct {
	t    *testing.T
	h    *testutil.DBHarness
	flow *persistence.CollectFlow
}

func newEngineHarness(t *testing.T, col CollectorRunner, cleanConfig string) *engineHarness {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CollectFlow{}, &persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.CollectTaskLog{}, &persistence.CleaningTrace{}, &persistence.MetricValueRT{},
		&persistence.MetricValueDay{}, &persistence.MetricPeakStat{}, &persistence.AlertRecord{},
		&persistence.TradingDayCalendar{}, &persistence.BusinessSystem{}, &persistence.SysParam{})
	// 系统参数: 重试 3 次 / 间隔 0s（测试加速）
	for _, p := range []persistence.SysParam{
		{Key: "collect_retry_max", Value: "3"},
		{Key: "collect_retry_interval_seconds", Value: "0"},
	} {
		if err := h.DB.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 交易日日历: 以今天为中心 ±20 天, 工作日为交易日
	for off := -20; off <= 20; off++ {
		d := clockDay(time.Now()).AddDate(0, 0, off)
		wd := d.Weekday()
		isTrading := wd != time.Saturday && wd != time.Sunday
		h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: d, IsTrading: isTrading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[isTrading], Source: "test",
		})
	}
	sys := persistence.BusinessSystem{SystemCode: "S1", Name: "测试系统", Status: "normal"}
	metric := persistence.CapacityMetric{
		MetricCode: "m1", Name: "测试指标", SystemCode: "S1", MetricType: "usage",
		Unit: "%", StatMode: "latest", JudgeDirection: "higher_worse",
		WarnLine: mustF(80), DangerLine: mustF(90), Status: "active",
	}
	ds := persistence.DataSource{Name: "ds1", Type: "db", Status: "available", Config: "{}"}
	if err := h.DB.Create(&ds).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&sys).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&metric).Error; err != nil {
		t.Fatal(err)
	}
	fl := &persistence.CollectFlow{
		FlowCode: "f1", Name: "F1", MetricCode: "m1", DataSourceID: &ds.ID,
		CollectType: "sql", CollectConfig: "{}", CleanConfig: cleanConfig,
		ScheduleMode: "normal", CronExpr: "0 0 2 * * ?", Status: "active", Enabled: true,
	}
	if err := h.DB.Create(fl).Error; err != nil {
		t.Fatal(err)
	}
	params := system.NewParams(h.DB)
	e := NewEngine(h.DB, data.NewStore(h.DB), judge.New(params, nil),
		alert.New(h.DB, params, nil), col, tradingday.NewCalendar(h.DB, h.RDB, params), params, slog.Default())
	_ = e
	return &engineHarness{t: t, h: h, flow: fl}
}

func clockDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func (e *engineHarness) engine(col CollectorRunner) *Engine {
	params := system.NewParams(e.h.DB)
	return NewEngine(e.h.DB, data.NewStore(e.h.DB), judge.New(params, nil),
		alert.New(e.h.DB, params, nil), col, tradingday.NewCalendar(e.h.DB, e.h.RDB, params), params, slog.Default())
}

func (e *engineHarness) task() *persistence.CollectTaskLog {
	var t persistence.CollectTaskLog
	e.t.Helper()
	if err := e.h.DB.Order("id DESC").First(&t).Error; err != nil {
		e.t.Fatalf("task log not found: %v", err)
	}
	return &t
}

func TestEngineSuccessPersistsAndTraces(t *testing.T) {
	col := &stubCollector{}
	eh := newEngineHarness(t, col, "{}")
	eng := eh.engine(col)
	res, err := eng.Run(context.Background(), eh.flow.ID, "manual", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Status != "success" {
		t.Fatalf("res = %+v", res)
	}
	if res.RetryCount != 0 || col.calls != 1 {
		t.Fatalf("retry=%d calls=%d", res.RetryCount, col.calls)
	}
	var pts int64
	eh.h.DB.Model(&persistence.MetricValueRT{}).Count(&pts)
	if pts != 1 {
		t.Fatalf("rt points = %d", pts)
	}
	task := eh.task()
	if task.Status != "success" || task.RetryCount != 0 {
		t.Fatalf("task = %+v", task)
	}
	var traces int64
	eh.h.DB.Model(&persistence.CleaningTrace{}).Count(&traces)
	if traces != 1 {
		t.Fatalf("traces = %d", traces)
	}
	var tr persistence.CleaningTrace
	eh.h.DB.First(&tr)
	if tr.RawValue != "60" || tr.CleanValue == nil || *tr.CleanValue != 60 {
		t.Fatalf("trace = %+v", tr)
	}
	if tr.MetricCode != "m1" {
		t.Fatalf("trace metric = %s", tr.MetricCode)
	}
}

func TestEngineRetryThenSuccess(t *testing.T) {
	col := &stubCollector{fails: 2}
	eh := newEngineHarness(t, col, "{}")
	eng := eh.engine(col)
	res, err := eng.Run(context.Background(), eh.flow.ID, "manual", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	if res.RetryCount != 2 || col.calls != 3 {
		t.Fatalf("retry=%d calls=%d (want 2/3)", res.RetryCount, col.calls)
	}
	task := eh.task()
	if task.Status != "success" || task.RetryCount != 2 {
		t.Fatalf("task = %+v", task)
	}
	var alerts int64
	eh.h.DB.Model(&persistence.AlertRecord{}).Count(&alerts)
	if alerts != 0 {
		t.Fatalf("unexpected alerts = %d", alerts)
	}
}

func TestEngineRetryExhaustedAlerts(t *testing.T) {
	col := &stubCollector{fails: 99, runLog: "boom\n--- stderr ---\nTraceback..."}
	eh := newEngineHarness(t, col, "{}")
	eng := eh.engine(col)
	res, err := eng.Run(context.Background(), eh.flow.ID, "auto", "scheduler")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Status != "failed" {
		t.Fatalf("res = %+v", res)
	}
	// 1 次初始 + 3 次重试
	if res.RetryCount != 3 || col.calls != 4 {
		t.Fatalf("retry=%d calls=%d (want 3/4)", res.RetryCount, col.calls)
	}
	task := eh.task()
	if task.Status != "failed" || task.RetryCount != 3 {
		t.Fatalf("task = %+v", task)
	}
	if task.RunLog == "" {
		t.Fatal("run_log not persisted")
	}
	var ds persistence.DataSource
	eh.h.DB.First(&ds)
	if ds.HealthStatus != "error" {
		t.Fatalf("ds health = %s", ds.HealthStatus)
	}
	// 采集失败 P2 告警恰好一条（防重复）
	var alerts int64
	eh.h.DB.Model(&persistence.AlertRecord{}).Where("alert_type = ?", "collect_fail").Count(&alerts)
	if alerts != 1 {
		t.Fatalf("collect_fail alerts = %d", alerts)
	}
	var pts int64
	eh.h.DB.Model(&persistence.MetricValueRT{}).Count(&pts)
	if pts != 0 {
		t.Fatalf("rt points should be 0, got %d", pts)
	}
}

func TestEnginePreviewNoSideEffects(t *testing.T) {
	col := &stubCollector{}
	eh := newEngineHarness(t, col, "{}")
	eng := eh.engine(col)
	res, err := eng.RunPreview(context.Background(), eh.flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("preview res = %+v", res)
	}
	for _, m := range []any{&persistence.MetricValueRT{}, &persistence.CollectTaskLog{}, &persistence.AlertRecord{}} {
		var n int64
		eh.h.DB.Model(m).Count(&n)
		if n != 0 {
			t.Fatalf("preview side effect: %d rows", n)
		}
	}
}

func TestEngineScriptCleanInvoked(t *testing.T) {
	col := &stubCollector{}
	eh := newEngineHarness(t, col, `{"script":{"scriptId":7,"version":1}}`)
	eng := eh.engine(col)
	res, err := eng.Run(context.Background(), eh.flow.ID, "manual", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	if col.scriptCalls != 1 {
		t.Fatalf("script clean calls = %d", col.scriptCalls)
	}
}

func TestEngineAnomalyNotPersisted(t *testing.T) {
	// 使用率 120 超域 → 标记异常 → 不落地/不判定
	col := &anomalyCollector{}
	eh := newEngineHarness(t, col, "{}")
	eng := eh.engine(col)
	res, err := eng.Run(context.Background(), eh.flow.ID, "manual", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	if res.AnomalyCount != 1 {
		t.Fatalf("anomaly = %d", res.AnomalyCount)
	}
	var pts int64
	eh.h.DB.Model(&persistence.MetricValueRT{}).Count(&pts)
	if pts != 0 {
		t.Fatalf("anomaly persisted: %d", pts)
	}
}

type anomalyCollector struct{}

func (a *anomalyCollector) Collect(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource,
	metric *persistence.CapacityMetric, vars map[string]string, base time.Time) ([]RawRow, *CollectMeta, error) {
	return []RawRow{{Raw: "120"}}, &CollectMeta{RowsTotal: 1}, nil
}

func (a *anomalyCollector) CleanByScript(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric,
	rows []CleanRow, ref ScriptRef, vars map[string]string) ([]CleanRow, error) {
	return rows, nil
}

// 回归: 预览展示与采集模板的数据日期均须按 bizDateMode 推导(此前固定取自然日, calendar/prev_day 展示错误)
func TestEnginePreviewBizDateFollowsMode(t *testing.T) {
	cases := []struct {
		mode string
		want func(today time.Time) time.Time
	}{
		{"natural", func(today time.Time) time.Time { return today }},
		{"prev_day", func(today time.Time) time.Time { return today.AddDate(0, 0, -1) }},
		{"calendar", func(today time.Time) time.Time {
			d := today.AddDate(0, 0, -1)
			for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				d = d.AddDate(0, 0, -1)
			}
			return d
		}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			col := &stubCollector{}
			eh := newEngineHarness(t, col, "{}")
			if err := eh.h.DB.Model(eh.flow).
				Updates(map[string]any{"biz_date_mode": tc.mode, "trading_market": "SH"}).Error; err != nil {
				t.Fatal(err)
			}
			res, err := eh.engine(col).RunPreview(context.Background(), eh.flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want(clockDay(time.Now())).Format("2006-01-02")
			if res.BizDate != want {
				t.Fatalf("bizDate = %s, want %s", res.BizDate, want)
			}
			if col.lastVars["biz_date"] != want {
				t.Fatalf("collector biz_date = %s, want %s", col.lastVars["biz_date"], want)
			}
		})
	}
}

// 回归: 上一交易日模式的手工采集须按业务日落地, 并立即刷新该业务日日值。
func TestEngineCalendarManualMaterializesBusinessDay(t *testing.T) {
	col := &stubCollector{}
	eh := newEngineHarness(t, col, "{}")
	if err := eh.h.DB.Model(eh.flow).Updates(map[string]any{
		"biz_date_mode": "calendar", "trading_market": "SH",
	}).Error; err != nil {
		t.Fatal(err)
	}
	wantBiz := clockDay(time.Now()).AddDate(0, 0, -1)
	for wantBiz.Weekday() == time.Saturday || wantBiz.Weekday() == time.Sunday {
		wantBiz = wantBiz.AddDate(0, 0, -1)
	}

	res, err := eh.engine(col).Run(context.Background(), eh.flow.ID, "manual", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}

	var point persistence.MetricValueRT
	if err := eh.h.DB.Where("metric_code = ?", "m1").First(&point).Error; err != nil {
		t.Fatal(err)
	}
	if !clockDay(point.Ts).Equal(wantBiz) {
		t.Fatalf("rt ts date = %s, want %s", point.Ts.Format("2006-01-02"), wantBiz.Format("2006-01-02"))
	}

	var day persistence.MetricValueDay
	if err := eh.h.DB.Where("metric_code = ? AND biz_date = ?", "m1", wantBiz).First(&day).Error; err != nil {
		t.Fatalf("day row not materialized: %v", err)
	}
	if day.StatValue == nil || *day.StatValue != 60 || day.PointCount != 1 {
		t.Fatalf("day row = stat %v points %d, want 60/1", day.StatValue, day.PointCount)
	}
}

// 补采必须指定历史业务日，并保留 source=backfill 以便审计和日汇总口径区分。
func TestEngineRunBackfillMaterializesRequestedBizDate(t *testing.T) {
	col := &stubCollector{}
	eh := newEngineHarness(t, col, "{}")
	bizDate := clockDay(time.Now()).AddDate(0, 0, -3)
	res, err := eh.engine(col).RunBackfill(context.Background(), eh.flow.ID, bizDate, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("res = %+v", res)
	}
	if col.lastVars["biz_date"] != bizDate.Format("2006-01-02") {
		t.Fatalf("collector biz_date = %s, want %s", col.lastVars["biz_date"], bizDate.Format("2006-01-02"))
	}
	var point persistence.MetricValueRT
	if err := eh.h.DB.Where("metric_code = ?", "m1").First(&point).Error; err != nil {
		t.Fatal(err)
	}
	if !clockDay(point.Ts).Equal(bizDate) {
		t.Fatalf("rt date = %s, want %s", clockDay(point.Ts).Format("2006-01-02"), bizDate.Format("2006-01-02"))
	}
	if point.Source != "backfill" {
		t.Fatalf("source = %s, want backfill", point.Source)
	}
	task := eh.task()
	if task.Source != "backfill" || task.Operator != "tester" {
		t.Fatalf("task = %+v", task)
	}
}

func TestEngineRunBackfillIsIdempotentForSameBizDate(t *testing.T) {
	col := &stubCollector{}
	eh := newEngineHarness(t, col, "{}")
	bizDate := clockDay(time.Now()).AddDate(0, 0, -3)
	for i := 0; i < 2; i++ {
		res, err := eh.engine(col).RunBackfill(context.Background(), eh.flow.ID, bizDate, "tester")
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK {
			t.Fatalf("run %d res = %+v", i+1, res)
		}
	}
	var points []persistence.MetricValueRT
	if err := eh.h.DB.Where("metric_code = ?", "m1").Find(&points).Error; err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("rt points = %d, want 1 for repeated backfill", len(points))
	}
}

// pythonFlowHarness 把 harness 流程改成 python 采集(可选解绑数据源), 返回引擎与数据源 ID。
func pythonFlowHarness(t *testing.T, col CollectorRunner, keepDS bool) (*engineHarness, *Engine, int64) {
	t.Helper()
	eh := newEngineHarness(t, col, "{}")
	dsID := *eh.flow.DataSourceID
	if keepDS {
		if err := eh.h.DB.Model(eh.flow).Update("collect_type", "python").Error; err != nil {
			t.Fatal(err)
		}
	} else {
		if err := eh.h.DB.Model(eh.flow).Updates(map[string]any{
			"collect_type": "python", "data_source_id": nil,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return eh, eh.engine(col), dsID
}

// python 采集在平台沙箱执行, 不读数据源 → 流程未绑数据源也应能正常运行(loadFlow 不再强制要求数据源)。
func TestPythonFlowWithoutDataSourceRuns(t *testing.T) {
	col := &stubCollector{}
	eh, eng, _ := pythonFlowHarness(t, col, false)
	res, err := eng.Run(context.Background(), eh.flow.ID, "manual", "tester")
	if err != nil {
		t.Fatalf("python 采集未绑数据源不应报错: %v", err)
	}
	if !res.OK || res.Status != "success" {
		t.Fatalf("res = %+v", res)
	}
	if task := eh.task(); task.Status != "success" {
		t.Fatalf("task = %+v", task)
	}
}

// python 采集未绑数据源且执行失败: 收尾不得 panic, 采集失败告警照常产生。
func TestPythonFlowFailureWithoutDataSourceAlerts(t *testing.T) {
	col := &stubCollector{fails: 99}
	eh, eng, _ := pythonFlowHarness(t, col, false)
	res, err := eng.Run(context.Background(), eh.flow.ID, "auto", "scheduler")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Status != "failed" {
		t.Fatalf("res = %+v", res)
	}
	var alerts int64
	eh.h.DB.Model(&persistence.AlertRecord{}).Where("alert_type = ?", "collect_fail").Count(&alerts)
	if alerts != 1 {
		t.Fatalf("collect_fail alerts = %d", alerts)
	}
}

// python 采集不读数据源 → 存量流程即使仍绑着数据源也不参与健康标记: 成功不置 ok, 失败不置 error。
func TestPythonFlowSkipsDataSourceHealth(t *testing.T) {
	col := &stubCollector{}
	eh, eng, dsID := pythonFlowHarness(t, col, true)
	if err := eh.h.DB.Model(&persistence.DataSource{}).Where("id = ?", dsID).
		Update("health_status", "unknown").Error; err != nil {
		t.Fatal(err)
	}
	health := func() string {
		t.Helper()
		var ds persistence.DataSource
		if err := eh.h.DB.First(&ds, dsID).Error; err != nil {
			t.Fatal(err)
		}
		return ds.HealthStatus
	}

	if _, err := eng.Run(context.Background(), eh.flow.ID, "manual", "tester"); err != nil {
		t.Fatal(err)
	}
	if got := health(); got != "unknown" {
		t.Fatalf("python 成功路径改写了数据源健康状态: %s", got)
	}

	fail := &stubCollector{fails: 99}
	if _, err := eh.engine(fail).Run(context.Background(), eh.flow.ID, "auto", "scheduler"); err != nil {
		t.Fatal(err)
	}
	if got := health(); got != "unknown" {
		t.Fatalf("python 失败路径改写了数据源健康状态: %s", got)
	}
}
