package openapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"

	"github.com/gin-gonic/gin"
)

func TestKeyMaterialUniquenessAndHashing(t *testing.T) {
	id1, secret1, err := newKeyMaterial()
	if err != nil {
		t.Fatalf("生成本应成功: %v", err)
	}
	id2, _, err := newKeyMaterial()
	if err != nil {
		t.Fatalf("生成本应成功: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("keyId 应唯一")
	}
	if len(secret1) != 48 {
		t.Fatalf("secret 应为 48 位十六进制, 实际 %d", len(secret1))
	}
	if hashSecret(secret1) == secret1 {
		t.Fatalf("不应明文存储 secret")
	}
	if hashSecret(secret1) != hashSecret(secret1) {
		t.Fatalf("摘要应确定")
	}
}

func TestSplitAPIKeyValidatesFormat(t *testing.T) {
	keyID, secret, ok := splitAPIKey("cap_abc.def")
	if !ok || keyID != "cap_abc" || secret != "def" {
		t.Fatalf("合法 Key 解析失败: %s %s %v", keyID, secret, ok)
	}
	for _, raw := range []string{"", "nocap", "cap_abc.", ".secret", "cap_abc"} {
		if _, _, ok := splitAPIKey(raw); ok {
			t.Fatalf("%q 应被拒绝", raw)
		}
	}
}

func TestHasScopeHonoursAllowedSet(t *testing.T) {
	key := &persistence.APIKey{Scopes: `["data:read"]`}
	if !hasScope(key, ScopeDataRead) {
		t.Fatalf("应命中 data:read")
	}
	if hasScope(key, ScopeForecastRead) {
		t.Fatalf("不应命中未授予的 forecast:read")
	}
	empty := &persistence.APIKey{Scopes: `[]`}
	if hasScope(empty, ScopeDataRead) {
		t.Fatalf("空 scope 不应放行")
	}
}

func TestRequireScopeRejectsMissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	r.GET("/open", h.RequireScope(ScopeDataRead), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req := httptest.NewRequest(http.MethodGet, "/open", nil)
	rec := httptest.NewRecorder()
	// 无 DB/RDB：authenticate 在读取 key 前即因缺少头返回，不需要依赖真实数据库。
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺少 X-API-Key 应 401, 实际 %d", rec.Code)
	}
}

func TestParsePartitionRange(t *testing.T) {
	// 规范命名（ensure_partitions 生成）
	from, to, ok := parsePartitionRange("metric_value_day_y2026m08")
	if !ok {
		t.Fatalf("应能解析规范分区名")
	}
	if from.Format("2006-01-02") != "2026-08-01" || to.Format("2006-01-02") != "2026-09-01" {
		t.Fatalf("规范分区区间错误: %s ~ %s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	// 兼容历史 _pYYYYMM 写法
	from, to, ok = parsePartitionRange("metric_value_day_p202401")
	if !ok {
		t.Fatalf("应能解析分区名")
	}
	if from.Format("2006-01-02") != "2024-01-01" {
		t.Fatalf("起始日错误: %s", from.Format("2006-01-02"))
	}
	if to.Format("2006-01-02") != "2024-02-01" {
		t.Fatalf("结束日应为下月首日: %s", to.Format("2006-01-02"))
	}
	for _, bad := range []string{"metric_value_day", "metric_value_day_p2024", "other_p20240101"} {
		if _, _, ok := parsePartitionRange(bad); ok {
			t.Fatalf("%q 应解析失败", bad)
		}
	}
}

func TestAPIKeyInputValidation(t *testing.T) {
	valid := apiKeyInput{Name: "外部数据平台", Scopes: []string{ScopeDataRead}, RateLimit: 120}
	if err := valid.validate(); err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	cases := map[string]apiKeyInput{
		"空名称":      {Name: " ", Scopes: []string{ScopeDataRead}},
		"空 scope":  {Name: "A"},
		"非法 scope": {Name: "A", Scopes: []string{"data:write"}},
		"限流越界":     {Name: "A", Scopes: []string{ScopeDataRead}, RateLimit: 200000},
	}
	for name, in := range cases {
		if err := in.validate(); err == nil {
			t.Fatalf("%s 应校验失败", name)
		}
	}
}

func signedOpenAPIRequest(method, target, body, secret, timestamp, nonce string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	canonical := requestSignatureCanonical(req, timestamp, nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	req.Header.Set(HeaderSignatureTimestamp, timestamp)
	req.Header.Set(HeaderSignatureNonce, nonce)
	req.Header.Set(HeaderSignatureValue, hex.EncodeToString(mac.Sum(nil)))
	return req
}

func TestVerifyRequestSignatureAcceptsValidHMAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "test-secret"
	now := time.Unix(1761148800, 0)
	req := signedOpenAPIRequest(http.MethodPost, "/open/stream/day?metricCode=M8_FC", `{"x":1}`, secret,
		"1761148800", "nonce-1234567890")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	if err := verifyRequestSignature(c, secret, now); err != nil {
		t.Fatalf("合法签名不应失败: %+v", err)
	}
}

func TestVerifyRequestSignatureRejectsTamperingAndReplayWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "test-secret"
	now := time.Unix(1761148800, 0)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = signedOpenAPIRequest(http.MethodPost, "/open/stream/day", `{"x":1}`, secret,
		"1761148800", "nonce-1234567890")
	c.Request.Body = httptest.NewRequest(http.MethodPost, "/open/stream/day", strings.NewReader(`{"x":2}`)).Body
	if err := verifyRequestSignature(c, secret, now); err == nil {
		t.Fatal("body 被篡改后应拒绝")
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = signedOpenAPIRequest(http.MethodGet, "/open/stream/day", "", secret,
		"1761148400", "nonce-1234567890")
	if err := verifyRequestSignature(c, secret, now); err == nil {
		t.Fatal("超出时间窗的签名应拒绝")
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = signedOpenAPIRequest(http.MethodGet, "/open/stream/day", "", secret,
		"1761148800", "short")
	if err := verifyRequestSignature(c, secret, now); err == nil {
		t.Fatal("过短 nonce 应拒绝")
	}
}
