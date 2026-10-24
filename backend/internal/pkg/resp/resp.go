package resp

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// 统一错误码（见 docs/10-design/03-API规范.md §1.2）
const (
	CodeOK             = 0
	CodeBadRequest     = 40001
	CodeUnauthorized   = 40100
	CodeLocked         = 40101
	CodeForbidden      = 40300
	CodeDataForbidden  = 40301
	CodeNotFound       = 40400
	CodeConflict       = 40900
	CodeUnprocessable  = 42200
	CodeTooMany        = 42900
	CodeInternal       = 50000
	CodeDepUnavailable = 50300
	CodeTimeout        = 50400
)

type Body struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	TraceID string `json:"traceId,omitempty"`
}

type PageData struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

func Page(items any, total int64, page, pageSize int) PageData {
	return PageData{Items: items, Total: total, Page: page, PageSize: pageSize}
}

// ParsePage 解析 page/pageSize。page 缺失或非正数时 ok=false，调用方走"不带分页"的兼容分支；
// pageSize 非法时回退 defaultSize，并限制不超过 maxSize，避免一次拉取过多数据。
func ParsePage(c *gin.Context, defaultSize, maxSize int) (page, size int, ok bool) {
	if defaultSize <= 0 {
		defaultSize = 20
	}
	if maxSize <= 0 {
		maxSize = defaultSize
	}
	n, err := strconv.Atoi(strings.TrimSpace(c.Query("page")))
	if err != nil || n <= 0 {
		return 0, 0, false
	}
	size = defaultSize
	if v := strings.TrimSpace(c.Query("pageSize")); v != "" {
		if m, cerr := strconv.Atoi(v); cerr == nil && m > 0 {
			size = m
		}
	}
	if size > maxSize {
		size = maxSize
	}
	return n, size, true
}

// AppError 业务错误（携带错误码）
type AppError struct {
	Code int
	Msg  string
}

func (e *AppError) Error() string { return e.Msg }

func NewErr(code int, format string, args ...any) *AppError {
	return &AppError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Code: CodeOK, Message: "ok", Data: data, TraceID: traceID(c)})
}

func Fail(c *gin.Context, err error) {
	code, msg := CodeInternal, "服务内部错误"
	var ae *AppError
	if errors.As(err, &ae) {
		code, msg = ae.Code, ae.Msg
	}
	c.JSON(codeToHTTP(code), Body{Code: code, Message: msg, TraceID: traceID(c)})
}

func codeToHTTP(code int) int {
	switch {
	case code == CodeOK:
		return http.StatusOK
	case code >= 40000 && code < 40100:
		return http.StatusBadRequest
	case code >= 40100 && code < 40200:
		return http.StatusUnauthorized
	case code >= 40300 && code < 40400:
		return http.StatusForbidden
	case code >= 40400 && code < 40500:
		return http.StatusNotFound
	case code >= 40900 && code < 41000:
		return http.StatusConflict
	case code == CodeUnprocessable:
		return http.StatusUnprocessableEntity
	case code >= 42900 && code < 43000:
		return http.StatusTooManyRequests
	case code >= 50000 && code < 50100:
		return http.StatusInternalServerError
	case code >= 50300 && code < 50400:
		return http.StatusServiceUnavailable
	case code >= 50400:
		return http.StatusGatewayTimeout
	default:
		return http.StatusOK
	}
}

const ctxTraceID = "traceId"

func traceID(c *gin.Context) string {
	if v, ok := c.Get(ctxTraceID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return uuid.NewString()
}

// WithTraceID 生成/透传 traceId（请求日志中间件使用）
func WithTraceID(c *gin.Context, id string) {
	c.Set(ctxTraceID, id)
	c.Header("X-Trace-Id", id)
}
