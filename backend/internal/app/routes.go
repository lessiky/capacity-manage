package app

import (
	"context"
	"strconv"
	"time"

	"capacity-manage/backend/internal/ai"
	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/auth"
	"capacity-manage/backend/internal/bizsystem"
	"capacity-manage/backend/internal/data"
	"capacity-manage/backend/internal/datasource"
	"capacity-manage/backend/internal/flow"
	"capacity-manage/backend/internal/forecast"
	"capacity-manage/backend/internal/metric"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/openapi"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pkg/scope"
	"capacity-manage/backend/internal/report"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// operateLog 审计写入
func operateLog(db *gorm.DB, c *gin.Context, module, action, target, detail string) {
	uid, _ := c.Get(middleware.CtxUserID)
	uname, _ := c.Get(middleware.CtxUsername)
	un, _ := uname.(string)
	var userID *int64
	if v, ok := uid.(int64); ok {
		userID = &v
	}
	_ = db.Create(&persistence.SysOperateLog{
		UserID: userID, Username: un, Module: module,
		Action: action, Target: target, Detail: detail, IP: c.ClientIP(),
	}).Error
}

// registerRoutes 全部一期 API（docs/10-design/03-API规范.md）
func registerRoutes(
	db *gorm.DB,
	v1 *gin.RouterGroup,
	rdb *redis.Client,
	jwtSecret string,
	sessionTTL time.Duration,
	az *middleware.AuthZ,
	authH *auth.Handler,
	sysH *system.Handler,
	bizH *bizsystem.Handler,
	metH *metric.Handler,
	tdH *tradingday.Handler,
	dsH *datasource.Handler,
	flowH *flow.Handler,
	taskH *flow.TaskHandler,
	dataH *data.Handler,
	alertH *alert.Handler,
	dashH *dashboard,
	rptH *report.Handler,
	fcH *forecast.Handler,
	aiH *ai.Handler,
	oaH *openapi.Handler,
) {
	auth := v1.Group("/auth")
	{
		auth.POST("/login", authH.Login)
	}

	// 对外开放数据服务（X-API-Key + scope）: 必须在 v1.Use(JWT) 之前注册，
	// 否则会被会话中间件拦截，第三方无法携带 API Key 直接访问（FR-DAY-07）。
	open := v1.Group("/open")
	{
		open.GET("/stream/day", oaH.RequireScope(openapi.ScopeDataRead), oaH.OpenDayData)
		open.GET("/stream/rt", oaH.RequireScope(openapi.ScopeDataRead), oaH.OpenRTData)
		open.GET("/forecast", oaH.RequireScope(openapi.ScopeForecastRead), oaH.OpenForecast)
	}
	v1.GET("/branding", sysH.Branding)

	// /auth 之外的全部路由要求 JWT 认证 + 会话滑动刷新
	v1.Use(middleware.Auth(rdb, jwtSecret), middleware.SlideSession(rdb, sessionTTL))

	protectedAuth := v1.Group("/auth")
	{
		protectedAuth.POST("/logout", authH.Logout)
		protectedAuth.GET("/me", authH.Me)
		protectedAuth.POST("/password", authH.ChangePassword)
	}

	admin := v1.Group("/admin")
	{
		admin.GET("/users", az.RequirePerm("admin:user:manage"), sysH.ListUsers)
		admin.POST("/users", az.RequirePerm("admin:user:manage"), sysH.CreateUser)
		admin.PUT("/users/:id", az.RequirePerm("admin:user:manage"), sysH.UpdateUser)
		admin.DELETE("/users/:id", az.RequirePerm("admin:user:manage"), sysH.DeleteUser)
		admin.POST("/users/:id/reset-password", az.RequirePerm("admin:user:manage"), sysH.ResetPassword)
		admin.PUT("/users/:id/roles", az.RequirePerm("admin:user:manage"), sysH.SetUserRoles)
		admin.GET("/roles", az.RequirePerm("admin:role:manage"), sysH.ListRoles)
		admin.POST("/roles", az.RequirePerm("admin:role:manage"), sysH.CreateRole)
		admin.PUT("/roles/:id", az.RequirePerm("admin:role:manage"), sysH.UpdateRole)
		admin.DELETE("/roles/:id", az.RequirePerm("admin:role:manage"), sysH.DeleteRole)
		admin.GET("/permissions", az.RequirePerm("admin:role:manage"), sysH.ListPermissions)
		admin.GET("/data-scope", az.RequirePerm("admin:datascope:manage"), sysH.GetDataScope)
		admin.PUT("/data-scope", az.RequirePerm("admin:datascope:manage"), sysH.PutDataScope)
		admin.DELETE("/data-scope/:id", az.RequirePerm("admin:datascope:manage"), sysH.DeleteDataScope)
		admin.GET("/params", az.RequirePerm("admin:param:manage"), sysH.ListParams)
		admin.PUT("/params", az.RequirePerm("admin:param:manage"), sysH.PutParam)
		admin.POST("/params/platform-icon", az.RequirePerm("admin:param:manage"), sysH.UploadPlatformIcon)
		admin.DELETE("/params/platform-icon", az.RequirePerm("admin:param:manage"), sysH.ClearPlatformIcon)
		admin.GET("/ai/config", az.RequirePerm("admin:param:manage"), aiH.GetConfig)
		admin.PUT("/ai/config", az.RequirePerm("admin:param:manage"), aiH.PutConfig)
		admin.POST("/ai/test", az.RequirePerm("admin:param:manage"), aiH.TestConnection)
		admin.GET("/operate-logs", az.RequirePerm("admin:log:view"), sysH.ListOperateLogs)
		admin.GET("/health", az.RequirePerm("admin:health:view"), sysH.Health)
	}

	sys := v1.Group("/systems")
	{
		sys.GET("", az.RequirePerm("system:view"), bizH.List)
		sys.POST("", az.RequirePerm("system:manage"), bizH.Create)
		sys.GET("/export", az.RequirePerm("system:export"), bizH.Export)
		sys.GET("/:code", az.RequirePerm("system:view"), requireScope(az, db), bizH.Get)
		sys.PUT("/:code", az.RequirePerm("system:manage"), requireScope(az, db), bizH.Update)
		sys.DELETE("/:code", az.RequirePerm("system:manage"), requireScope(az, db), bizH.Delete)
		sys.POST("/:code/status", az.RequirePerm("system:manage"), requireScope(az, db), bizH.SetStatus)
		sys.GET("/:code/resources", az.RequirePerm("system:view"), requireScope(az, db), bizH.ListResources)
		sys.PUT("/:code/resources", az.RequirePerm("system:manage"), requireScope(az, db), bizH.SaveResources)
	}

	td := v1.Group("/trading-days")
	{
		td.GET("", az.RequirePerm("tradingday:view"), tdH.List)
		td.PUT("", az.RequirePerm("tradingday:manage"), tdH.Save)
		td.POST("/import", az.RequirePerm("tradingday:manage"), tdH.Import)
		td.GET("/export", az.RequirePerm("tradingday:view"), tdH.Export)
		td.GET("/template", az.RequirePerm("tradingday:manage"), tdH.Template)
		td.POST("/derive", az.RequirePerm("tradingday:view"), tdH.Derive)
	}

	ds := v1.Group("/data-sources")
	{
		ds.GET("", az.RequirePerm("datasource:view"), dsH.List)
		ds.POST("", az.RequirePerm("datasource:manage"), dsH.Create)
		ds.POST("/import", az.RequirePerm("datasource:manage"), dsH.Import)
		ds.GET("/:id", az.RequirePerm("datasource:view"), dsH.Get)
		ds.PUT("/:id", az.RequirePerm("datasource:manage"), dsH.Update)
		ds.POST("/:id/test", az.RequirePerm("datasource:manage"), dsH.Test)
		ds.POST("/:id/disable", az.RequirePerm("datasource:manage"), dsH.Disable)
		ds.POST("/:id/enable", az.RequirePerm("datasource:manage"), dsH.Enable)
		ds.DELETE("/:id", az.RequirePerm("datasource:manage"), dsH.Delete)
	}

	cat := v1.Group("/data-source-categories")
	{
		cat.GET("", az.RequirePerm("datasource:view"), dsH.ListCategories)
		cat.POST("", az.RequirePerm("datasource:manage"), dsH.CreateCategory)
		cat.PUT("/:id", az.RequirePerm("datasource:manage"), dsH.UpdateCategory)
		cat.DELETE("/:id", az.RequirePerm("datasource:manage"), dsH.DeleteCategory)
	}

	mt := v1.Group("/metrics")
	{
		mt.GET("", az.RequirePerm("metric:view"), metH.List)
		mt.POST("", az.RequirePerm("metric:manage"), metH.Create)
		mt.GET("/:code", az.RequirePerm("metric:view"), metH.Get)
		mt.PUT("/:code", az.RequirePerm("metric:manage"), metH.Update)
		mt.POST("/:code/disable", az.RequirePerm("metric:manage"), metH.Disable)
		mt.POST("/:code/enable", az.RequirePerm("metric:manage"), metH.Enable)
		mt.DELETE("/:code", az.RequirePerm("metric:manage"), metH.Delete)
		mt.PUT("/:code/threshold", az.RequirePerm("metric:manage"), metH.UpdateThreshold)
		mt.GET("/:code/threshold-history", az.RequirePerm("metric:view"), metH.ThresholdHistory)
		mt.GET("/:code/trend", az.RequirePerm("data:view"), dataH.Trend)
		mt.POST("/:code/peaks/recalc", az.RequirePerm("data:correct"), dataH.RecalcPeaks)
		mt.GET("/:code/forecast", az.RequirePerm("metric:view"), fcH.MetricForecast)
		mt.GET("/tree", az.RequirePerm("metric:view"), metH.Tree)
		mt.GET("/:code/modules", az.RequirePerm("metric:view"), metH.Modules)
	}

	// 统计预测与风险清单（FR-PRE-01~04）: 数据权限在 handler 内按可见系统过滤
	fc := v1.Group("/forecasts")
	{
		fc.GET("/risk", az.RequirePerm("metric:view"), fcH.Risk)
		fc.GET("/runs", az.RequirePerm("metric:view"), fcH.Runs)
		fc.GET("/runs/:id", az.RequirePerm("metric:view"), fcH.RunDetail)
		fc.POST("/recompute", az.RequirePerm("admin:health:view"), fcH.Recompute)
	}

	// 事件因子（FR-AI-06）: 录入/确认/过期/版本追踪与 AI 建议
	fe := v1.Group("/forecast-events")
	{
		fe.GET("", az.RequirePerm("metric:view"), fcH.ListEvents)
		fe.GET("/:id", az.RequirePerm("metric:view"), fcH.GetEvent)
		fe.POST("", az.RequirePerm("forecast:event:manage"), fcH.CreateEvent)
		fe.PUT("/:id", az.RequirePerm("forecast:event:manage"), fcH.UpdateEvent)
		fe.POST("/:id/confirm", az.RequirePerm("forecast:event:manage"), fcH.ConfirmEvent)
		fe.POST("/:id/expire", az.RequirePerm("forecast:event:manage"), fcH.ExpireEvent)
		fe.POST("/:id/ai-suggest", az.RequirePerm("forecast:event:manage"), fcH.SuggestEvent)
	}

	// 开放 API 管理（FR-DAY-07）：API Key、调用审计、保留期与归档（管理员）
	oa := v1.Group("/open-api")
	{
		oa.GET("/keys", az.RequirePerm("admin:health:view"), oaH.ListKeys)
		oa.POST("/keys", az.RequirePerm("admin:health:view"), oaH.CreateKey)
		oa.PUT("/keys/:id", az.RequirePerm("admin:health:view"), oaH.UpdateKey)
		oa.POST("/keys/:id/rotate", az.RequirePerm("admin:health:view"), oaH.RotateKey)
		oa.POST("/keys/:id/disable", az.RequirePerm("admin:health:view"), oaH.DisableKey)
		oa.POST("/keys/:id/enable", az.RequirePerm("admin:health:view"), oaH.EnableKey)
		oa.DELETE("/keys/:id", az.RequirePerm("admin:health:view"), oaH.DeleteKey)
		oa.GET("/logs", az.RequirePerm("admin:log:view"), oaH.KeyLogs)
		oa.GET("/retention", az.RequirePerm("admin:health:view"), oaH.RetentionView)
		oa.PUT("/retention", az.RequirePerm("admin:param:manage"), oaH.UpdateRetention)
		oa.GET("/archives", az.RequirePerm("admin:health:view"), oaH.ListArchives)
		oa.POST("/archives/run", az.RequirePerm("admin:param:manage"), oaH.RunArchive)
		oa.POST("/archives/:id/restore", az.RequirePerm("admin:param:manage"), oaH.RestoreArchive)
		oa.GET("/archives/query", az.RequirePerm("admin:health:view"), oaH.QueryArchivedData)
	}

	// 二期扩展项：衍生指标、阈值模板、批量导入、日历完整性
	oa.GET("/derived-metrics", az.RequirePerm("metric:view"), oaH.ListDerivedMetrics)
	oa.POST("/derived-metrics", az.RequirePerm("metric:manage"), oaH.CreateDerivedMetric)
	oa.POST("/derived-metrics/:id/evaluate", az.RequirePerm("metric:view"), oaH.EvaluateDerived)
	oa.POST("/derived-metrics/:id/disable", az.RequirePerm("metric:manage"), oaH.DisableDerivedMetric)
	oa.POST("/derived-metrics/:id/enable", az.RequirePerm("metric:manage"), oaH.EnableDerivedMetric)
	oa.DELETE("/derived-metrics/:id", az.RequirePerm("metric:manage"), oaH.DeleteDerivedMetric)
	oa.GET("/threshold-templates", az.RequirePerm("metric:view"), oaH.ListThresholdTemplates)
	oa.POST("/threshold-templates", az.RequirePerm("metric:manage"), oaH.CreateThresholdTemplate)
	oa.POST("/threshold-templates/:id/apply", az.RequirePerm("metric:manage"), oaH.ApplyThresholdTemplate)
	oa.POST("/threshold-templates/:id/disable", az.RequirePerm("metric:manage"), oaH.DisableThresholdTemplate)
	oa.POST("/threshold-templates/:id/enable", az.RequirePerm("metric:manage"), oaH.EnableThresholdTemplate)
	oa.DELETE("/threshold-templates/:id", az.RequirePerm("metric:manage"), oaH.DeleteThresholdTemplate)
	oa.POST("/metrics/import", az.RequirePerm("metric:manage"), oaH.ImportMetrics)
	oa.GET("/metrics/imports", az.RequirePerm("metric:view"), oaH.ListImportRuns)
	oa.POST("/calendar/check", az.RequirePerm("tradingday:manage"), oaH.RunCalendarCheck)

	fl := v1.Group("/flows")
	{
		fl.GET("", az.RequirePerm("flow:view"), flowH.List)
		fl.POST("", az.RequirePerm("flow:manage"), flowH.Create)
		fl.GET("/:id", az.RequirePerm("flow:view"), flowH.Get)
		fl.PUT("/:id", az.RequirePerm("flow:manage"), flowH.Update)
		fl.POST("/:id/preview", az.RequirePerm("flow:manage"), flowH.Preview)
		fl.POST("/:id/activate", az.RequirePerm("flow:manage"), flowH.Activate)
		fl.POST("/:id/disable", az.RequirePerm("flow:manage"), flowH.Disable)
		fl.POST("/:id/archive", az.RequirePerm("flow:manage"), flowH.Archive)
		fl.POST("/:id/trigger", az.RequirePerm("task:manage"), flowH.Trigger)
		fl.DELETE("/:id", az.RequirePerm("flow:manage"), flowH.Delete)
	}

	sc := v1.Group("/scripts")
	{
		sc.GET("", az.RequirePerm("script:view"), flowH.ListScripts)
		sc.POST("", az.RequirePerm("script:manage"), flowH.CreateScript)
		sc.GET("/:id/versions", az.RequirePerm("script:view"), flowH.ListVersions)
		sc.POST("/:id/versions", az.RequirePerm("script:manage"), flowH.CreateVersion)
		sc.POST("/:id/versions/:v/activate", az.RequirePerm("script:manage"), flowH.ActivateVersion)
		sc.DELETE("/:id", az.RequirePerm("script:manage"), flowH.DeleteScript)
	}

	tk := v1.Group("/tasks")
	{
		tk.GET("", az.RequirePerm("task:view"), taskH.List)
		tk.GET("/:id", az.RequirePerm("task:view"), taskH.Get)
		tk.GET("/:id/cleaning-traces", az.RequirePerm("task:view"), taskH.CleaningTraces)
	}

	da := v1.Group("/data")
	{
		da.GET("/rt", az.RequirePerm("data:view"), dataH.QueryRT)
		da.GET("/day", az.RequirePerm("data:view"), dataH.QueryDay)
		da.POST("/corrections", az.RequirePerm("data:correct"), dataH.Correct)
		da.GET("/corrections", az.RequirePerm("data:view"), dataH.ListCorrections)
		da.POST("/summary/run", az.RequirePerm("admin:health:view"), dataH.RunSummary)
		da.GET("/quality", az.RequirePerm("data:view"), dataH.ListQuality)
		da.POST("/quality/evaluate", az.RequirePerm("data:correct"), dataH.EvaluateQuality)
		da.GET("/backfill", az.RequirePerm("data:view"), dataH.ListBackfills)
		da.GET("/backfill/:id", az.RequirePerm("data:view"), dataH.GetBackfill)
		da.POST("/backfill", az.RequirePerm("task:manage"), dataH.RunBackfill)
	}

	al := v1.Group("/alerts")
	{
		al.GET("", az.RequirePerm("alert:view"), alertH.List)
		al.GET("/stats", az.RequirePerm("alert:view"), alertH.Stats)
		al.GET("/export", az.RequirePerm("alert:export"), alertH.Export)
		al.GET("/dashboard", az.RequirePerm("alert:view"), alertH.Dashboard)
		al.GET("/dashboard-preference", az.RequirePerm("alert:view"), alertH.DashboardPreference)
		al.PUT("/dashboard-preference", az.RequirePerm("alert:view"), alertH.SaveDashboardPreference)
		al.GET("/dashboard-options", az.RequirePerm("alert:view"), alertH.DashboardOptions)
		al.GET("/:id", az.RequirePerm("alert:view"), alertH.Get)
		al.POST("/:id/claim", az.RequirePerm("alert:handle"), alertH.Claim)
		al.POST("/:id/note", az.RequirePerm("alert:handle"), alertH.Note)
		al.POST("/:id/close", az.RequirePerm("alert:handle"), alertH.Close)
	}
	pol := v1.Group("/alert-escalation-policies")
	{
		pol.GET("", az.RequirePerm("alert:view"), alertH.ListPolicies)
		pol.POST("", az.RequirePerm("alert:policy:manage"), alertH.CreatePolicy)
		pol.POST("/simulate", az.RequirePerm("alert:view"), alertH.SimulatePolicy)
		pol.GET("/:id/preview", az.RequirePerm("alert:view"), alertH.PreviewPolicy)
		pol.GET("/:id", az.RequirePerm("alert:view"), alertH.GetPolicy)
		pol.PUT("/:id", az.RequirePerm("alert:policy:manage"), alertH.UpdatePolicy)
		pol.DELETE("/:id", az.RequirePerm("alert:policy:manage"), alertH.DeletePolicy)
	}
	nt := v1.Group("/notification-channels")
	{
		nt.GET("", az.RequirePerm("notification:view"), alertH.ListNotificationChannels)
		nt.POST("", az.RequirePerm("notification:manage"), alertH.CreateNotificationChannel)
		nt.PUT("/:id", az.RequirePerm("notification:manage"), alertH.UpdateNotificationChannel)
		nt.DELETE("/:id", az.RequirePerm("notification:manage"), alertH.DeleteNotificationChannel)
		nt.POST("/:id/test", az.RequirePerm("notification:manage"), alertH.TestNotificationChannel)
	}
	ntpl := v1.Group("/notification-templates")
	{
		ntpl.GET("", az.RequirePerm("notification:view"), alertH.ListNotificationTemplates)
		ntpl.POST("", az.RequirePerm("notification:manage"), alertH.CreateNotificationTemplate)
		ntpl.PUT("/:id", az.RequirePerm("notification:manage"), alertH.UpdateNotificationTemplate)
		ntpl.DELETE("/:id", az.RequirePerm("notification:manage"), alertH.DeleteNotificationTemplate)
	}
	nsub := v1.Group("/notification-subscriptions")
	{
		nsub.GET("", az.RequirePerm("notification:view"), alertH.ListNotificationSubscriptions)
		nsub.POST("", az.RequirePerm("notification:manage"), alertH.CreateNotificationSubscription)
		nsub.PUT("/:id", az.RequirePerm("notification:manage"), alertH.UpdateNotificationSubscription)
		nsub.DELETE("/:id", az.RequirePerm("notification:manage"), alertH.DeleteNotificationSubscription)
	}
	nsil := v1.Group("/notification-silences")
	{
		nsil.GET("", az.RequirePerm("notification:view"), alertH.ListNotificationSilences)
		nsil.POST("", az.RequirePerm("notification:manage"), alertH.CreateNotificationSilence)
		nsil.DELETE("/:id", az.RequirePerm("notification:manage"), alertH.DeleteNotificationSilence)
	}
	nlog := v1.Group("/notification-logs")
	{
		nlog.GET("", az.RequirePerm("notification:view"), alertH.ListNotificationLogs)
		nlog.POST("/:id/retry", az.RequirePerm("notification:manage"), alertH.RetryNotificationLog)
	}

	v1.GET("/dashboard/overview", az.RequirePerm("dashboard:view"), dashH.Overview)
	v1.GET("/dashboard/trend", az.RequirePerm("dashboard:view"), dashH.Trend)
	v1.GET("/systems/:code/overview", az.RequirePerm("system:view"), requireScope(az, db), dashH.SystemOverview)

	rp := v1.Group("/reports")
	{
		rp.GET("/daily", az.RequirePerm("report:view"), rptH.Daily)
		rp.GET("/files", az.RequirePerm("report:view"), rptH.Files)
		rp.POST("/daily/:date/generate", az.RequirePerm("report:export"), rptH.Generate)
		rp.GET("/daily/:date/file", az.RequirePerm("report:export"), rptH.Download)
		rp.GET("/daily/:date/export", az.RequirePerm("report:export"), rptH.Export)
		// 报表中心（FR-RPT-01~14）: 模板 / 运行 / 下载 / 订阅
		rp.GET("/definitions", az.RequirePerm("report:view"), rptH.ListDefinitions)
		rp.POST("/definitions", az.RequirePerm("report:export"), rptH.CreateDefinition)
		rp.PUT("/definitions/:id", az.RequirePerm("report:export"), rptH.UpdateDefinition)
		rp.DELETE("/definitions/:id", az.RequirePerm("report:export"), rptH.DeleteDefinition)
		rp.GET("/definitions/:id/preview", az.RequirePerm("report:view"), rptH.PreviewCenter)
		rp.POST("/definitions/:id/run", az.RequirePerm("report:export"), rptH.RunCenter)
		rp.GET("/runs", az.RequirePerm("report:view"), rptH.ListRuns)
		rp.GET("/runs/:id", az.RequirePerm("report:view"), rptH.GetRun)
		rp.GET("/runs/:id/download", az.RequirePerm("report:export"), rptH.DownloadCenter)
		rp.GET("/subscriptions", az.RequirePerm("report:view"), rptH.ListSubscriptions)
		rp.POST("/subscriptions", az.RequirePerm("report:export"), rptH.CreateSubscription)
		rp.PUT("/subscriptions/:id", az.RequirePerm("report:export"), rptH.UpdateSubscription)
		rp.DELETE("/subscriptions/:id", az.RequirePerm("report:export"), rptH.DeleteSubscription)
		rp.POST("/subscriptions/:id/run", az.RequirePerm("report:export"), rptH.RunSubscription)
	}

	aiGrp := v1.Group("/ai")
	{
		aiGrp.POST("/chat", az.RequirePerm("ai:use"), aiH.Chat)
		aiGrp.POST("/flow-generate", az.RequirePerm("ai:use"), aiH.FlowGenerate)
		aiGrp.POST("/clean-generate", az.RequirePerm("ai:use"), aiH.CleanGenerate)
		aiGrp.POST("/clean-tryrun", az.RequirePerm("ai:use"), aiH.CleanTryRun)
		aiGrp.GET("/logs", az.RequirePerm("admin:log:view"), aiH.Logs)
	}
}

// requireScope 数据权限: 路径 :code 系统须在用户数据权限内（platform_admin 放行）
func requireScope(az *middleware.AuthZ, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		username, _ := c.Get(middleware.CtxUsername)
		uname, _ := username.(string)
		az.EnsureFresh()
		if az.IsPlatformAdmin(uname) {
			c.Next()
			return
		}
		if !hasDataScope(c.Request.Context(), db, uname, code) {
			c.AbortWithStatusJSON(403, resp.Body{Code: resp.CodeDataForbidden, Message: "无该业务系统数据权限: " + code})
			return
		}
		c.Next()
	}
}

