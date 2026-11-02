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
	return newSchedHarnessAt(t, time.Now())
}

func newSchedHarnessAt(t *testing.T, center time.Time) (*testutil.DBHarness, *Scheduler) {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CollectFlow{}, &persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.MetricValueRT{}, &persistence.AlertRecord{}, &persistence.TradingDayCalendar{},
		&persistence.BusinessSystem{})
	// 以 center 为中心 ±15 天日历(工作日为交易日)
	for off := -15; off <= 15; off++ {
		d := time.Date(center.Year(), center.Month(), center.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, off)
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
	seedFlowAt(t, h, flowCode, metricCode, mode, market, time.Now())
}

func seedFlowAt(t *testing.T, h *testutil.DBHarness, flowCode, metricCode, mode, market string, center time.Time) {
	t.Helper()
	sysCode := "SYS_" + flowCode
	sys := persistence.BusinessSystem{SystemCode: sysCode, Name: flowCode, Status: "normal"}
	metric := persistence.CapacityMetric{MetricCode: metricCode, Name: metricCode, SystemCode: sysCode,
		MetricType: "absolute", Unit: "GB", StatMode: "latest", Status: "active"}
	ds := persistence.DataSource{Name: "ds-" + flowCode, Type: "log", Status: "available", Config: "{}"}
	bizDateMode := "natural"
	if mode == "trading_day" {
		bizDateMode = "calendar"
	}
	fl := persistence.CollectFlow{
		FlowCode: flowCode, Name: flowCode, MetricCode: metricCode,
		CollectType: "sql", CollectConfig: "{}", CleanConfig: "{}",
		ScheduleMode: mode, CronExpr: "0 0 2 * * ?", TradingMarket: market, WindowTime: "17:30",
		BizDateMode: bizDateMode, Status: "active", Enabled: true,
	}
	fl.CreatedAt = center.AddDate(0, 0, -3) // 回退建档时间, 避免"新建流程"豁免
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
	center := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_normal", "m1", "normal", "", center)
	seedFlowAt(t, h, "f_tday", "m2", "trading_day", "SH", center)
	ctx := context.Background()
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local)
	s.missingCheckAt(ctx, now)
	if n := missingAlerts(t, h); n != 2 {
		t.Fatalf("missing alerts = %d, want 2", n)
	}
	// 重复执行不重复告警
	s.missingCheckAt(ctx, now)
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

func TestMissingCheckOnWeekendUsesLastCompletedRunBizDate(t *testing.T) {
	center := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_weekend", "m_weekend", "trading_day", "SH", center)
	h.DB.Model(&persistence.CollectFlow{}).Where("flow_code = ?", "f_weekend").Update("window_time", "00:15:00")

	thursday := time.Date(2026, 10, 15, 0, 15, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "m_weekend", Ts: thursday, CleanValue: &value, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}

	s.missingCheckAt(context.Background(), time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local))
	if n := missingAlerts(t, h); n != 0 {
		t.Fatalf("missing alerts = %d, want 0 on weekend before next trading-day collection", n)
	}
}

func TestMissingCheckOnMondayUsesFridayAsExpectedBizDate(t *testing.T) {
	center := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_monday", "m_monday", "trading_day", "SH", center)
	h.DB.Model(&persistence.CollectFlow{}).Where("flow_code = ?", "f_monday").Update("window_time", "00:15:00")

	s.missingCheckAt(context.Background(), time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local))

	var rec persistence.AlertRecord
	if err := h.DB.Where("alert_type = ?", "missing").First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	if rec.LastValueAt == nil || !rec.LastValueAt.Equal(want) {
		t.Fatalf("missing alert biz date = %v, want Friday %v", rec.LastValueAt, want)
	}
}

func TestMissingCheckUsesPreviousEveningRunForLateWindow(t *testing.T) {
	center := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_evening", "m_evening", "trading_day", "SH", center)

	monday := time.Date(2026, 10, 19, 17, 30, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "m_evening", Ts: monday, CleanValue: &value, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}

	s.missingCheckAt(context.Background(), time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local))
	if n := missingAlerts(t, h); n != 0 {
		t.Fatalf("missing alerts = %d, want 0 after previous evening run", n)
	}
}

func TestMissingCheckNaturalTradingDayStaysOnLastCompletedRunDate(t *testing.T) {
	center := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_natural_18", "m_natural_18", "trading_day", "SH", center)
	h.DB.Model(&persistence.CollectFlow{}).Where("flow_code = ?", "f_natural_18").Updates(map[string]any{
		"biz_date_mode": "natural",
		"window_time":   "18:00:00",
	})

	friday := time.Date(2026, 10, 16, 18, 0, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "m_natural_18", Ts: friday, CleanValue: &value, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, now := range []time.Time{
		time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local),
		time.Date(2026, 10, 18, 9, 0, 0, 0, time.Local),
		time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local),
	} {
		s.missingCheckAt(context.Background(), now)
		if n := missingAlerts(t, h); n != 0 {
			t.Fatalf("missing alerts = %d after %s, want 0 before Monday 18:00 run", n, now.Format("2006-01-02 15:04"))
		}
	}
}

func TestMissingCheckPrevDayTradingDayUsesLastCompletedRunDate(t *testing.T) {
	center := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_prev_day_18", "m_prev_day_18", "trading_day", "SH", center)
	h.DB.Model(&persistence.CollectFlow{}).Where("flow_code = ?", "f_prev_day_18").Updates(map[string]any{
		"biz_date_mode": "prev_day",
		"window_time":   "18:00:00",
	})

	thursday := time.Date(2026, 10, 15, 18, 0, 0, 0, time.Local)
	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "m_prev_day_18", Ts: thursday, CleanValue: &value, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}

	s.missingCheckAt(context.Background(), time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local))
	if n := missingAlerts(t, h); n != 0 {
		t.Fatalf("missing alerts = %d, want 0 after Friday run collected Thursday", n)
	}
}

func TestMissingCheckRecoversActiveAlertWhenExpectedDataExists(t *testing.T) {
	center := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)
	h, s := newSchedHarnessAt(t, center)
	seedFlowAt(t, h, "f_recover", "m_recover", "trading_day", "SH", center)
	h.DB.Model(&persistence.CollectFlow{}).Where("flow_code = ?", "f_recover").Update("window_time", "00:15:00")

	friday := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	metric := persistence.CapacityMetric{MetricCode: "m_recover", SystemCode: "SYS_f_recover"}
	s.alerts.OnMissing(context.Background(), &metric, friday)

	value := 42.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "m_recover", Ts: friday.Add(15 * time.Minute), CleanValue: &value, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}

	s.missingCheckAt(context.Background(), time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local))

	var rec persistence.AlertRecord
	if err := h.DB.Where("metric_code = ? AND alert_type = ?", "m_recover", "missing").First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.Status != "recovered" || rec.RecoveredAt == nil {
		t.Fatalf("missing alert status = %q recoveredAt=%v, want recovered", rec.Status, rec.RecoveredAt)
	}
}
