package persistence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestDailySummaryMissingMigrationCleansOnlyInvalidNonTradingRows(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:daily-summary-missing-%d?mode=memory&cache=shared", time.Now().UnixNano())),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&CapacityMetric{}, &CollectFlow{}, &TradingDayCalendar{}, &MetricValueDay{},
	); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}

	weekend := time.Date(2026, 10, 31, 0, 0, 0, 0, time.Local)
	tradingDay := time.Date(2026, 10, 30, 0, 0, 0, 0, time.Local)
	for _, day := range []TradingDayCalendar{
		{Market: "SH", CalDate: tradingDay, IsTrading: true, DayType: "trading", Source: "test"},
		{Market: "SH", CalDate: weekend, IsTrading: false, DayType: "weekend", Source: "test"},
	} {
		if err := db.Create(&day).Error; err != nil {
			t.Fatalf("create calendar: %v", err)
		}
	}

	seedMigrationMetricFlow(t, db, "M_CAL", "calendar", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "M_NATURAL", "natural", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "M_PREV_DAY", "prev_day", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "M_INACTIVE", "calendar", "trading_day", "disabled", true)
	seedMigrationMetricFlow(t, db, "M_NORMAL", "natural", "normal", "active", true)
	seedMigrationMetricFlow(t, db, "M_VALUED", "calendar", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "M_CORRECTED", "calendar", "trading_day", "active", true)

	rows := []MetricValueDay{
		{MetricCode: "M_CAL", BizDate: weekend, Level: "missing", PointCount: 0},
		{MetricCode: "M_NATURAL", BizDate: weekend, Level: "missing", PointCount: 0},
		{MetricCode: "M_PREV_DAY", BizDate: weekend, Level: "missing", PointCount: 0},
		{MetricCode: "M_INACTIVE", BizDate: weekend, Level: "missing", PointCount: 0},
		{MetricCode: "M_NORMAL", BizDate: weekend, Level: "missing", PointCount: 0},
		{MetricCode: "M_CAL", BizDate: tradingDay, Level: "missing", PointCount: 0},
	}
	value := 12.5
	rows = append(rows, MetricValueDay{
		MetricCode: "M_VALUED", BizDate: weekend, Level: "missing", StatValue: &value, PointCount: 1,
	})
	rows = append(rows, MetricValueDay{
		MetricCode: "M_CORRECTED", BizDate: weekend, Level: "missing", PointCount: 0, Corrected: true,
	})
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create day rows: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0038_daily_summary_non_trading_missing.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read daily summary missing migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply daily summary missing migration statement: %v", err)
		}
	}

	assertMigrationRowCount(t, db, "M_CAL", weekend, 0)
	assertMigrationRowCount(t, db, "M_NATURAL", weekend, 0)
	assertMigrationRowCount(t, db, "M_PREV_DAY", weekend, 1)
	assertMigrationRowCount(t, db, "M_INACTIVE", weekend, 0)
	assertMigrationRowCount(t, db, "M_NORMAL", weekend, 1)
	assertMigrationRowCount(t, db, "M_CAL", tradingDay, 1)
	assertMigrationRowCount(t, db, "M_VALUED", weekend, 1)
	assertMigrationRowCount(t, db, "M_CORRECTED", weekend, 1)
}

func seedMigrationMetricFlow(t *testing.T, db *gorm.DB, code, bizDateMode, scheduleMode, status string, enabled bool) {
	t.Helper()
	if err := db.Create(&CapacityMetric{
		MetricCode: code, Name: code, SystemCode: "S1", MetricKind: "normal",
		MetricType: "absolute", Unit: "-", StatMode: "latest", Status: status,
	}).Error; err != nil {
		t.Fatalf("create metric %s: %v", code, err)
	}
	if err := db.Create(&CollectFlow{
		FlowCode: "F_" + code, Name: code, MetricCode: code,
		ScheduleMode: scheduleMode, TradingMarket: "SH", BizDateMode: bizDateMode,
		Status: status, Enabled: enabled,
	}).Error; err != nil {
		t.Fatalf("create flow %s: %v", code, err)
	}
}

func assertMigrationRowCount(t *testing.T, db *gorm.DB, metricCode string, bizDate time.Time, want int64) {
	t.Helper()
	var got int64
	if err := db.Model(&MetricValueDay{}).
		Where("metric_code = ? AND biz_date = ?", metricCode, bizDate).
		Count(&got).Error; err != nil {
		t.Fatalf("count %s %s: %v", metricCode, bizDate.Format("2006-01-02"), err)
	}
	if got != want {
		t.Fatalf("rows %s %s = %d, want %d", metricCode, bizDate.Format("2006-01-02"), got, want)
	}
}
