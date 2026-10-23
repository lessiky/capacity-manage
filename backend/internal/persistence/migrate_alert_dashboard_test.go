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

func migrateAlertDashboard(t *testing.T, db *gorm.DB) {
	t.Helper()
	migration := filepath.Join("..", "..", "..", "db", "migrations", "0031_alert_dashboard_preference.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read alert dashboard migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply alert dashboard migration statement: %v", err)
		}
	}
}

func TestAlertDashboardPreferenceMigrationDefaults(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:alert-dashboard-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysUser{}); err != nil {
		t.Fatalf("migrate sys_user: %v", err)
	}
	user := SysUser{Username: "dashboard-user", PasswordHash: "x", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	migrateAlertDashboard(t, db)

	if err := db.Exec("INSERT INTO alert_dashboard_preference (user_id) VALUES (?)", user.ID).Error; err != nil {
		t.Fatalf("insert default preference: %v", err)
	}
	var got struct {
		Levels     string `gorm:"column:levels"`
		Statuses   string `gorm:"column:statuses"`
		AlertTypes string `gorm:"column:alert_types"`
		SystemCode string `gorm:"column:system_codes"`
		DateMode   string `gorm:"column:date_mode"`
		RecentDays int    `gorm:"column:recent_days"`
		SortRules  string `gorm:"column:sort_rules"`
	}
	err = db.Raw(`SELECT levels, statuses, alert_types, system_codes, date_mode, recent_days, sort_rules
		FROM alert_dashboard_preference WHERE user_id = ?`, user.ID).Scan(&got).Error
	if err != nil {
		t.Fatalf("read default preference: %v", err)
	}
	if got.Levels != `["p1","p2","p3","p4"]` {
		t.Fatalf("levels = %s", got.Levels)
	}
	if got.Statuses != `["active","processing","recovered","closed"]` {
		t.Fatalf("statuses = %s", got.Statuses)
	}
	if got.AlertTypes != `["threshold","missing","collect_fail","anomaly","ds_error"]` {
		t.Fatalf("alert types = %s", got.AlertTypes)
	}
	if got.SystemCode != `[]` {
		t.Fatalf("system codes = %s", got.SystemCode)
	}
	if got.DateMode != "today" || got.RecentDays != 7 {
		t.Fatalf("date defaults = %s/%d", got.DateMode, got.RecentDays)
	}
	if got.SortRules != `[{"field":"triggerAt","direction":"desc"}]` {
		t.Fatalf("sort rules = %s", got.SortRules)
	}
}

func TestAlertDashboardPreferenceMigrationChecksDateBounds(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:alert-dashboard-check?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysUser{}); err != nil {
		t.Fatalf("migrate sys_user: %v", err)
	}
	user := SysUser{Username: "check-user", PasswordHash: "x", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	migrateAlertDashboard(t, db)

	if err := db.Exec(`INSERT INTO alert_dashboard_preference (user_id, recent_days) VALUES (?, 0)`, user.ID).Error; err == nil {
		t.Fatal("recent_days=0 must violate the migration check constraint")
	}
	if err := db.Exec(`INSERT INTO alert_dashboard_preference (user_id, date_mode) VALUES (?, 'yesterday')`, user.ID).Error; err == nil {
		t.Fatal("date_mode=yesterday must violate the migration check constraint")
	}
}

func TestAlertDashboardSortRulesJSONRoundTrip(t *testing.T) {
	want := AlertDashboardSortRules{
		{Field: "level", Direction: "asc"},
		{Field: "triggerAt", Direction: "desc"},
	}
	value, err := want.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	var got AlertDashboardSortRules
	if err := got.Scan(value); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 2 || got[0].Field != "level" || got[1].Direction != "desc" {
		t.Fatalf("round trip = %+v", got)
	}
}
