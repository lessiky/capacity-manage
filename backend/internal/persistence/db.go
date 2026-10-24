package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"capacity-manage/backend/internal/config"
)

// Open 打开 PostgreSQL 连接（GORM）
func Open(ctx context.Context, cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
		// 与 DDL 表名一致: 单数 (sys_user / business_system ...)
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(64)
	sqlDB.SetMaxIdleConns(16)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}