package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestLog 结构化请求日志 + traceId
func RequestLog(lg *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		trace := uuid.NewString()
		c.Header("X-Trace-Id", trace)
		start := time.Now()
		c.Next()
		lg.Info("http",
			slog.String("trace", trace),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Int64("costMs", time.Since(start).Milliseconds()),
			slog.String("ip", c.ClientIP()),
		)
	}
}

// AuditFunc 审计写入实现（app 层注入，避免循环依赖）
type AuditFunc func(c *gin.Context, module, action, target, detail string)

var auditFunc AuditFunc

// SetAudit 注入审计实现
func SetAudit(f AuditFunc) { auditFunc = f }

// Log 由 handler 在关键操作成功后调用（阈值/流程/修正/告警处理/权限/导出/AI）
func Log(c *gin.Context, module, action, target, detail string) {
	if auditFunc != nil {
		auditFunc(c, module, action, target, detail)
	}
}