// hasDataScope 查询用户(含角色)对系统的 manage/view 授权
func hasDataScope(ctx context.Context, db *gorm.DB, username, systemCode string) bool {
	var n int64
	err := db.Table("sys_data_scope s").
		Joins("JOIN sys_user u ON u.id = s.user_id").
		Joins("LEFT JOIN sys_user_role ur ON ur.user_id = u.id").
		Joins("LEFT JOIN sys_role r ON r.id = ur.role_id").
		Where("u.username = ? AND s.system_code = ?", username, systemCode).
		Where("s.user_id IS NOT NULL OR r.status = 'active'").
		Count(&n).Error
	return err == nil && n > 0
}

// dashboard 监控大盘（总览 + 系统视图）
type dashboard struct {
	db *gorm.DB
	az *middleware.AuthZ
}

func dashboardHandler(db *gorm.DB, az *middleware.AuthZ) *dashboard {
	return &dashboard{db: db, az: az}
}

// Overview 平台总览
func (d *dashboard) Overview(c *gin.Context) {
	var sysTotal, metTotal, activeMet int64
	var overWarn, overDanger int64
	var alertNew, alertActive, collectFail int64

	// 数据权限: 非平台管理员仅统计可见系统（FR-ADM-03）
	sys := scope.Systems(c.Request.Context(), d.db, d.az, scope.Username(c))
	var dsq *gorm.DB
	dsq = scope.Apply(d.db.Model(&persistence.BusinessSystem{}).Where("status <> 'offline'"), sys)
	dsq.Count(&sysTotal)
	dsq = scope.Apply(d.db.Model(&persistence.CapacityMetric{}), sys)
	dsq.Count(&metTotal)
	dsq = scope.Apply(d.db.Model(&persistence.CapacityMetric{}).Where("status = 'active'"), sys)
	dsq.Count(&activeMet)

	today := clock.DayStart(time.Now())
	yesterday := today.AddDate(0, 0, -1)
	dsq = scope.Apply(d.db.Model(&persistence.MetricValueDay{}).
		Joins("JOIN capacity_metric m ON m.metric_code = metric_value_day.metric_code").
		Where("biz_date = ? AND level = 'warn'", yesterday), sys)
	dsq.Count(&overWarn)
	dsq = scope.Apply(d.db.Model(&persistence.MetricValueDay{}).
		Joins("JOIN capacity_metric m ON m.metric_code = metric_value_day.metric_code").
		Where("biz_date = ? AND level = 'danger'", yesterday), sys)
	dsq.Count(&overDanger)
	dsq = scope.Apply(d.db.Model(&persistence.AlertRecord{}).Where("trigger_at >= ?", today), sys)
	dsq.Count(&alertNew)
	dsq = scope.Apply(d.db.Model(&persistence.AlertRecord{}).Where("status IN ('active','processing')"), sys)
	dsq.Count(&alertActive)
	dsq = scope.Apply(d.db.Model(&persistence.CollectTaskLog{}).
		Joins("JOIN capacity_metric m ON m.metric_code = collect_task_log.metric_code").
		Where("collect_task_log.triggered_at >= ? AND collect_task_log.status IN ('failed','timeout')", today), sys)
	dsq.Count(&collectFail)

	type topRow struct {
		MetricCode string   `json:"metricCode" gorm:"column:metric_code"`
		Name       string   `json:"name" gorm:"column:name"`
		SystemCode string   `json:"systemCode" gorm:"column:system_code"`
		SystemName string   `json:"systemName" gorm:"column:system_name"`
		StatValue  *float64 `json:"statValue" gorm:"column:stat_value"`
		WaterLevel *float64 `json:"waterLevel" gorm:"column:water_level"`
		Level      string   `json:"level" gorm:"column:level"`
	}
	var top []topRow
	topSQL := `
		SELECT m.metric_code, m.name, m.system_code, s.name AS system_name,
		       d.stat_value, d.water_level, d.level
		FROM metric_value_day d
		JOIN capacity_metric m ON m.metric_code = d.metric_code
		JOIN business_system s ON s.system_code = m.system_code
		WHERE d.biz_date = ? AND d.water_level IS NOT NULL`
	topArgs := []any{yesterday}
	if sys != nil {
		topSQL += " AND m.system_code IN ?"
		topArgs = append(topArgs, sys)
	}
	topSQL += " ORDER BY d.water_level DESC NULLS LAST\n\t\tLIMIT 10"
	_ = d.db.Raw(topSQL, topArgs...).Scan(&top).Error

	resp.OK(c, gin.H{
		"systemCount": sysTotal, "metricCount": metTotal, "activeMetricCount": activeMet,
		"overWarnCount": overWarn, "overDangerCount": overDanger,
		"alertNewToday": alertNew, "alertActive": alertActive, "collectFailToday": collectFail,
		"topWater": top, "bizDate": yesterday.Format("2006-01-02"),
	})
}

