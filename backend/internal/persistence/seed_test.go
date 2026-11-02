package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestSeedRolesAndPermsIdempotentAfterMigrationInsert(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:seed-perms-%d?mode=memory&cache=shared", time.Now().UnixNano())),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysRole{}, &SysPermission{}, &SysRolePermission{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 0017_notification.sql 会先插入该权限；seed 不应再次插入并撞唯一键。
	if err := db.Create(&SysPermission{
		Code: "alert:policy:manage", Name: "告警升级策略管理", Type: "button", Sort: 410,
	}).Error; err != nil {
		t.Fatalf("preinsert permission: %v", err)
	}

	ctx := context.Background()
	if err := seedRolesAndPerms(ctx, db); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if err := seedRolesAndPerms(ctx, db); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	var roleCount, permCount, rolePermCount int64
	if err := db.Model(&SysRole{}).Count(&roleCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&SysPermission{}).Count(&permCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&SysRolePermission{}).Count(&rolePermCount).Error; err != nil {
		t.Fatal(err)
	}
	if roleCount != int64(len(builtinRoles)) {
		t.Fatalf("roles=%d, want %d", roleCount, len(builtinRoles))
	}
	if permCount != int64(len(builtinPerms)) {
		t.Fatalf("permissions=%d, want %d", permCount, len(builtinPerms))
	}
	if rolePermCount == 0 {
		t.Fatal("expected role-permission mappings")
	}
}

func TestSeedDoesNotCreateDemoAggregate(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:seed-base-%d?mode=memory&cache=shared", time.Now().UnixNano())),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&SysUser{},
		&SysRole{},
		&SysPermission{},
		&SysUserRole{},
		&SysRolePermission{},
		&TradingDayCalendar{},
		&CapacityMetric{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := Seed(context.Background(), db); err != nil {
		t.Fatalf("seed base data: %v", err)
	}

	var n int64
	if err := db.Model(&CapacityMetric{}).
		Where("metric_code = ?", "SRV_REQ").
		Count(&n).Error; err != nil {
		t.Fatalf("count demo metrics: %v", err)
	}
	if n != 0 {
		t.Fatalf("default seed created %d demo aggregate metrics, want 0", n)
	}
}

// 回归: 曾因 Truncate(24h) 时区错移导致整表日期错位一天（周五被标休市/周日被标交易）。
func TestSeedCalendarRow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	cases := []struct {
		date      string
		wantTrade bool
		wantType  string
	}{
		{"2026-10-15", true, "trading"},  // 周四
		{"2026-10-16", true, "trading"},  // 周五（历史缺陷: 曾被错标为非交易日）
		{"2026-10-17", false, "weekend"}, // 周六
		{"2026-10-18", false, "weekend"}, // 周日（历史缺陷: 曾被错标为交易日）
		{"2026-10-19", true, "trading"},  // 周一
		{"2026-01-01", false, "holiday"}, // 元旦
		{"2026-10-01", false, "holiday"}, // 国庆
		{"2027-02-08", false, "holiday"}, // 春节
	}
	for _, tc := range cases {
		d, err := time.ParseInLocation("2006-01-02", tc.date, loc)
		if err != nil {
			t.Fatal(err)
		}
		got := seedCalendarRow(d)
		if got.IsTrading != tc.wantTrade {
			t.Errorf("%s isTrading=%v, want %v", tc.date, got.IsTrading, tc.wantTrade)
		}
		if got.DayType != tc.wantType {
			t.Errorf("%s dayType=%s, want %s", tc.date, got.DayType, tc.wantType)
		}
		if got.CalDate.Format("2006-01-02") != tc.date {
			t.Errorf("%s CalDate=%s 日期错位", tc.date, got.CalDate.Format("2006-01-02 15:04:05"))
		}
	}
}
