package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
	"capacity-manage/backend/internal/tradingday"
)

// 2026-10 日历: 工作日为交易日, 10-13(周一)为节假日; 10-16 周五, 10-17 周六, 10-18 周日
func TestPickBizDateNatural(t *testing.T) {
	anchor := time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local)
	fl := &persistence.CollectFlow{ScheduleMode: "normal", CronExpr: "0 0 17 * * ?"}
	if got := PickBizDate(context.Background(), nil, fl, anchor); !got.Equal(anchor) {
		t.Fatalf("natural biz = %v, want %v", got, anchor)
	}
	// trading_day 模式 + 数据日期=当日 → 仍取自然日(数据日期改由 biz_date_mode 驱动)
	fl2 := &persistence.CollectFlow{ScheduleMode: "trading_day", TradingMarket: "SH", BizDateMode: "natural"}
	if got := PickBizDate(context.Background(), nil, fl2, anchor); !got.Equal(anchor) {
		t.Fatalf("trading_day+natural biz = %v, want %v", got, anchor)
	}
	// 数据日期=上日 → 前一自然日(与交易日无关)
	fl3 := &persistence.CollectFlow{ScheduleMode: "normal", BizDateMode: "prev_day"}
	if got := PickBizDate(context.Background(), nil, fl3, anchor); !got.Equal(anchor.AddDate(0, 0, -1)) {
		t.Fatalf("prev_day biz = %v, want %v", got, anchor.AddDate(0, 0, -1))
	}
	// 无流程 → 自然日
	if got := PickBizDate(context.Background(), nil, nil, anchor); !got.Equal(anchor) {
		t.Fatalf("no-flow biz = %v, want %v", got, anchor)
	}
}

func TestPickBizDateTrading(t *testing.T) {
	h := testutil.NewCalHarness(t)
	h.SeedOctober2026()
	cal := tradingday.NewCalendar(h.DB, h.RDB, nil)
	fl := &persistence.CollectFlow{ScheduleMode: "trading_day", TradingMarket: "SH", BizDateMode: "calendar"}
	cases := []struct {
		anchor time.Time
		want   time.Time
	}{
		// 周五(交易日) → 上一交易日 周四
		{time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)},
		// 周六 → 周五
		{time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)},
		// 周日 → 周五
		{time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)},
		// 周二节假日(10-13) → 周一 10-12
		{time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 12, 0, 0, 0, 0, time.Local)},
		// 周一(10-19) → 上一交易日 周五 10-16
		{time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)},
	}
	for _, c := range cases {
		if got := PickBizDate(context.Background(), cal, fl, c.anchor); !got.Equal(c.want) {
			t.Fatalf("anchor %s: biz = %s, want %s", c.anchor.Format("2006-01-02"), got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
}

// 回归: 带交易日历的流程, 调度器传入的 anchor 已是目标业务日;
// 日汇总不得再执行一次 PrevTradingDay, 否则会把 10-26 错误回退到 10-23。
func TestSummarizeDayCalendarUsesSummaryDayWithoutSecondRollback(t *testing.T) {
	h, store, dep := newSumHarness(t)
	dep.Cal = tradingday.NewCalendar(h.DB, h.RDB, nil)
	bizDate := time.Date(2026, 10, 26, 0, 0, 0, 0, time.Local)
	for _, d := range []time.Time{
		time.Date(2026, 10, 23, 0, 0, 0, 0, time.Local),
		bizDate,
	} {
		if err := h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: d, IsTrading: true, DayType: "trading", Source: "test",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.DB.Create(&persistence.BusinessSystem{
		SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "A5_JZJY_ORDER_CNT_ALL", Name: "订单量", SystemCode: "S1",
		MetricKind: "normal", MetricType: "absolute", Unit: "笔", StatMode: "latest", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: "A5_JZJY_ORDER_CNT_ALL_FLOW", Name: "订单量采集",
		MetricCode: "A5_JZJY_ORDER_CNT_ALL", ScheduleMode: "trading_day",
		TradingMarket: "SH", BizDateMode: "calendar", Status: "active", Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	value := 82.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "A5_JZJY_ORDER_CNT_ALL", Ts: bizDate.Add(15 * time.Minute),
		RawValue: &value, CleanValue: &value, JudgeLevel: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}

	done, missing, err := store.SummarizeDay(context.Background(), bizDate, dep)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if done != 1 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 1/0", done, missing)
	}
	var day persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "A5_JZJY_ORDER_CNT_ALL", bizDate).
		First(&day).Error; err != nil {
		t.Fatalf("day row for %s: %v", bizDate.Format("2006-01-02"), err)
	}
	if day.StatValue == nil || *day.StatValue != value || day.PointCount != 1 {
		t.Fatalf("day row = stat %v point_count %d, want %.0f/1", day.StatValue, day.PointCount, value)
	}
	var wrongCount int64
	if err := h.DB.Model(&persistence.MetricValueDay{}).
		Where("metric_code = ? AND biz_date = ?", "A5_JZJY_ORDER_CNT_ALL",
			time.Date(2026, 10, 23, 0, 0, 0, 0, time.Local)).
		Count(&wrongCount).Error; err != nil {
		t.Fatal(err)
	}
	if wrongCount != 0 {
		t.Fatalf("unexpected rollback day rows = %d, want 0", wrongCount)
	}
}

func TestSummarizeScheduledDayNaturalUsesLastCompletedTradingRun(t *testing.T) {
	h, store, dep := newSumHarness(t)
	dep.Cal = tradingday.NewCalendar(h.DB, h.RDB, nil)
	seedScheduledSummaryCalendar(t, h)
	seedScheduledSummaryFlow(t, h, "F_NATURAL_18", "M_NATURAL_18", "natural", "18:00:00")

	friday := time.Date(2026, 10, 16, 18, 0, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "M_NATURAL_18", Ts: friday, RawValue: &value, CleanValue: &value,
		JudgeLevel: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}

	anchor := time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local)
	asOf := time.Date(2026, 10, 18, 0, 10, 0, 0, time.Local)
	done, missing, bizDates, err := store.SummarizeScheduledDay(context.Background(), anchor, asOf, dep)
	if err != nil {
		t.Fatalf("scheduled summarize: %v", err)
	}
	if done != 1 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 1/0", done, missing)
	}
	if len(bizDates) != 1 || !bizDates[0].Equal(time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("summarized dates = %v, want 2026-10-16", bizDates)
	}
	assertNoDayRow(t, h, "M_NATURAL_18", anchor)
	assertDayRowValue(t, h, "M_NATURAL_18", time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local), value)
}