// SystemOverview 系统视图: 该系统全部指标水位(汇总指标日值由日汇总按主统计模块生成, 分组节点无直存数据)
func (d *dashboard) SystemOverview(c *gin.Context) {
	code := c.Param("code")
	type row struct {
		MetricCode string   `json:"metricCode" gorm:"column:metric_code"`
		Name       string   `json:"name" gorm:"column:name"`
		Unit       string   `json:"unit" gorm:"column:unit"`
		MetricKind string   `json:"metricKind" gorm:"column:metric_kind"`
		StatValue  *float64 `json:"statValue" gorm:"column:stat_value"`
		WaterLevel *float64 `json:"waterLevel" gorm:"column:water_level"`
		Level      string   `json:"level" gorm:"column:level"`
	}
	yesterday := clock.DayStart(time.Now().AddDate(0, 0, -1))
	var items []row
	err := d.db.Raw(`
		SELECT m.metric_code, m.name, m.unit, m.metric_kind, d.stat_value, d.water_level,
		       COALESCE(d.level, 'normal') AS level
		FROM capacity_metric m
		LEFT JOIN metric_value_day d ON d.metric_code = m.metric_code AND d.biz_date = ?
		WHERE m.system_code = ? AND m.status = 'active'
		ORDER BY d.water_level DESC NULLS LAST`, yesterday, code).Scan(&items).Error
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"systemCode": code, "metrics": items, "count": len(items)})
}

