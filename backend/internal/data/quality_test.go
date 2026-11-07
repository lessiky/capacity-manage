package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"

	"gorm.io/gorm"
)

func TestCalculateQualityPerfectAndFlaggedRanges(t *testing.T) {
	perfect := CalculateQuality(QualityInput{
		RangeDays: 10, ValidDays: 10, TimelyDays: 10,
		TotalPoints: 100, AnomalyPoints: 0, DuplicatePoints: 0,
		LatestBizDate: time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local),
		RangeEnd:      time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local),
	})
	if perfect.Score != 1 || len(perfect.Flags) != 0 {
		t.Fatalf("perfect = %+v", perfect)
	}
	flagged := CalculateQuality(QualityInput{
		RangeDays: 10, ValidDays: 6, TimelyDays: 7,
		TotalPoints: 100, AnomalyPoints: 25, DuplicatePoints: 5,
		LatestBizDate: time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local),
		RangeEnd:      time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local),
	})
	if flagged.Score >= 0.8 {
		t.Fatalf("flagged score = %.4f, want below 0.8", flagged.Score)
	}
	for _, flag := range []string{"missing_days", "late_days", "anomaly_ratio", "duplicates", "stale"} {
		if !containsString(flagged.Flags, flag) {
			t.Fatalf("flags = %v, missing %s", flagged.Flags, flag)
		}
	}
	empty := CalculateQuality(QualityInput{RangeDays: 5, RangeEnd: time.Now()})
	if empty.Score != 0 || !containsString(empty.Flags, "no_data") {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestQualityServiceEvaluatePersistsDailyScore(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.MetricValueDay{}, &persistence.MetricValueRT{}, &persistence.MetricQualityDay{},
	)
	_ = h.DB.Create(&persistence.CapacityMetric{MetricCode: "CPU", SystemCode: "SYS", MetricKind: "normal", Status: "active"}).Error
	for day := 18; day <= 20; day++ {
		v := 80.0
		ts := time.Date(2026, 10, day, 0, 0, 0, 0, time.Local)
		_ = h.DB.Create(&persistence.MetricValueDay{
			MetricCode: "CPU", BizDate: ts, StatValue: &v, Level: "warn",
			PointCount: 1, Source: "collect", UpdatedAt: ts.Add(2 * time.Hour),
		}).Error
		_ = h.DB.Create(&persistence.MetricValueRT{
			MetricCode: "CPU", Ts: ts.Add(1 * time.Hour), CleanValue: &v,
			RawValue: &v, JudgeLevel: "warn",
		}).Error
	}
	svc := NewQualityService(h.DB, system.NewParams(h.DB))
	res, err := svc.Evaluate(context.Background(), "CPU",
		time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local),
		time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if res.Score <= 0 || len(res.Days) != 3 {
		t.Fatalf("result = %+v", res)
	}
	var n int64
	if err := h.DB.Model(&persistence.MetricQualityDay{}).Where("metric_code = ?", "CPU").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("quality rows = %d, want 3", n)
	}
}

// 回归: 上一交易日/上一自然日的数据会在业务日之后的凌晨采集,
// 及时性应从业务日结束时刻起算, 不得直接把跨日采集判为不及时。
func TestQualityServiceTimelinessAllowsNextDayScheduledCollection(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.MetricValueDay{}, &persistence.MetricValueRT{}, &persistence.MetricQualityDay{},
	)
	_ = h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "ORDER_CNT", SystemCode: "SYS", MetricKind: "normal", Status: "active",
	}).Error
	bizDate := time.Date(2026, 10, 26, 0, 0, 0, 0, time.Local)
	value := 82.0
	_ = h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "ORDER_CNT", Ts: bizDate.Add(5 * time.Minute),
		RawValue: &value, CleanValue: &value, JudgeLevel: "normal",
		CreatedAt: time.Date(2026, 10, 27, 0, 5, 0, 0, time.Local),
	}).Error

	res, err := NewQualityService(h.DB, nil).
		Evaluate(context.Background(), "ORDER_CNT", bizDate, bizDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Days) != 1 {
		t.Fatalf("days = %d, want 1", len(res.Days))
	}
	if res.Days[0].Timeliness != 1 || res.Days[0].Score != 1 {
		t.Fatalf("day = timeliness %.4f score %.4f, want 1/1", res.Days[0].Timeliness, res.Days[0].Score)
	}
	for _, flag := range []string{"late_days", "stale"} {
		if strings.Contains(res.Days[0].Flags, `"`+flag+`"`) {
			t.Fatalf("flags = %s, unexpected %s", res.Days[0].Flags, flag)
		}
	}
}

