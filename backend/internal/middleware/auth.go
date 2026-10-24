package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

type Claims struct {
	UserID   int64    `json:"uid"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

const (
	CtxUserID   = "ctxUserId"
	CtxUsername = "ctxUsername"
	CtxRoles    = "ctxRoles"
)

// Auth JWT 校验 + Redis 会话有效性（支持注销与 30 分钟滑动超时）
func Auth(rdb *redis.Client, secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, resp.Body{Code: resp.CodeUnauthorized, Message: "未认证"})
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, resp.Body{Code: resp.CodeUnauthorized, Message: "令牌无效或已过期"})
			return
		}
		jti := claims.ID
		if jti == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, resp.Body{Code: resp.CodeUnauthorized, Message: "令牌缺少会话标识"})
			return
		}
		ctx := c.Request.Context()
		ok, err := rdb.Exists(ctx, "sess:"+jti).Result()
		if err != nil || ok == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, resp.Body{Code: resp.CodeUnauthorized, Message: "会话已失效，请重新登录"})
			return
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxUsername, claims.Username)
		c.Set(CtxRoles, claims.Roles)
		c.Set("jwtClaims", claims) // 供 SlideSession 滑动刷新会话 TTL
		c.Next()
	}
}

// SlideSession 滑动刷新会话 TTL（在路由组中置于 Auth 之后）
func SlideSession(rdb *redis.Client, ttl time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("jwtClaims")
		if ok {
			if cl, ok := claims.(*Claims); ok {
				_ = rdb.Expire(c.Request.Context(), "sess:"+cl.ID, ttl)
			}
		}
		c.Next()
	}
}

// AttachClaims 供 Auth 内部挂载 claims（测试/扩展用）
func AttachClaims(c *gin.Context, cl *Claims) {
	c.Set("jwtClaims", cl)
}

// LoginLock 登录失败锁定检查（连续 N 次失败锁 M 分钟，参数由调用方传入）
func LoginLocked(ctx context.Context, rdb *redis.Client, username string) bool {
	n, err := rdb.Exists(ctx, "loginlock:"+username).Result()
	return err == nil && n > 0
}

func BumpLoginFail(ctx context.Context, rdb *redis.Client, username string, limit int, lock time.Duration) (count int, locked bool) {
	n, err := rdb.Incr(ctx, "loginfail:"+username).Result()
	if err != nil {
		return 0, false
	}
	_ = rdb.Expire(ctx, "loginfail:"+username, lock).Err()
	if int(n) >= limit {
		_ = rdb.Set(ctx, "loginlock:"+username, "1", lock).Err()
		_ = rdb.Del(ctx, "loginfail:"+username).Err()
		return int(n), true
	}
	return int(n), false
}

func ClearLoginFail(ctx context.Context, rdb *redis.Client, username string) {
	_ = rdb.Del(ctx, "loginfail:"+username, "loginlock:"+username).Err()
}