// Trend platform water-level trend (dashboard 30-day line): daily avg/max water level
func (d *dashboard) Trend(c *gin.Context) {
	days := 30
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 7 && n <= 180 {
			days = n
		}
	}
	from := clock.DayStart(time.Now()).AddDate(0, 0, -(days - 1))
	type row struct {
		BizDate time.Time `gorm:"column:biz_date"`
		Avg     *float64  `gorm:"column:avg_wl"`
		Max     *float64  `gorm:"column:max_wl"`
	}
	var rows []row
	trendSQL := `
		SELECT d.biz_date, AVG(d.water_level) AS avg_wl, MAX(d.water_level) AS max_wl
		FROM metric_value_day d
		JOIN capacity_metric m ON m.metric_code = d.metric_code AND m.status = 'active'
		WHERE d.biz_date >= ? AND d.water_level IS NOT NULL`
	trendArgs := []any{from}
	if sys := scope.Systems(c.Request.Context(), d.db, d.az, scope.Username(c)); sys != nil {
		trendSQL += " AND m.system_code IN ?"
		trendArgs = append(trendArgs, sys)
	}
	trendSQL += " GROUP BY d.biz_date ORDER BY d.biz_date"
	_ = d.db.Raw(trendSQL, trendArgs...).Scan(&rows).Error
	byDate := make(map[string]row, len(rows))
	for _, r := range rows {
		byDate[r.BizDate.Format("2006-01-02")] = r
	}
	dates := make([]string, 0, days)
	avg := make([]any, 0, days)
	max := make([]any, 0, days)
	for i := 0; i < days; i++ {
		dte := from.AddDate(0, 0, i)
		key := dte.Format("2006-01-02")
		dates = append(dates, key)
		if r, ok := byDate[key]; ok {
			avg = append(avg, r.Avg)
			max = append(max, r.Max)
		} else {
			avg = append(avg, nil)
			max = append(max, nil)
		}
	}
	resp.OK(c, gin.H{"days": days, "dates": dates, "avg": avg, "max": max})
}
