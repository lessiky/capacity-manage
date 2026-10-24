package scheduler

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
	"capacity-manage/backend/internal/tradingday"
)

func newSchedHarness(t *testing.T) (*testutil.DBHarness, *Scheduler) {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CollectFlow{}, &persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.MetricValueRT{}, &persistence.AlertRecord{}, &persistence.TradingDayCalendar{},
		&persistence.BusinessSystem{})
	// 以今天为中心 ±15 天日历(工作日为交易日)
	for off := -15; off <= 15; off++ {
		d := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, off)
		wd := d.Weekday()
		isTrading := wd != time.Saturday && wd != time.Sunday
		h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: d, IsTrading: isTrading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[isTrading], Source: "test",
		})
	}
	params := system.NewParams(h.DB)
	cal := tradingday.NewCalendar(h.DB, h.RDB, params)
	s := New(h.DB, nil, cal, params, slog.Default())
	s.SetAlerts(alert.New(h.DB, params, nil))
	return h, s
}

func seedFlow(t *testing.T, h *testutil.DBHarness, flowCode, metricCode, mode, market string) {
	t.Helper()
	sysCode := "SYS_" + flowCode
	sys := persistence.BusinessSystem{SystemCode: sysCode, Name: flowCode, Status: "normal"}
	metric := persistence.CapacityMetric{MetricCode: metricCode, Name: metricCode, SystemCode: sysCode,
		MetricType: "absolute", Unit: "GB", StatMode: "latest", Status: "active"}
	ds := persistence.DataSource{Name: "ds-" + flowCode, Type: "log", Status: "available", Config: "{}"}
	fl := persistence.CollectFlow{
		FlowCode: flowCode, Name: flowCode, MetricCode: metricCode,
		CollectType: "sql", CollectConfig: "{}", CleanConfig: "{}",
		ScheduleMode: mode, CronExpr: "0 0 2 * * ?", TradingMarket: market, WindowTime: "17:30",
		Status: "active", Enabled: true,
	}
	fl.CreatedAt = time.Now().AddDate(0, 0, -3) // 回退建档时间, 避免"新建流程"豁免
	for _, m := range []any{&sys, &metric, &ds, &fl} {
		if err := h.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func missingAlerts(t *testing.T, h *testutil.DBHarness) int64 {
	t.Helper()
	var n int64
	if err := h.DB.Model(&persistence.AlertRecord{}).Where("alert_type = ?", "missing").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMissingCheckDetectsAndDedups(t *testing.T) {
	h, s := newSchedHarness(t)
	seedFlow(t, h, "f_normal", "m1", "normal", "")
	seedFlow(t, h, "f_tday", "m2", "trading_day", "SH")
	ctx := context.Background()
	s.missingCheck(ctx)
	if n := missingAlerts(t, h); n != 2 {
		t.Fatalf("missing alerts = %d, want 2", n)
	}
	// 重复执行不重复告警
	s.missingCheck(ctx)
	if n := missingAlerts(t, h); n != 2 {
		t.Fatalf("dedup failed: %d", n)
	}
}

func TestMissingCheckSkipsWithDataAndOffline(t *testing.T) {
	h, s := newSchedHarness(t)
	seedFlow(t, h, "f_ok", "m1", "normal", "")
	seedFlow(t, h, "f_off", "m2", "normal", "")
	// f_off 所属系统下线 → 冻结不判定
	h.DB.Model(&persistence.BusinessSystem{}).Where("system_code = ?", "SYS_f_off").Update("status", "offline")
	// m1 前一自然日有数据点
	day := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -1)
	v := 42.0
	h.DB.Create(&persistence.MetricValueRT{MetricCode: "m1", Ts: day.Add(3 * time.Hour), CleanValue: &v, Source: "collect"})
	s.missingCheck(context.Background())
	// m1 有数据 → 不告警; m2 系统下线冻结 → 不告警
	var n int64
	if err := h.DB.Model(&persistence.AlertRecord{}).Where("alert_type = ?", "missing").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("missing alerts = %d, want 0 (有数据/下线系统均跳过)", n)
	}
}
