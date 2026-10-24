package openapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// 开放 API 的 scope 语义（计划 §7 Task 10 Step 1/2）。
const (
	ScopeDataRead     = "data:read"
	ScopeForecastRead = "forecast:read"
	ScopeAlertWebhook = "alert:webhook"
)

var validScopes = map[string]bool{
	ScopeDataRead: true, ScopeForecastRead: true, ScopeAlertWebhook: true,
}

type Handler struct {
	db      *gorm.DB
	rdb     *redis.Client
	az      *middleware.AuthZ
	log     *slog.Logger
	archive *ArchiveService
}

func NewHandler(db *gorm.DB, rdb *redis.Client, az *middleware.AuthZ, log *slog.Logger) *Handler {
	return &Handler{db: db, rdb: rdb, az: az, log: log}
}

// ---------- API Key 管理 ----------

type apiKeyInput struct {
	Name              string     `json:"name"`
	Scopes            []string   `json:"scopes"`
	SystemScope       []string   `json:"systemScope"`
	RateLimit         int        `json:"rateLimit"`
	SignatureRequired bool       `json:"signatureRequired"`
	Remark            string     `json:"remark"`
	ExpiresAt         *time.Time `json:"expiresAt"`
}

func (in apiKeyInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("名称不能为空")
	}
	if len(in.Scopes) == 0 {
		return fmt.Errorf("至少需要一个 scope")
	}
	for _, s := range in.Scopes {
		if !validScopes[s] {
			return fmt.Errorf("不支持的 scope: %s", s)
		}
	}
	if in.RateLimit < 0 || in.RateLimit > 100000 {
		return fmt.Errorf("限流值超出 [0, 100000]")
	}
	return nil
}

// ListKeys 返回 API Key 列表（不含 secret）。
func (h *Handler) ListKeys(c *gin.Context) {
	if page, size, ok := resp.ParsePage(c, 20, 200); ok {
		q := h.db.Model(&persistence.APIKey{})
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.APIKey
		if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, gin.H{"items": keyViews(rows), "count": len(rows), "total": total, "page": page, "pageSize": size})
		return
	}
	var rows []persistence.APIKey
	if err := h.db.Order("created_at DESC").Limit(500).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": keyViews(rows), "count": len(rows)})
}

// CreateKey 生成新 Key，明文 secret 只在本次响应返回一次。
func (h *Handler) CreateKey(c *gin.Context) {
	var in apiKeyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if err := in.validate(); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", err.Error()))
		return
	}
	keyID, secret, err := newKeyMaterial()
	if err != nil {
		resp.Fail(c, err)
		return
	}
	scopes, _ := json.Marshal(in.Scopes)
	systems, _ := json.Marshal(in.SystemScope)
	rate := in.RateLimit
	if rate == 0 {
		rate = 300
	}
	row := persistence.APIKey{
		Name: in.Name, KeyID: keyID, KeyHash: hashSecret(secret),
		Scopes: string(scopes), SystemScope: string(systems), RateLimit: rate,
		SignatureRequired: in.SignatureRequired,
		Status:            "active", Remark: in.Remark, CreatedBy: currentUser(c), ExpiresAt: in.ExpiresAt,
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "key_create", in.Name, "keyId="+keyID)
	view := keyView(row)
	view["secret"] = secret
	view["apiKey"] = keyID + "." + secret
	resp.OK(c, view)
}

// UpdateKey 调整 scope/限流/状态，不轮换 secret。
func (h *Handler) UpdateKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.APIKey
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "API Key 不存在"))
		return
	}
	var in apiKeyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if err := in.validate(); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", err.Error()))
		return
	}
	scopes, _ := json.Marshal(in.Scopes)
	systems, _ := json.Marshal(in.SystemScope)
	updates := map[string]any{
		"name": in.Name, "scopes": string(scopes), "system_scope": string(systems),
		"signature_required": in.SignatureRequired,
		"remark":             in.Remark,
		"updated_at":         time.Now(),
	}
	if in.RateLimit > 0 {
		updates["rate_limit"] = in.RateLimit
	}
	if in.ExpiresAt != nil {
		updates["expires_at"] = in.ExpiresAt
	}
	if err := h.db.Model(&row).Updates(updates).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "key_update", row.Name, fmt.Sprintf("id=%d", row.ID))
	resp.OK(c, keyView(row))
}

// RotateKey 轮换 secret，旧 secret 立即失效。
func (h *Handler) RotateKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.APIKey
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "API Key 不存在"))
		return
	}
	keyID, secret, err := newKeyMaterial()
	if err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"key_id": keyID, "key_hash": hashSecret(secret), "updated_at": time.Now(),
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	row.KeyID = keyID
	middleware.Log(c, "openapi", "key_rotate", row.Name, "keyId="+keyID)
	view := keyView(row)
	view["secret"] = secret
	view["apiKey"] = keyID + "." + secret
	resp.OK(c, view)
}