func TestSummarizeScheduledDayPrevDayUsesLastCompletedTradingRun(t *testing.T) {
	h, store, dep := newSumHarness(t)
	dep.Cal = tradingday.NewCalendar(h.DB, h.RDB, nil)
	seedScheduledSummaryCalendar(t, h)
	seedScheduledSummaryFlow(t, h, "F_PREV_18", "M_PREV_18", "prev_day", "18:00:00")

	thursday := time.Date(2026, 10, 15, 18, 0, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "M_PREV_18", Ts: thursday, RawValue: &value, CleanValue: &value,
		JudgeLevel: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}

	anchor := time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local)
	asOf := time.Date(2026, 10, 17, 0, 10, 0, 0, time.Local)
	done, missing, bizDates, err := store.SummarizeScheduledDay(context.Background(), anchor, asOf, dep)
	if err != nil {
		t.Fatalf("scheduled summarize: %v", err)
	}
	if done != 1 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 1/0", done, missing)
	}
	if len(bizDates) != 1 || !bizDates[0].Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("summarized dates = %v, want 2026-10-15", bizDates)
	}
	assertNoDayRow(t, h, "M_PREV_18", anchor)
	assertDayRowValue(t, h, "M_PREV_18", time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local), value)
}

func TestSummarizeScheduledDayCalendarUsesLastCompletedTradingRun(t *testing.T) {
	h, store, dep := newSumHarness(t)
	dep.Cal = tradingday.NewCalendar(h.DB, h.RDB, nil)
	seedScheduledSummaryCalendar(t, h)
	seedScheduledSummaryFlow(t, h, "F_CAL_0015", "M_CAL_0015", "calendar", "00:15:00")

	thursday := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "M_CAL_0015", Ts: thursday.Add(18 * time.Hour),
		RawValue: &value, CleanValue: &value, JudgeLevel: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}

	anchor := time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local)
	asOf := time.Date(2026, 10, 17, 3, 0, 0, 0, time.Local)
	done, missing, bizDates, err := store.SummarizeScheduledDay(context.Background(), anchor, asOf, dep)
	if err != nil {
		t.Fatalf("scheduled summarize: %v", err)
	}
	if done != 1 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 1/0", done, missing)
	}
	if len(bizDates) != 1 || !bizDates[0].Equal(thursday) {
		t.Fatalf("summarized dates = %v, want 2026-10-15", bizDates)
	}
	assertNoDayRow(t, h, "M_CAL_0015", anchor)
	assertDayRowValue(t, h, "M_CAL_0015", thursday, value)
}

