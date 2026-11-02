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

func TestMissingCheckTimeMigrationSeedsSystemParam(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:missing-check-time-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysParam{}); err != nil {
		t.Fatalf("migrate sys_param: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0035_missing_check_time_param.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read missing_check_time migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply missing_check_time migration statement: %v", err)
		}
	}

	var param SysParam
	if err := db.First(&param, "key = ?", "missing_check_time").Error; err != nil {
		t.Fatalf("missing_check_time should be visible in system params: %v", err)
	}
	if param.Value != "09:00" {
		t.Fatalf("missing_check_time value = %q, want 09:00", param.Value)
	}
	if param.Category != "schedule" {
		t.Fatalf("missing_check_time category = %q, want schedule", param.Category)
	}
}
