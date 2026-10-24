package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/ai"
	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/auth"
	"capacity-manage/backend/internal/bizsystem"
	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/data"
	"capacity-manage/backend/internal/datasource"
	"capacity-manage/backend/internal/flow"
	"capacity-manage/backend/internal/forecast"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/metric"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/openapi"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pkg/store"
	"capacity-manage/backend/internal/pyclient"
	"capacity-manage/backend/internal/report"
	"capacity-manage/backend/internal/scheduler"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Deps 依赖集合（由 main 组装）
type Deps struct {
	Cfg    *config.Config
	DB     *gorm.DB
	RDB    *redis.Client
	Store  store.Store
	Cipher *aesgcm.Cipher
	Log    *slog.Logger
}

// App 组装后的应用
type App struct {
	Engine    *gin.Engine
	Deps      *Deps
	Scheduler *scheduler.Scheduler
	AuthZ     *middleware.AuthZ
}

var version = "0.1.0-m0"

// New 构建应用：中间件、模块服务、路由、调度器
func New(ctx context.Context, d *Deps) (*App, error) {
	if d.Cfg.Env == "prod" || d.Cfg.Env == "uat" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(middleware.Recovery(), gin.Recovery())

	az, err := middleware.NewAuthZ(d.DB)
	if err != nil {
		return nil, err
	}

	// 基础服务
	params := system.NewParams(d.DB)
	cal := tradingday.NewCalendar(d.DB, d.RDB, params)
	cred := datasource.NewRegistry(d.DB, d.Cipher, d.Cfg, d.Log)
	// 数据源连接测试: 模板变量按当前日期渲染(与采集执行同口径), 避免按字面 ${prev_trading_day} 占位路径测试
	cred.SetTemplateRenderer(func(ctx context.Context, template string) string {
		if template == "" {
			return template
		}
		vars, err := flow.ResolveVars(ctx, cal, "SH", time.Now(), "", "", "", false)
		if err != nil {
			return template // 推导失败退回原模板, 不阻断测试
		}
		rendered, err := flow.RenderTemplate(template, vars, nil, nil)
		if err != nil {
			return template
		}
		return rendered
	})
	dataStore := data.NewStore(d.DB)
	judger := judge.New(params, d.DB)
	alertSvc := alert.New(d.DB, params, cal)
	notifyMgr := alert.NewNotificationManager(d.DB, d.Cipher, params, d.Log)
	alertSvc.SetNotificationManager(notifyMgr)
	py := pyclient.New(d.Cfg.WorkerURL, d.Cfg.WorkerTimeout)
	collectors := flow.NewCollectors(d.DB, cred, py, params, cal)
	flowEngine := flow.NewEngine(d.DB, dataStore, judger, alertSvc, collectors, cal, params, d.Log)
	sched := scheduler.New(d.DB, flowEngine, cal, params, d.Log)

	// 审计注入
	middleware.SetAudit(func(c *gin.Context, module, action, target, detail string) {
		operateLog(d.DB, c, module, action, target, detail)
		d.Log.Info("operate",
			slog.String("module", module), slog.String("action", action),
			slog.String("target", target),
		)
	})

	// 模块 handler
	authH := auth.NewHandler(d.DB, d.RDB, d.Cfg, params)
	sysH := system.NewHandler(d.DB, params, az, d.Log)
	bizH := bizsystem.NewHandler(d.DB, az, d.Log)
	metH := metric.NewHandler(d.DB, az, d.Log)
	tdH := tradingday.NewHandler(cal, d.DB, d.Log)
	dsH := datasource.NewHandler(d.DB, cred, az, d.Cfg, d.Log)
	flowH := flow.NewHandler(d.DB, flowEngine, az, py, d.Log)
	taskH := flow.NewTaskHandler(d.DB, az)
	dataH := data.NewHandler(dataStore, d.DB, az, params, cal, d.Log)
	alertH := alert.NewHandler(d.DB, az, params, d.Log, d.Cipher)
	dashH := dashboardHandler(d.DB, az)
	rptH := report.NewHandler(d.DB, d.Store, params, az, d.Log)
	fcH := forecast.NewHandler(d.DB, az, d.Log)
	aiH := ai.NewHandler(d.Cfg, d.DB, az, d.Log, py, params)
	oaH := openapi.NewHandler(d.DB, d.RDB, az, d.Log)
	oaH.SetArchive(openapi.NewArchiveService(d.DB, d.Store))
	// AI 配置可经系统管理热更新，因此用桥接对象在每次调用时取最新配置；
	// 未启用时预测与事件建议自动回退（计划 §9.3）。
	fcH.SetAI(aiH.ChatFunc)
	backfillSvc := data.NewBackfillService(d.DB, dataStore, judger, alertSvc, params)
	backfillSvc.SetRunner(func(ctx context.Context, flowID int64, bizDate time.Time) error {
		res, err := flowEngine.RunBackfill(ctx, flowID, bizDate, "backfill")
		if err != nil {
			return err
		}
		if res == nil || !res.OK {
			if res == nil {
				return fmt.Errorf("补采执行失败")
			}
			return fmt.Errorf("补采执行失败: %s", res.ErrMsg)
		}
		return nil
	})
	dataH.SetBackfillService(backfillSvc)

	v1 := engine.Group("/api/v1")
	registerRoutes(d.DB, v1, d.RDB, d.Cfg.JWTSecret, d.Cfg.JWTAccessTTL, az, authH, sysH, bizH, metH, tdH, dsH, flowH, taskH, dataH, alertH, dashH, rptH, fcH, aiH, oaH)

	// 健康检查
	engine.GET("/healthz", func(c *gin.Context) { resp.OK(c, gin.H{"status": "ok"}) })
	engine.GET("/readyz", func(c *gin.Context) {
		issues := []string{}
		if sqlDB, err := d.DB.DB(); err != nil {
			issues = append(issues, "db")
		} else if err := sqlDB.PingContext(c.Request.Context()); err != nil {
			issues = append(issues, "db")
		}
		if err := d.RDB.Ping(c).Err(); err != nil {
			issues = append(issues, "redis")
		}
		if len(issues) > 0 {
			c.JSON(http.StatusServiceUnavailable, resp.Body{Code: resp.CodeDepUnavailable, Message: "deps not ready: " + joinAll(issues)})
			return
		}
		resp.OK(c, gin.H{"status": "ready"})
	})
	engine.GET("/version", func(c *gin.Context) {
		resp.OK(c, gin.H{"name": "capacity-server", "version": version, "env": d.Cfg.Env})
	})

	// 调度器回调注入（避免模块间循环依赖）
	sched.SetSummary(data.RunDailySummary)
	sched.SetReportGen(func(ctx2 context.Context, db2 *gorm.DB, bizDate time.Time) error {
		res, err := report.GenerateDaily(ctx2, db2, d.Store, bizDate)
		if err != nil {
			d.Log.Error("daily report generate failed",
				slog.String("bizDate", bizDate.Format("2006-01-02")), slog.Any("err", err))
			return err
		}
		d.Log.Info("daily report generated",
			slog.String("bizDate", bizDate.Format("2006-01-02")), slog.String("fileRef", res.FileRef))
		return nil
	})
	sched.SetAlerts(alertSvc)
	// M7: 日汇总与数据质量完成后的统计预测, 以及报表订阅到期推送
	forecastSvc := fcH.Service()
	sched.SetForecast(func(ctx2 context.Context, db2 *gorm.DB, bizDate time.Time) error {
		return forecastSvc.RunDaily(ctx2, bizDate)
	})
	reportSvc := report.NewService(d.DB, d.Store)
	sched.SetReportSubscriptions(func(ctx2 context.Context, db2 *gorm.DB, now time.Time) error {
		return reportSvc.RunDueSubscriptions(ctx2, now, notifyMgr.SendGeneric)
	})
	if err := report.EnsureReportDefinitions(ctx, d.DB); err != nil {
		d.Log.Warn("ensure report definitions failed", slog.Any("err", err))
	}
	// M8: 超期数据压缩归档（按月分区，默认 3 年在线保留期）
	if archiveSvc := oaH.ArchiveService(); archiveSvc != nil {
		sched.SetArchive(func(ctx2 context.Context, now time.Time) error {
			_, _, err := archiveSvc.RetentionSweep(ctx2,
				sysParamInt(d.DB, "rt_retain_days", 90),
				sysParamInt(d.DB, "day_retain_days", 1095))
			return err
		})
	}
	sched.SetHealthCheck(func(ctx2 context.Context, src *persistence.DataSource) (bool, string) {
		r, _ := cred.Test(ctx2, src)
		if r.OK {
			if src.HealthFailStreak > 0 {
				_ = d.DB.Model(src).Update("health_fail_streak", 0).Error
			}
			return true, r.Message
		}
		src.HealthFailStreak++
		_ = d.DB.Model(src).Update("health_fail_streak", src.HealthFailStreak).Error
		// FR-COL-04: 连续异常达到阈值时生成平台内 P2 告警（仅首次触发, 防重复通知）
		if streak := params.Int("ds_health_alert_streak", 3); streak > 0 && src.HealthFailStreak == streak {
			alertSvc.OnDataSourceError(ctx2, src, r.Message)
		}
		return false, r.Message
	})

	// 平台自检扩展数据（FR-ADM-08 基础版）
	sysH.SetHealthProvider(func(c *gin.Context) gin.H {
		out := gin.H{}
		ok, werr := py.Health(c.Request.Context())
		if werr != nil {
			out["worker"] = gin.H{"ok": false, "error": werr.Error()}
		} else {
			out["worker"] = gin.H{"ok": ok}
		}
		var dbSize int64
		_ = d.DB.Raw("SELECT pg_database_size(current_database())").Scan(&dbSize).Error
		fileSize, ferr := d.Store.Size()
		if ferr != nil {
			out["storage"] = gin.H{"dbBytes": dbSize, "fileBytes": 0, "error": ferr.Error()}
		} else {
			out["storage"] = gin.H{"dbBytes": dbSize, "fileBytes": fileSize}
		}
		var sums []persistence.DailySummaryLog
		_ = d.DB.Order("biz_date DESC").Limit(5).Find(&sums).Error
		out["dailySummaries"] = sums
		var reports []persistence.ReportResult
		_ = d.DB.Where("report_type = 'daily'").Order("biz_date DESC").Limit(5).Find(&reports).Error
		out["dailyReports"] = reports
		out["scheduler"] = sched.Snapshot()
		out["reportTime"] = params.Get("daily_report_time", "00:30")
		return out
	})

	// 调度器
	if d.Cfg.SchedulerEnabled {
		if err := sched.Start(ctx); err != nil {
			return nil, err
		}
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = notifyMgr.RetryDue(ctx, 50)
				}
			}
		}()
	}

	return &App{Engine: engine, Deps: d, Scheduler: sched, AuthZ: az}, nil
}

func joinAll(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// sysParamInt 读取 sys_param 的整数配置，缺失或非法时回退默认值。
func sysParamInt(db *gorm.DB, key string, fallback int) int {
	var row struct{ Value string }
	if err := db.Table("sys_param").Select("value").Where("key = ?", key).Scan(&row).Error; err != nil {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(row.Value))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// Run 启动 HTTP 服务（阻塞直至 ctx 取消）
func (a *App) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    ":" + strconv.Itoa(a.Deps.Cfg.HTTPPort),
		Handler: a.Engine,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
		return nil
	case err := <-errCh:
		return err
	}
}
