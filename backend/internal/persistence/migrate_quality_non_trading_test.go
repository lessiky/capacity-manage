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

func TestQualityNonTradingMigrationCleansOnlyTradingDayRows(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:quality-non-trading-%d?mode=memory&cache=shared", time.Now().UnixNano())),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&CapacityMetric{}, &CollectFlow{}, &TradingDayCalendar{}, &MetricQualityDay{},
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

	seedMigrationMetricFlow(t, db, "Q_CAL", "calendar", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "Q_NATURAL", "natural", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "Q_PREV", "prev_day", "trading_day", "active", true)
	seedMigrationMetricFlow(t, db, "Q_NORMAL", "natural", "normal", "active", true)

	rows := []MetricQualityDay{
		{MetricCode: "Q_CAL", BizDate: weekend, SystemCode: "S1", Score: 0, Flags: "[]"},
		{MetricCode: "Q_NATURAL", BizDate: weekend, SystemCode: "S1", Score: 0, Flags: "[]"},
		{MetricCode: "Q_PREV", BizDate: weekend, SystemCode: "S1", Score: 0, Flags: "[]"},
		{MetricCode: "Q_NORMAL", BizDate: weekend, SystemCode: "S1", Score: 0, Flags: "[]"},
		{MetricCode: "Q_CAL", BizDate: tradingDay, SystemCode: "S1", Score: 1, Flags: "[]"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create quality rows: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0039_quality_non_trading_rows.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read quality non-trading migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply quality non-trading migration statement: %v", err)
		}
	}

	assertQualityMigrationRowCount(t, db, "Q_CAL", weekend, 0)
	assertQualityMigrationRowCount(t, db, "Q_NATURAL", weekend, 0)
	assertQualityMigrationRowCount(t, db, "Q_PREV", weekend, 1)
	assertQualityMigrationRowCount(t, db, "Q_NORMAL", weekend, 1)
	assertQualityMigrationRowCount(t, db, "Q_CAL", tradingDay, 1)
}

func assertQualityMigrationRowCount(t *testing.T, db *gorm.DB, metricCode string, bizDate time.Time, want int64) {
	t.Helper()
	var got int64
	if err := db.Model(&MetricQualityDay{}).
		Where("metric_code = ? AND biz_date = ?", metricCode, bizDate).
		Count(&got).Error; err != nil {
		t.Fatalf("count %s %s: %v", metricCode, bizDate.Format("2006-01-02"), err)
	}
	if got != want {
		t.Fatalf("rows %s %s = %d, want %d", metricCode, bizDate.Format("2006-01-02"), got, want)
	}
}