// DisableKey 停用 Key。
func (h *Handler) DisableKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.db.Model(&persistence.APIKey{}).Where("id = ?", id).
		Updates(map[string]any{"status": "disabled", "updated_at": time.Now()}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "key_disable", strconv.FormatInt(id, 10), "")
	resp.OK(c, gin.H{"id": id, "status": "disabled"})
}

// EnableKey 启用 Key。
func (h *Handler) EnableKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.db.Model(&persistence.APIKey{}).Where("id = ?", id).
		Updates(map[string]any{"status": "active", "updated_at": time.Now()}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "key_enable", strconv.FormatInt(id, 10), "")
	resp.OK(c, gin.H{"id": id, "status": "active"})
}

// DeleteKey 永久删除已停用的 Key。调用审计通过 key_id 外键置空保留。
func (h *Handler) DeleteKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.APIKey
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "API Key 不存在"))
		return
	}
	if row.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "请先停用 API Key 后再删除"))
		return
	}
	if err := h.db.Delete(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "key_delete", row.Name, "keyId="+row.KeyID)
	resp.OK(c, gin.H{"id": id, "deleted": true})
}

// KeyLogs 调用审计（最近 500 条）。
func (h *Handler) KeyLogs(c *gin.Context) {
	q := h.db.Model(&persistence.APICallLog{})
	if raw := strings.TrimSpace(c.Query("keyId")); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			q = q.Where("key_id = ?", id)
		}
	}
	if raw := strings.TrimSpace(c.Query("allowed")); raw != "" {
		q = q.Where("allowed = ?", raw == "true")
	}
	if page, size, ok := resp.ParsePage(c, 20, 200); ok {
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.APICallLog
		if err := q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, gin.H{"items": rows, "count": len(rows), "total": total, "page": page, "pageSize": size})
		return
	}
	var rows []persistence.APICallLog
	if err := q.Order("created_at DESC, id DESC").Limit(500).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows)})
}

// ---------- 开放数据接口 ----------

// OpenDayData 开放日数据查询，支持按指标与日期区间过滤，受 Key 的 system scope 限制。
func (h *Handler) OpenDayData(c *gin.Context) {
	key := keyFromContext(c)
	if key == nil {
		return
	}
	from, to, ok := parseRange(c, 90)
	if !ok {
		return
	}
	metricCode := strings.TrimSpace(c.Query("metricCode"))
	q := h.db.Model(&persistence.MetricValueDay{}).
		Where("biz_date >= ? AND biz_date <= ?", from, to)
	if metricCode != "" {
		q = q.Where("metric_code = ?", metricCode)
	}
	if allowed := keySystemScope(key); allowed != nil {
		codes, err := h.metricCodesForSystems(c.Request.Context(), allowed)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		if len(codes) == 0 {
			resp.OK(c, gin.H{"items": []any{}, "count": 0, "from": from, "to": to})
			return
		}
		q = q.Where("metric_code IN ?", codes)
	}
	var rows []persistence.MetricValueDay
	if err := q.Order("biz_date DESC, metric_code").Limit(20000).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows), "from": from, "to": to})
}

// OpenRTData 开放日内数据查询（默认最近 7 天在线数据）。
func (h *Handler) OpenRTData(c *gin.Context) {
	key := keyFromContext(c)
	if key == nil {
		return
	}
	hours := 24
	if raw := strings.TrimSpace(c.Query("hours")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 24*90 {
			hours = n
		}
	}
	metricCode := strings.TrimSpace(c.Query("metricCode"))
	from := time.Now().Add(-time.Duration(hours) * time.Hour)
	type rtRow struct {
		MetricCode string    `json:"metricCode"`
		Ts         time.Time `json:"ts"`
		Value      *float64  `json:"value"`
		Level      string    `json:"level"`
	}
	var rows []rtRow
	// 清洗值优先, 缺省回退原始值; 级别来自 judge_level。
	q := h.db.Table("metric_value_rt").
		Select("metric_code, ts, COALESCE(clean_value, raw_value) AS value, judge_level AS level").
		Where("ts >= ?", from)
	if metricCode != "" {
		q = q.Where("metric_code = ?", metricCode)
	}
	if allowed := keySystemScope(key); allowed != nil {
		codes, err := h.metricCodesForSystems(c.Request.Context(), allowed)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		if len(codes) == 0 {
			resp.OK(c, gin.H{"items": []any{}, "count": 0})
			return
		}
		q = q.Where("metric_code IN ?", codes)
	}
	if err := q.Order("ts DESC").Limit(20000).Scan(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows), "hours": hours})
}

