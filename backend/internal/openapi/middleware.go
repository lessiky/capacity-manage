package openapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ctxAPIKey = "apiKey"

const (
	HeaderSignatureTimestamp = "X-Cap-Timestamp"
	HeaderSignatureNonce     = "X-Cap-Nonce"
	HeaderSignatureValue     = "X-Cap-Signature"

	signatureMaxSkew     = 5 * time.Minute
	signatureNonceMinLen = 16
	signatureNonceMaxLen = 128
)

// RequireScope 校验 X-API-Key、Key 状态/有效期、scope、系统范围和限流，并在
// 调用结束后写入 api_call_log（含被拒绝的越权/超限请求，满足 FR-DAY-07 审计）。
func (h *Handler) RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		key, err := h.authenticate(c)
		if err != nil {
			h.recordCall(c, nil, scope, false, err.Reason, start)
			resp.Fail(c, err.Err)
			c.Abort()
			return
		}
		if !hasScope(key, scope) {
			h.recordCall(c, key, scope, false, "scope 不足", start)
			resp.Fail(c, resp.NewErr(resp.CodeForbidden, "API Key 缺少 scope: %s", scope))
			c.Abort()
			return
		}
		if !h.allowRate(c.Request.Context(), key) {
			h.recordCall(c, key, scope, false, "超过限流", start)
			resp.Fail(c, resp.NewErr(resp.CodeTooMany, "超过 API Key 限流阈值"))
			c.Abort()
			return
		}
		c.Set(ctxAPIKey, key)
		c.Next()
		if c.Writer.Status() >= 400 {
			h.recordCall(c, key, scope, false, "handler 返回错误", start)
			return
		}
		h.recordCall(c, key, scope, true, "", start)
	}
}

type authError struct {
	Reason string
	Err    *resp.AppError
}

func authFail(reason string, e *resp.AppError) *authError { return &authError{Reason: reason, Err: e} }

func (h *Handler) authenticate(c *gin.Context) (*persistence.APIKey, *authError) {
	raw := strings.TrimSpace(c.GetHeader("X-API-Key"))
	if raw == "" {
		return nil, authFail("缺少 X-API-Key", resp.NewErr(resp.CodeUnauthorized, "缺少 X-API-Key"))
	}
	keyID, secret, ok := splitAPIKey(raw)
	if !ok {
		return nil, authFail("X-API-Key 格式非法", resp.NewErr(resp.CodeUnauthorized, "X-API-Key 格式应为 {keyId}.{secret}"))
	}
	var key persistence.APIKey
	if err := h.db.Where("key_id = ?", keyID).First(&key).Error; err != nil {
		return nil, authFail("Key 不存在", resp.NewErr(resp.CodeUnauthorized, "API Key 无效"))
	}
	if key.KeyHash != hashSecret(secret) {
		return nil, authFail("secret 不匹配", resp.NewErr(resp.CodeUnauthorized, "API Key 无效"))
	}
	if key.Status != "active" {
		return nil, authFail("Key 已停用", resp.NewErr(resp.CodeForbidden, "API Key 已停用"))
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, authFail("Key 已过期", resp.NewErr(resp.CodeForbidden, "API Key 已过期"))
	}
	if key.SignatureRequired {
		if err := verifyRequestSignature(c, secret, time.Now()); err != nil {
			return nil, err
		}
		if h.rdb == nil {
			return nil, authFail("签名重放存储不可用",
				resp.NewErr(resp.CodeDepUnavailable, "API Key 签名校验服务暂不可用"))
		}
		nonce := strings.TrimSpace(c.GetHeader(HeaderSignatureNonce))
		ok, err := h.rdb.SetNX(c.Request.Context(), "apikey:nonce:"+key.KeyID+":"+nonce, "1", 10*time.Minute).Result()
		if err != nil {
			return nil, authFail("签名重放存储不可用",
				resp.NewErr(resp.CodeDepUnavailable, "API Key 签名校验服务暂不可用"))
		}
		if !ok {
			return nil, authFail("nonce 重放",
				resp.NewErr(resp.CodeUnauthorized, "签名 nonce 已使用"))
		}
	}
	return &key, nil
}

