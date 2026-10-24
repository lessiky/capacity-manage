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

func durationPtr(v int) *int { return &v }

// 日汇总/补采回放会把持续时长写成负数, 迁移需把已落库的负值回收为 0, 且不动正常值。
func TestAlertDurationMigrationClampsNegativeDurations(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:alert-duration-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AlertRecord{}); err != nil {
		t.Fatalf("migrate alert_record: %v", err)
	}

	negative := AlertRecord{
		AlertNo: "AL_NEG", MetricCode: "M_NEG", Level: "p3",
		AlertType: "threshold", Status: "recovered", DurationMin: durationPtr(-600),
	}
	positive := AlertRecord{
		AlertNo: "AL_POS", MetricCode: "M_POS", Level: "p3",
		AlertType: "threshold", Status: "active", DurationMin: durationPtr(120),
	}
	if err := db.Create(&negative).Error; err != nil {
		t.Fatalf("seed negative alert: %v", err)
	}
	if err := db.Create(&positive).Error; err != nil {
		t.Fatalf("seed positive alert: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0030_alert_duration_nonnegative.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read alert duration migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply alert duration migration statement: %v", err)
		}
	}

	var gotNegative AlertRecord
	if err := db.First(&gotNegative, "alert_no = ?", "AL_NEG").Error; err != nil {
		t.Fatalf("reload negative alert: %v", err)
	}
	if gotNegative.DurationMin == nil || *gotNegative.DurationMin != 0 {
		t.Fatalf("negative duration = %v, want 0", gotNegative.DurationMin)
	}

	var gotPositive AlertRecord
	if err := db.First(&gotPositive, "alert_no = ?", "AL_POS").Error; err != nil {
		t.Fatalf("reload positive alert: %v", err)
	}
	if gotPositive.DurationMin == nil || *gotPositive.DurationMin != 120 {
		t.Fatalf("positive duration = %v, want 120 (迁移不得改动正常值)", gotPositive.DurationMin)
	}
}