// OpenForecast 开放预测结果查询（最新成功批次）。
func (h *Handler) OpenForecast(c *gin.Context) {
	key := keyFromContext(c)
	if key == nil {
		return
	}
	horizon := 30
	if raw := strings.TrimSpace(c.Query("horizon")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && (n == 30 || n == 90) {
			horizon = n
		}
	}
	metricCode := strings.TrimSpace(c.Query("metricCode"))
	var run persistence.ForecastRun
	if err := h.db.Where("status = ?", "success").Order("run_date DESC, id DESC").First(&run).Error; err != nil {
		resp.OK(c, gin.H{"run": nil, "items": []any{}, "count": 0})
		return
	}
	q := h.db.Model(&persistence.ForecastResult{}).
		Where("run_id = ? AND horizon_days = ?", run.ID, horizon)
	if metricCode != "" {
		q = q.Where("metric_code = ?", metricCode)
	}
	if allowed := keySystemScope(key); allowed != nil {
		codes, err := h.metricCodesForSystems(c.Request.Context(), allowed)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		if len(codes) == 0 {
			resp.OK(c, gin.H{"run": run, "items": []any{}, "count": 0})
			return
		}
		q = q.Where("metric_code IN ?", codes)
	}
	var rows []persistence.ForecastResult
	if err := q.Order("metric_code").Limit(20000).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, gin.H{
			"metricCode": row.MetricCode, "horizonDays": row.HorizonDays,
			"modelType": row.ModelType, "status": row.Status,
			"etaWarnDate": row.ETAWarnDate, "etaDangerDate": row.ETADangerDate,
			"riskScore": row.RiskScore, "confidence": row.Confidence,
			"dataQuality": row.DataQuality, "explanation": row.Explanation,
			"eventFactorVersion": row.EventFactorVersion,
		})
	}
	resp.OK(c, gin.H{"run": run, "items": items, "count": len(items), "horizon": horizon})
}

// keySystemScope 解析 Key 的 system scope，空数组返回 nil 表示不限。
func keySystemScope(key *persistence.APIKey) []string {
	if key == nil || strings.TrimSpace(key.SystemScope) == "" {
		return nil
	}
	var systems []string
	if err := json.Unmarshal([]byte(key.SystemScope), &systems); err != nil {
		return nil
	}
	if len(systems) == 0 {
		return nil
	}
	return systems
}

func (h *Handler) metricCodesForSystems(ctx context.Context, systems []string) ([]string, error) {
	var codes []string
	err := h.db.WithContext(ctx).Model(&persistence.CapacityMetric{}).
		Where("system_code IN ?", systems).Pluck("metric_code", &codes).Error
	return codes, err
}

func parseRange(c *gin.Context, defaultDays int) (time.Time, time.Time, bool) {
	to := clock.DayStart(time.Now())
	from := to.AddDate(0, 0, -defaultDays)
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "to 格式应为 YYYY-MM-DD"))
			return time.Time{}, time.Time{}, false
		}
		to = parsed
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "from 格式应为 YYYY-MM-DD"))
			return time.Time{}, time.Time{}, false
		}
		from = parsed
	}
	if from.After(to) {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "from 不能晚于 to"))
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// ---------- 工具 ----------

func newKeyMaterial() (string, string, error) {
	idBytes := make([]byte, 8)
	secretBytes := make([]byte, 24)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", err
	}
	return "cap_" + hex.EncodeToString(idBytes), hex.EncodeToString(secretBytes), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func keyView(row persistence.APIKey) gin.H {
	return gin.H{
		"id": row.ID, "name": row.Name, "keyId": row.KeyID,
		"scopes": jsonList(row.Scopes), "systemScope": jsonList(row.SystemScope),
		"rateLimit": row.RateLimit, "status": row.Status, "remark": row.Remark,
		"signatureRequired": row.SignatureRequired,
		"createdBy":         row.CreatedBy, "lastUsedAt": row.LastUsedAt,
		"expiresAt": row.ExpiresAt, "callCount": row.CallCount,
		"createdAt": row.CreatedAt, "updatedAt": row.UpdatedAt,
	}
}

func keyViews(rows []persistence.APIKey) []gin.H {
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, keyView(row))
	}
	return out
}

func jsonList(raw string) []string {
	var out []string
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "ID 非法"))
		return 0, false
	}
	return id, true
}

func currentUser(c *gin.Context) string {
	if v, ok := c.Get(middleware.CtxUsername); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "system"
}

var _ = http.StatusOK