// verifyRequestSignature 只负责时间窗、nonce 形态与 HMAC 校验；nonce 的
// 一次性消费由 Redis SetNX 在 authenticate 中完成。
func verifyRequestSignature(c *gin.Context, secret string, now time.Time) *authError {
	timestamp := strings.TrimSpace(c.GetHeader(HeaderSignatureTimestamp))
	nonce := strings.TrimSpace(c.GetHeader(HeaderSignatureNonce))
	supplied := strings.ToLower(strings.TrimSpace(c.GetHeader(HeaderSignatureValue)))
	if timestamp == "" || nonce == "" || supplied == "" {
		return authFail("缺少签名头",
			resp.NewErr(resp.CodeUnauthorized, "签名请求缺少 %s/%s/%s",
				HeaderSignatureTimestamp, HeaderSignatureNonce, HeaderSignatureValue))
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return authFail("签名时间戳非法",
			resp.NewErr(resp.CodeUnauthorized, "%s 应为 Unix 秒", HeaderSignatureTimestamp))
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > signatureMaxSkew {
		return authFail("签名时间戳超窗",
			resp.NewErr(resp.CodeUnauthorized, "签名时间戳超出 %s 时间窗", signatureMaxSkew))
	}
	if len(nonce) < signatureNonceMinLen || len(nonce) > signatureNonceMaxLen {
		return authFail("签名 nonce 非法",
			resp.NewErr(resp.CodeUnauthorized, "nonce 长度应为 %d~%d", signatureNonceMinLen, signatureNonceMaxLen))
	}
	canonical := requestSignatureCanonical(c.Request, timestamp, nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(supplied)) {
		return authFail("签名不匹配", resp.NewErr(resp.CodeUnauthorized, "请求签名无效"))
	}
	return nil
}

// requestSignatureCanonical 的换行拼接顺序固定为：
// timestamp, nonce, METHOD, path, rawQuery, sha256(body)。
func requestSignatureCanonical(req *http.Request, timestamp, nonce string) string {
	bodyHash := sha256.Sum256(nil)
	if req != nil && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			bodyHash = sha256.Sum256(body)
		}
	}
	method, path, query := "", "/", ""
	if req != nil {
		method = req.Method
		if req.URL != nil {
			path = req.URL.EscapedPath()
			if path == "" {
				path = "/"
			}
			query = req.URL.RawQuery
		}
	}
	return strings.Join([]string{
		timestamp, nonce, method, path, query, hex.EncodeToString(bodyHash[:]),
	}, "\n")
}

func splitAPIKey(raw string) (string, string, bool) {
	idx := strings.LastIndex(raw, ".")
	if idx <= 0 || idx == len(raw)-1 {
		return "", "", false
	}
	return raw[:idx], raw[idx+1:], true
}

func hasScope(key *persistence.APIKey, scope string) bool {
	for _, s := range jsonList(key.Scopes) {
		if s == scope {
			return true
		}
	}
	return false
}

// allowRate 使用 Redis 固定窗口计数实现每 Key 每分钟限流；Redis 不可用时
// 选择放行（可用性优先），但调用日志仍会记录。
func (h *Handler) allowRate(ctx context.Context, key *persistence.APIKey) bool {
	if key.RateLimit <= 0 {
		return true
	}
	if h.rdb == nil {
		return true
	}
	bucket := time.Now().UTC().Format("200601021504")
	redisKey := "apikey:rate:" + key.KeyID + ":" + bucket
	count, err := h.rdb.Incr(ctx, redisKey).Result()
	if err != nil {
		h.log.Warn("api key rate limit redis unavailable", "keyId", key.KeyID, "err", err.Error())
		return true
	}
	if count == 1 {
		h.rdb.Expire(ctx, redisKey, 70*time.Second)
	}
	return count <= int64(key.RateLimit)
}

func (h *Handler) recordCall(c *gin.Context, key *persistence.APIKey, scope string, allowed bool, reason string, start time.Time) {
	if h.db == nil {
		return
	}
	latency := int(time.Since(start).Milliseconds())
	var keyID *int64
	keyName := ""
	if key != nil {
		keyID = &key.ID
		keyName = key.Name
	}
	rec := persistence.APICallLog{
		KeyID: keyID, KeyName: keyName, Method: c.Request.Method,
		Path: c.Request.URL.Path, Scope: scope, Status: c.Writer.Status(),
		Allowed: allowed, Reason: reason, IP: c.ClientIP(), LatencyMS: &latency,
	}
	if err := h.db.Create(&rec).Error; err != nil {
		h.log.Warn("api call log write failed", "path", rec.Path, "err", err.Error())
	}
	if key != nil {
		now := time.Now()
		_ = h.db.Model(&persistence.APIKey{}).Where("id = ?", key.ID).
			Updates(map[string]any{
				"last_used_at": now,
				"call_count":   gorm.Expr("call_count + 1"),
			}).Error
	}
}

func keyFromContext(c *gin.Context) *persistence.APIKey {
	if v, ok := c.Get(ctxAPIKey); ok {
		if key, ok := v.(*persistence.APIKey); ok {
			return key
		}
	}
	resp.Fail(c, resp.NewErr(resp.CodeUnauthorized, "缺少 API Key 上下文"))
	c.Abort()
	return nil
}
