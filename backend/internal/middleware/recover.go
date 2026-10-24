package middleware

import (
	"log"
	"net/http"
	"runtime/debug"

	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

// Recovery panic 恢复
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic: %v\n%s", r, debug.Stack())
				c.AbortWithStatusJSON(http.StatusInternalServerError, resp.Body{Code: resp.CodeInternal, Message: "服务内部错误"})
			}
		}()
		c.Next()
	}
}