// capacity-server 容量管理平台后端入口
// 用法:
//
//	capacity-server migrate  # 执行 DDL 迁移
//	capacity-server seed     # 初始化种子数据(管理员/角色/日历)
//	capacity-server server   # 启动服务(默认)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"capacity-manage/backend/internal/app"
	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
	"capacity-manage/backend/internal/pkg/store"

	"github.com/redis/go-redis/v9"
)

func main() {
	cmd := "server"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	cfg := config.Get()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "migrate":
		run(ctx, cfg, logger, func(d *app.Deps) error {
			return persistence.Migrate(ctx, d.DB, cfg.MigrationFile)
		})
	case "seed":
		run(ctx, cfg, logger, func(d *app.Deps) error {
			return persistence.Seed(ctx, d.DB)
		})
	case "server":
		run(ctx, cfg, logger, func(d *app.Deps) error {
			ap, err := app.New(ctx, d)
			if err != nil {
				return err
			}
			logger.Info("capacity-server starting",
				slog.String("env", cfg.Env), slog.Int("port", cfg.HTTPPort),
				slog.Bool("scheduler", cfg.SchedulerEnabled))
			return ap.Run(ctx)
		})
	case "version":
		fmt.Println("capacity-server", app.Version())
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s (migrate|seed|server|version)\n", cmd)
		os.Exit(2)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger, fn func(*app.Deps) error) {
	d, err := buildDeps(ctx, cfg, logger)
	if err != nil {
		logger.Error("init deps failed", slog.Any("err", err))
		os.Exit(1)
	}
	if err := fn(d); err != nil {
		logger.Error("command failed", slog.Any("err", err))
		os.Exit(1)
	}
	if sqlDB, err := d.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func buildDeps(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*app.Deps, error) {
	db, err := persistence.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	st, err := store.NewLocal(cfg.FileRoot)
	if err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}
	cipher, err := aesgcm.New(cfg.CredKey)
	if err != nil {
		return nil, fmt.Errorf("cred cipher: %w", err)
	}
	return &app.Deps{Cfg: cfg, DB: db, RDB: rdb, Store: st, Cipher: cipher, Log: logger}, nil
}