func TestSummarizeDayManualKeepsExplicitNonTradingAnchor(t *testing.T) {
	h, store, dep := newSumHarness(t)
	dep.Cal = tradingday.NewCalendar(h.DB, h.RDB, nil)
	seedScheduledSummaryCalendar(t, h)
	seedScheduledSummaryFlow(t, h, "F_MANUAL", "M_MANUAL", "natural", "18:00:00")

	anchor := time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local)
	done, missing, err := store.SummarizeDay(context.Background(), anchor, dep)
	if err != nil {
		t.Fatalf("manual summarize: %v", err)
	}
	if done != 0 || missing != 1 {
		t.Fatalf("done=%d missing=%d, want 0/1", done, missing)
	}
	var row persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "M_MANUAL", anchor).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Level != "missing" {
		t.Fatalf("manual row level = %q, want missing", row.Level)
	}
}

func seedScheduledSummaryCalendar(t *testing.T, h *testutil.DBHarness) {
	t.Helper()
	for _, d := range []struct {
		date    time.Time
		trading bool
	}{
		{time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), false},
		{time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local), false},
	} {
		if err := h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: d.date, IsTrading: d.trading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[d.trading], Source: "test",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func seedScheduledSummaryFlow(t *testing.T, h *testutil.DBHarness, flowCode, metricCode, bizMode, window string) {
	t.Helper()
	systemCode := "SYS_" + flowCode
	if err := h.DB.Create(&persistence.BusinessSystem{
		SystemCode: systemCode, Name: flowCode, Owner: "admin", Status: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: metricCode, Name: metricCode, SystemCode: systemCode,
		MetricKind: "normal", MetricType: "absolute", Unit: "-", StatMode: "latest", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: flowCode, Name: flowCode, MetricCode: metricCode,
		ScheduleMode: "trading_day", TradingMarket: "SH", WindowTime: window,
		BizDateMode: bizMode, Status: "active", Enabled: true,
		CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func assertNoDayRow(t *testing.T, h *testutil.DBHarness, metricCode string, bizDate time.Time) {
	t.Helper()
	var n int64
	if err := h.DB.Model(&persistence.MetricValueDay{}).
		Where("metric_code = ? AND biz_date = ?", metricCode, bizDate).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("unexpected day row for %s %s", metricCode, bizDate.Format("2006-01-02"))
	}
}

func assertDayRowValue(t *testing.T, h *testutil.DBHarness, metricCode string, bizDate time.Time, want float64) {
	t.Helper()
	var row persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", metricCode, bizDate).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Level != "normal" || row.StatValue == nil || *row.StatValue != want {
		t.Fatalf("day row = level %q stat %v, want normal/%v", row.Level, row.StatValue, want)
	}
}

// 回归: 日汇总重跑时日内数据已不可用, 已有的有效日值不得被 missing 覆盖。
func TestSummarizeDayPreservesExistingValueWithoutRT(t *testing.T) {
	h, store, dep := newSumHarness(t)
	anchor := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	if err := h.DB.Create(&persistence.BusinessSystem{
		SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "ORDER_CNT", Name: "订单量", SystemCode: "S1",
		MetricKind: "normal", MetricType: "absolute", Unit: "笔", StatMode: "latest", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	value := 1867.0
	if err := h.DB.Create(&persistence.MetricValueDay{
		MetricCode: "ORDER_CNT", BizDate: anchor,
		DayMax: &value, DayMin: &value, DayAvg: &value, DayLast: &value, StatValue: &value,
		Level: "normal", PointCount: 1, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}

	done, missing, err := store.SummarizeDay(context.Background(), anchor, dep)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if done != 1 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 1/0", done, missing)
	}
	var got persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "ORDER_CNT", anchor).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.StatValue == nil || *got.StatValue != value {
		t.Fatalf("stat_value = %v, want %.0f", got.StatValue, value)
	}
	if got.Level != "normal" || got.PointCount != 1 {
		t.Fatalf("day row = level %s point_count %d, want normal/1", got.Level, got.PointCount)
	}
}

// newSumHarness 日汇总测试: 内存库 + judge/alert 依赖(与生产 RunDailySummary 注入一致)
func newSumHarness(t *testing.T) (*testutil.DBHarness, *Store, *SummaryDeps) {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.MetricValueDay{}, &persistence.MetricValueRT{},
		&persistence.AlertRecord{}, &persistence.BusinessSystem{}, &persistence.CollectFlow{},
		&persistence.MetricStatModule{}, &persistence.TradingDayCalendar{},
		&persistence.MetricPeakStat{}, &persistence.AlertDailyHit{},
		&persistence.AlertEscalationPolicy{}, &persistence.AlertLevelHistory{})
	params := system.NewParams(h.DB)
	dep := &SummaryDeps{Judge: judge.New(params, h.DB), Alerts: alert.New(h.DB, params, nil)}
	return h, NewStore(h.DB), dep
}

// TestSummarizeDayAggregateAlert 汇总指标日值(主模块求和) + 超线生成阈值告警
func TestSummarizeDayAggregateAlert(t *testing.T) {
	h, store, dep := newSumHarness(t)
	anchor := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	warn, danger := 250.0, 400.0
	_ = h.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate", Unit: "-",
		MainModule: "count", JudgeDirection: "higher_worse", ThresholdMode: "absolute",
		WarnLine: &warn, DangerLine: &danger, Status: "active",
	}).Error
	for _, g := range []string{"SRV.ORD", "SRV.PAY"} {
		pc := "SRV"
		_ = h.DB.Create(&persistence.CapacityMetric{
			MetricCode: g, Name: "子指标", SystemCode: "S1", MetricKind: "group",
			ParentCode: &pc, Unit: "-", Status: "active",
		}).Error
	}
	for _, c := range []string{"SRV.ORD.COUNT", "SRV.PAY.COUNT"} {
		pc := c[:len(c)-6]
		_ = h.DB.Create(&persistence.CapacityMetric{
			MetricCode: c, Name: "请求量", SystemCode: "S1", MetricKind: "normal",
			ParentCode: func() *string { s := pc; return &s }(),
			StatModule: "count", Unit: "笔", StatMode: "sum", Status: "active",
		}).Error
	}
	// 日内数据: ORD 60+40=100, PAY 100+100=200 → 汇总 300 (>=250 warn)
	for i, p := range []struct {
		code string
		v    float64
	}{
		{"SRV.ORD.COUNT", 60}, {"SRV.ORD.COUNT", 40},
		{"SRV.PAY.COUNT", 100}, {"SRV.PAY.COUNT", 100},
	} {
		v := p.v
		_ = h.DB.Create(&persistence.MetricValueRT{
			MetricCode: p.code, Ts: anchor.Add(time.Duration(i+1) * time.Hour), RawValue: &v, CleanValue: &v,
			JudgeLevel: "normal",
		}).Error
	}
	done, missing, err := store.SummarizeDay(context.Background(), anchor, dep)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if done != 2 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 2/0", done, missing)
	}
	// 汇总指标日值
	var day persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "SRV", anchor).First(&day).Error; err != nil {
		t.Fatalf("aggregate day row: %v", err)
	}
	if day.StatValue == nil || *day.StatValue != 300 {
		t.Fatalf("aggregate stat = %v, want 300", *day.StatValue)
	}
	if day.Level != "warn" || day.Source != "aggregate" {
		t.Fatalf("aggregate day = level %s source %s", day.Level, day.Source)
	}
	// 阈值告警联动: 首次命中一条活动 P3
	var recs []persistence.AlertRecord
	if err := h.DB.Where("metric_code = ? AND status IN ('active','processing')", "SRV").Find(&recs).Error; err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Level != "p3" || recs[0].AlertType != "threshold" {
		t.Fatalf("aggregate alerts = %+v", recs)
	}
	// 幂等: 重跑不重复生成告警
	if _, _, err := store.SummarizeDay(context.Background(), anchor, dep); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	var again []persistence.AlertRecord
	_ = h.DB.Where("metric_code = ? AND status IN ('active','processing')", "SRV").Find(&again).Error
	if len(again) != 1 {
		t.Fatalf("alerts after re-run = %d, want 1 (merge)", len(again))
	}
}