// 回归: 交易日调度指标只评价交易日，周末未采集不得作为缺天拉低质量分。
func TestQualityServiceSkipsNonTradingDaysForTradingDayFlow(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.TradingDayCalendar{}, &persistence.MetricValueDay{},
		&persistence.MetricValueRT{}, &persistence.MetricQualityDay{},
	)
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "ORDER_CNT", SystemCode: "SYS", MetricKind: "normal", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: "F_ORDER_CNT", Name: "订单量采集", MetricCode: "ORDER_CNT",
		ScheduleMode: "trading_day", TradingMarket: "SH", BizDateMode: "natural",
		Status: "active", Enabled: true,
		CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, day := range []struct {
		date    time.Time
		trading bool
	}{
		{time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), false},
		{time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local), false},
		{time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local), true},
	} {
		if err := h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: day.date, IsTrading: day.trading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[day.trading],
			Source:  "test",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, date := range []time.Time{
		time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local),
		time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local),
	} {
		value := 82.0
		if err := h.DB.Create(&persistence.MetricValueDay{
			MetricCode: "ORDER_CNT", BizDate: date, StatValue: &value,
			Level: "normal", PointCount: 1, Source: "collect",
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.DB.Create(&persistence.MetricValueRT{
			MetricCode: "ORDER_CNT", Ts: date.Add(time.Hour),
			RawValue: &value, CleanValue: &value, JudgeLevel: "normal",
			CreatedAt: date.Add(2 * time.Hour),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewQualityService(h.DB, nil)
	svc.now = func() time.Time {
		return time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	}
	res, err := svc.Evaluate(
		context.Background(), "ORDER_CNT",
		time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local),
		time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Days) != 2 || res.Score != 1 {
		t.Fatalf("result = score %.4f days %v, want 2 trading days with score 1", res.Score, qualityDates(res.Days))
	}
	for _, day := range res.Days {
		if day.BizDate.Weekday() == time.Saturday || day.BizDate.Weekday() == time.Sunday {
			t.Fatalf("unexpected non-trading quality row: %s", day.BizDate.Format("2006-01-02"))
		}
	}
	var weekendRows int64
	if err := h.DB.Model(&persistence.MetricQualityDay{}).
		Where("metric_code = ? AND biz_date IN ?", "ORDER_CNT", []time.Time{
			time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local),
			time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local),
		}).Count(&weekendRows).Error; err != nil {
		t.Fatal(err)
	}
	if weekendRows != 0 {
		t.Fatalf("weekend quality rows = %d, want 0", weekendRows)
	}
}

func TestQualityServiceNonTradingRangeEndDoesNotReduceFreshness(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.TradingDayCalendar{}, &persistence.MetricValueDay{},
		&persistence.MetricValueRT{}, &persistence.MetricQualityDay{},
	)
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "ORDER_CNT", SystemCode: "SYS", MetricKind: "normal", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: "F_ORDER_CNT", Name: "订单量采集", MetricCode: "ORDER_CNT",
		ScheduleMode: "trading_day", TradingMarket: "SH", BizDateMode: "natural",
		Status: "active", Enabled: true,
		CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatal(err)
	}
	friday := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	for _, day := range []struct {
		date    time.Time
		trading bool
	}{
		{friday, true},
		{time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), false},
		{time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local), false},
	} {
		if err := h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: day.date, IsTrading: day.trading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[day.trading],
			Source:  "test",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	value := 82.0
	if err := h.DB.Create(&persistence.MetricValueDay{
		MetricCode: "ORDER_CNT", BizDate: friday, StatValue: &value,
		Level: "normal", PointCount: 1, Source: "collect",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "ORDER_CNT", Ts: friday.Add(time.Hour),
		RawValue: &value, CleanValue: &value, JudgeLevel: "normal",
		CreatedAt: friday.Add(2 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewQualityService(h.DB, nil)
	svc.now = func() time.Time {
		return time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	}
	res, err := svc.Evaluate(
		context.Background(), "ORDER_CNT", friday, friday.AddDate(0, 0, 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Days) != 1 || res.Score != 1 || res.Freshness != 1 {
		t.Fatalf("result = score %.4f freshness %.4f days %v, want 1 trading day with score/freshness 1",
			res.Score, res.Freshness, qualityDates(res.Days))
	}
}

// 回归: 交易日 00:15 采集上一交易日时，周末不得提前评价周五数据；
// 周五数据要等到下周一 00:15 调度完成后才进入质量评分。
func TestQualityServiceDefersCalendarBizDateUntilScheduledRun(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.TradingDayCalendar{}, &persistence.MetricValueDay{},
		&persistence.MetricValueRT{}, &persistence.MetricQualityDay{},
	)
	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "ORDER_CNT", SystemCode: "SYS", MetricKind: "normal", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.CollectFlow{
		FlowCode: "F_ORDER_CNT", Name: "订单量采集", MetricCode: "ORDER_CNT",
		ScheduleMode: "trading_day", TradingMarket: "SH", BizDateMode: "calendar",
		WindowTime: "00:15:00", Status: "active", Enabled: true,
		CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, day := range []struct {
		date    time.Time
		trading bool
	}{
		{time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), false},
		{time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local), false},
		{time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local), true},
	} {
		if err := h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: day.date, IsTrading: day.trading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[day.trading],
			Source:  "test",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	friday := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	value := 82.0
	if err := h.DB.Create(&persistence.MetricValueRT{
		MetricCode: "ORDER_CNT", Ts: friday.Add(time.Hour),
		RawValue: &value, CleanValue: &value, JudgeLevel: "normal",
		CreatedAt: time.Date(2026, 10, 19, 0, 20, 0, 0, time.Local),
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewQualityService(h.DB, nil)
	svc.now = func() time.Time {
		return time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local)
	}
	res, err := svc.Evaluate(context.Background(), "ORDER_CNT", friday, friday)
	if err != nil {
		t.Fatal(err)
	}
	if res.Score != 1 || len(res.Days) != 0 {
		t.Fatalf("weekend result = score %.4f days %v, want neutral with no premature row", res.Score, qualityDates(res.Days))
	}
	var prematureRows int64
	if err := h.DB.Model(&persistence.MetricQualityDay{}).
		Where("metric_code = ? AND biz_date = ?", "ORDER_CNT", friday).
		Count(&prematureRows).Error; err != nil {
		t.Fatal(err)
	}
	if prematureRows != 0 {
		t.Fatalf("premature quality rows = %d, want 0", prematureRows)
	}

	svc.now = func() time.Time {
		return time.Date(2026, 10, 19, 0, 20, 0, 0, time.Local)
	}
	res, err = svc.Evaluate(context.Background(), "ORDER_CNT", friday, friday)
	if err != nil {
		t.Fatal(err)
	}
	if res.Score != 1 || len(res.Days) != 1 || res.Days[0].ValidPoints == 0 {
		t.Fatalf("post-run result = score %.4f days %v, want Friday quality row with data", res.Score, qualityDates(res.Days))
	}
}

func TestQualityServiceCleanupRemovesOnlyPrematureNoDataRows(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.CapacityMetric{}, &persistence.CollectFlow{},
		&persistence.TradingDayCalendar{}, &persistence.MetricQualityDay{},
	)
	for code, cfg := range map[string]struct{ mode, window string }{
		"Q_PREV":    {mode: "calendar", window: "00:15:00"},
		"Q_NATURAL": {mode: "natural", window: "18:00:00"},
	} {
		if err := h.DB.Create(&persistence.CapacityMetric{
			MetricCode: code, SystemCode: "SYS", MetricKind: "normal", Status: "active",
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.DB.Create(&persistence.CollectFlow{
			FlowCode: "F_" + code, Name: code, MetricCode: code,
			ScheduleMode: "trading_day", TradingMarket: "SH", BizDateMode: cfg.mode,
			WindowTime: cfg.window, Status: "active", Enabled: true,
			CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, day := range []struct {
		date    time.Time
		trading bool
	}{
		{time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local), true},
		{time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), false},
	} {
		if err := h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: day.date, IsTrading: day.trading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[day.trading],
			Source:  "test",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	thursday := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	friday := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	for _, row := range []persistence.MetricQualityDay{
		{MetricCode: "Q_PREV", BizDate: thursday, SystemCode: "SYS", Flags: `["no_data"]`},
		{MetricCode: "Q_PREV", BizDate: friday, SystemCode: "SYS", Flags: `["no_data"]`},
		{MetricCode: "Q_NATURAL", BizDate: friday, SystemCode: "SYS", Flags: `["no_data"]`},
	} {
		if err := h.DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewQualityService(h.DB, nil)
	if err := svc.CleanupPremature(context.Background(),
		time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	assertQualityRowCount(t, h.DB, "Q_PREV", thursday, 1)
	assertQualityRowCount(t, h.DB, "Q_PREV", friday, 0)
	assertQualityRowCount(t, h.DB, "Q_NATURAL", friday, 1)
}

func qualityDates(rows []persistence.MetricQualityDay) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].BizDate.Format("2006-01-02"))
	}
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func assertQualityRowCount(t *testing.T, db *gorm.DB, metricCode string, bizDate time.Time, want int64) {
	t.Helper()
	var n int64
	if err := db.Model(&persistence.MetricQualityDay{}).
		Where("metric_code = ? AND biz_date = ?", metricCode, bizDate).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("quality rows %s %s = %d, want %d", metricCode, bizDate.Format("2006-01-02"), n, want)
	}
}
