package persistence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestDailyReportTimeMigrationSeedsSystemParam(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:daily-report-time-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysParam{}); err != nil {
		t.Fatalf("migrate sys_param: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0028_daily_report_time_param.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read daily_report_time migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply daily_report_time migration statement: %v", err)
		}
	}

	var param SysParam
	if err := db.First(&param, "key = ?", "daily_report_time").Error; err != nil {
		t.Fatalf("daily_report_time should be visible in system params: %v", err)
	}
	if param.Value != "00:30" {
		t.Fatalf("daily_report_time value = %q, want 00:30", param.Value)
	}
	if param.Category != "schedule" {
		t.Fatalf("daily_report_time category = %q, want schedule", param.Category)
	}
}

func TestDailyForecastTimeMigrationSeedsSystemParam(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:daily-forecast-time-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysParam{}); err != nil {
		t.Fatalf("migrate sys_param: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0029_daily_forecast_time_param.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read daily_forecast_time migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply daily_forecast_time migration statement: %v", err)
		}
	}

	var param SysParam
	if err := db.First(&param, "key = ?", "daily_forecast_time").Error; err != nil {
		t.Fatalf("daily_forecast_time should be visible in system params: %v", err)
	}
	if param.Value != "00:45" {
		t.Fatalf("daily_forecast_time value = %q, want 00:45", param.Value)
	}
	if param.Category != "schedule" {
		t.Fatalf("daily_forecast_time category = %q, want schedule", param.Category)
	}
}