// TestSummarizeDayAggregateConfiguredModuleSum 模块汇总方式与普通指标日内方式解耦:
// 两个普通指标 latest 分别取 40/100, count 模块显式 sum 后汇总值应为 140。
func TestSummarizeDayAggregateConfiguredModuleSum(t *testing.T) {
	h, store, dep := newSumHarness(t)
	anchor := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	_ = h.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate",
		Unit: "-", MainModule: "count", Status: "active",
	}).Error
	for _, g := range []string{"SRV.ORD", "SRV.PAY"} {
		pc := "SRV"
		_ = h.DB.Create(&persistence.CapacityMetric{
			MetricCode: g, Name: "子指标", SystemCode: "S1", MetricKind: "group",
			ParentCode: &pc, Unit: "-", Status: "active",
		}).Error
	}
	for _, c := range []struct {
		code, parent string
	}{
		{"SRV.ORD.COUNT", "SRV.ORD"},
		{"SRV.PAY.COUNT", "SRV.PAY"},
	} {
		pc := c.parent
		if err := h.DB.Create(&persistence.CapacityMetric{
			MetricCode: c.code, Name: "请求量", SystemCode: "S1", MetricKind: "normal",
			ParentCode: &pc, StatModule: "count", Unit: "笔", StatMode: "latest", Status: "active",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.DB.Create(&persistence.MetricStatModule{
		AggregateCode: "SRV", ModuleKey: "count", AggMode: "sum",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for i, p := range []struct {
		code string
		v    float64
	}{
		{"SRV.ORD.COUNT", 60}, {"SRV.ORD.COUNT", 40},
		{"SRV.PAY.COUNT", 100}, {"SRV.PAY.COUNT", 100},
	} {
		v := p.v
		_ = h.DB.Create(&persistence.MetricValueRT{
			MetricCode: p.code, Ts: anchor.Add(time.Duration(i+1) * time.Hour), RawValue: &v, CleanValue: &v,
			JudgeLevel: "normal",
		}).Error
	}

	done, missing, err := store.SummarizeDay(context.Background(), anchor, dep)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if done != 2 || missing != 0 {
		t.Fatalf("done=%d missing=%d, want 2/0", done, missing)
	}
	var day persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "SRV", anchor).First(&day).Error; err != nil {
		t.Fatalf("aggregate day row: %v", err)
	}
	if day.StatValue == nil || *day.StatValue != 140 {
		t.Fatalf("aggregate stat = %v, want 140", day.StatValue)
	}
}

// TestSummarizeDayAggregateWeighted 主模块=加权均值(平均耗时按请求量加权)
func TestSummarizeDayAggregateWeighted(t *testing.T) {
	h, store, dep := newSumHarness(t)
	anchor := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	_ = h.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal"}).Error
	_ = h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "SRV", Name: "服务请求", SystemCode: "S1", MetricKind: "aggregate", Unit: "-",
		MainModule: "avg_latency", Status: "active",
	}).Error
	type leaf struct {
		code, module, statMode, weight string
	}
	for _, g := range []string{"SRV.ORD", "SRV.PAY"} {
		pc := "SRV"
		_ = h.DB.Create(&persistence.CapacityMetric{
			MetricCode: g, Name: "子指标", SystemCode: "S1", MetricKind: "group",
			ParentCode: &pc, Unit: "-", Status: "active",
		}).Error
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
		if err := h.DB.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	pts := []struct {
		code string
		vs   []float64
	}{
		{"SRV.ORD.AVG", []float64{100, 200}}, // avg 150
		{"SRV.ORD.COUNT", []float64{10, 30}}, // sum 40
		{"SRV.PAY.AVG", []float64{200, 400}}, // avg 300
		{"SRV.PAY.COUNT", []float64{40, 60}}, // sum 100
	}
	for i, p := range pts {
		for j, v := range p.vs {
			x := v
			_ = h.DB.Create(&persistence.MetricValueRT{
				MetricCode: p.code, Ts: anchor.Add(time.Duration(i+j) * time.Hour),
				RawValue: &x, CleanValue: &x, JudgeLevel: "normal",
			}).Error
		}
	}
	if _, _, err := store.SummarizeDay(context.Background(), anchor, dep); err != nil {
		t.Fatalf("summarize: %v", err)
	}
	var day persistence.MetricValueDay
	if err := h.DB.Where("metric_code = ? AND biz_date = ?", "SRV", anchor).First(&day).Error; err != nil {
		t.Fatalf("aggregate day row: %v", err)
	}
	// 加权均值: (150*40 + 300*100) / (40+100) = 257.142857...
	want := (150*40 + 300*100) / 140.0
	if day.StatValue == nil || *day.StatValue < want-0.001 || *day.StatValue > want+0.001 {
		t.Fatalf("weighted stat = %v, want %v", day.StatValue, want)
	}
	if day.Level != "normal" {
		t.Fatalf("level = %s, want normal(未配阈值)", day.Level)
	}
	var recs []persistence.AlertRecord
	_ = h.DB.Where("metric_code = ?", "SRV").Find(&recs).Error
	if len(recs) != 0 {
		t.Fatalf("unexpected alerts = %+v", recs)
	}
}
