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

func TestPlatformBrandingMigrationSeedsSystemParams(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:platform-branding-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysParam{}); err != nil {
		t.Fatalf("migrate sys_param: %v", err)
	}

	migration := filepath.Join("..", "..", "..", "db", "migrations", "0032_platform_branding.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read platform branding migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply platform branding migration statement: %v", err)
		}
	}

	var name SysParam
	if err := db.First(&name, "key = ?", "platform_name").Error; err != nil {
		t.Fatalf("platform_name should be visible in system params: %v", err)
	}
	if name.Value != "容量管理平台" {
		t.Fatalf("platform_name value = %q, want 容量管理平台", name.Value)
	}
	if name.Category != "general" {
		t.Fatalf("platform_name category = %q, want general", name.Category)
	}

	var icon SysParam
	if err := db.First(&icon, "key = ?", "platform_icon").Error; err != nil {
		t.Fatalf("platform_icon should be visible in system params: %v", err)
	}
	if icon.Value != "" {
		t.Fatalf("platform_icon value = %q, want empty", icon.Value)
	}
	if icon.Category != "general" {
		t.Fatalf("platform_icon category = %q, want general", icon.Category)
	}
}
