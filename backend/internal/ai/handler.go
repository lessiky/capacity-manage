package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/masking"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pyclient"
	"capacity-manage/backend/internal/system"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	cfg    *config.Config
	db     *gorm.DB
	az     *middleware.AuthZ
	log    *slog.Logger
	llm    LLMClient
	params *system.Params
	// mu 保护 llm/llmKey 懒构建（AI 配置经系统管理热更新后自动重建客户端）
	mu     sync.Mutex
	llmKey string
	py     *pyclient.Client
}

func NewHandler(cfg *config.Config, db *gorm.DB, az *middleware.AuthZ, log *slog.Logger, py *pyclient.Client, params *system.Params) *Handler {
	return &Handler{cfg: cfg, db: db, az: az, log: log, py: py, params: params}
}

// aiEffective 生效中的 AI 配置: sys_param 热参数优先, 空值回退环境变量
type aiEffective struct {
	Enabled bool
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
}

func (h *Handler) effective() aiEffective {
	eff := aiEffective{
		Enabled: h.cfg.AIEnabled,
		BaseURL: strings.TrimRight(strings.TrimSpace(h.cfg.AIBaseURL), "/"),
		Model:   h.cfg.AIModel,
		APIKey:  h.cfg.AIKey,
		Timeout: h.cfg.AITimeout,
	}
	if v := h.params.Get("ai.enabled", ""); v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			eff.Enabled = b
		}
	}
	if v := strings.TrimSpace(h.params.Get("ai.base_url", "")); v != "" {
		eff.BaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(h.params.Get("ai.model", "")); v != "" {
		eff.Model = v
	}
	if v := h.params.Get("ai.api_key", ""); v != "" {
		eff.APIKey = v
	}
	if v := strings.TrimSpace(h.params.Get("ai.timeout_sec", "")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			eff.Timeout = time.Duration(n) * time.Second
		}
	}
	return eff
}

// client 按当前生效配置懒构建 LLM 客户端（配置变化时自动重建）; 未启用返回 nil
func (h *Handler) client() LLMClient {
	eff := h.effective()
	if !eff.Enabled || eff.BaseURL == "" || eff.Model == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key := eff.BaseURL + "|" + eff.Model + "|" + eff.APIKey + "|" + eff.Timeout.String()
	if h.llm != nil && h.llmKey == key {
		return h.llm
	}
	h.llm = NewOpenAICompat(&config.Config{AIBaseURL: eff.BaseURL, AIModel: eff.Model, AIKey: eff.APIKey, AITimeout: eff.Timeout})
	h.llmKey = key
	return h.llm
}

// ChatFunc 返回当前生效配置下的对话能力（供 forecast 等模块复用，避免重复
// 读取 sys_param 并保证配置热更新）。未启用或未配置时返回 nil。
func (h *Handler) ChatFunc() func(ctx context.Context, system, user string) (string, string, error) {
	client := h.client()
	if client == nil {
		return nil
	}
	return func(ctx context.Context, system, user string) (string, string, error) {
		// 每次调用重新取一次 client，配置热更新后立即生效。
		cur := h.client()
		if cur == nil {
			return "", "", fmt.Errorf("AI 未启用或未配置")
		}
		resp2, err := cur.Chat(ctx, ChatReq{
			Feature: "forecast_adjust",
			Messages: []Message{
				{Role: "system", Content: system},
				{Role: "user", Content: masking.SanitizeForAI(user)},
			},
			MaxTokens:   600,
			Temperature: 0,
		})
		if err != nil {
			return "", resp2.Model, err
		}
		return resp2.Content, resp2.Model, nil
	}
}

// ---------- AI 配置（系统管理 → AI 配置, sys_param 热参数, 免重启生效） ----------

type aiConfigView struct {
	Enabled    bool     `json:"enabled"`
	BaseURL    string   `json:"baseURL"`
	Model      string   `json:"model"`
	APIKey     string   `json:"apiKey"` // 恒脱敏(掩码)
	TimeoutSec int      `json:"timeoutSec"`
	Overrides  []string `json:"overrides"` // 已用页面配置覆盖环境变量的字段
}

func (h *Handler) configView() aiConfigView {
	eff := h.effective()
	v := aiConfigView{
		Enabled:    eff.Enabled,
		BaseURL:    eff.BaseURL,
		Model:      eff.Model,
		APIKey:     maskAPIKey(eff.APIKey),
		TimeoutSec: int(eff.Timeout / time.Second),
		Overrides:  []string{},
	}
	if h.params.Get("ai.enabled", "") != "" {
		v.Overrides = append(v.Overrides, "enabled")
	}
	if h.params.Get("ai.base_url", "") != "" {
		v.Overrides = append(v.Overrides, "baseURL")
	}
	if h.params.Get("ai.model", "") != "" {
		v.Overrides = append(v.Overrides, "model")
	}
	if h.params.Get("ai.api_key", "") != "" {
		v.Overrides = append(v.Overrides, "apiKey")
	}
	if h.params.Get("ai.timeout_sec", "") != "" {
		v.Overrides = append(v.Overrides, "timeoutSec")
	}
	return v
}

// GetConfig 当前生效的 AI 配置（apiKey 脱敏; 空字段表示跟随环境变量）
func (h *Handler) GetConfig(c *gin.Context) {
	resp.OK(c, h.configView())
}

// PutConfig 保存 AI 配置（apiKey 留空或回显掩码 = 不修改; 保存后热生效）
func (h *Handler) PutConfig(c *gin.Context) {
	var req struct {
		Enabled    bool   `json:"enabled"`
		BaseURL    string `json:"baseURL"`
		Model      string `json:"model"`
		APIKey     string `json:"apiKey"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if req.TimeoutSec < 5 || req.TimeoutSec > 600 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "超时须为 5~600 秒"))
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL != "" && baseURL != "******" {
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "Base URL 须以 http:// 或 https:// 开头"))
			return
		}
	}
	updates := map[string]string{
		"ai.enabled":     strconv.FormatBool(req.Enabled),
		"ai.base_url":    baseURL,
		"ai.model":       strings.TrimSpace(req.Model),
		"ai.timeout_sec": strconv.Itoa(req.TimeoutSec),
	}
	if req.APIKey != "" && !strings.HasPrefix(req.APIKey, "******") {
		updates["ai.api_key"] = strings.TrimSpace(req.APIKey)
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	tx := h.db.Begin()
	for k, v := range updates {
		var p persistence.SysParam
		// key 在 PostgreSQL 中为非保留字, 无需引号(Postgres 不使用反引号, GORM 亦不会转换)
		if err := tx.Where("key = ?", k).First(&p).Error; err == nil {
			_ = tx.Model(&p).Updates(map[string]any{"value": v, "updated_by": un, "updated_at": time.Now()}).Error
		} else {
			_ = tx.Create(&persistence.SysParam{Key: k, Value: v, UpdatedBy: un, UpdatedAt: time.Now()}).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	h.params.Invalidate()
	middleware.Log(c, "system", "ai.config.put", "", "enabled="+strconv.FormatBool(req.Enabled))
	resp.OK(c, h.configView())
}

// TestConnection 用当前（或随请求临时提交的）配置做连通性校验
func (h *Handler) TestConnection(c *gin.Context) {
	var req struct {
		BaseURL string `json:"baseURL"`
		Model   string `json:"model"`
		APIKey  string `json:"apiKey"`
	}
	_ = c.ShouldBindJSON(&req) // 允许空 = 使用当前生效配置
	eff := h.effective()
	if b := strings.TrimSpace(req.BaseURL); b != "" && b != "******" {
		eff.BaseURL = strings.TrimRight(b, "/")
	}
	if m := strings.TrimSpace(req.Model); m != "" {
		eff.Model = m
	}
	if k := strings.TrimSpace(req.APIKey); k != "" && !strings.HasPrefix(k, "******") {
		eff.APIKey = k
	}
	if eff.BaseURL == "" || eff.Model == "" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "请先填写 Base URL 与模型名称"))
		return
	}
	llm := NewOpenAICompat(&config.Config{AIBaseURL: eff.BaseURL, AIModel: eff.Model, AIKey: eff.APIKey, AITimeout: 20 * time.Second})
	start := time.Now()
	out, err := llm.Chat(c.Request.Context(), ChatReq{
		Feature:   "config_test",
		Messages:  []Message{{Role: "user", Content: "ping"}},
		MaxTokens: 16,
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		h.audit("config_test", "", "failed", err.Error(), nil, latency)
		resp.OK(c, gin.H{"ok": false, "message": "连接失败: " + err.Error(), "latencyMs": latency})
		return
	}
	content := out.Content
	h.audit("config_test", "", "success", "", &content, latency)
	resp.OK(c, gin.H{"ok": true, "message": "连接成功", "model": out.Model, "latencyMs": latency})
}

// maskAPIKey 展示层脱敏: 保留尾 4 位
func maskAPIKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "******"
	}
	return "******" + k[len(k)-4:]
}

// unavailable AI 不可用 → 50300 + 降级提示（主流程不阻塞, NFR 7.4）
func (h *Handler) unavailable(c *gin.Context, feature string, err error) {
	resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "AI 服务暂不可用(%v)，请手工配置", err))
	h.audit(feature, "", "degraded", err.Error(), nil, 0)
}

// audit 调用审计（FR-AI-04: 输入输出摘要, 脱敏）
func (h *Handler) audit(feature, user, status, errMsg string, out *string, latencyMS int64) {
	lat := int(latencyMS)
	rec := persistence.AICallLog{
		Feature: feature, UserID: user, Model: h.effective().Model,
		Status: status, ErrMsg: masking.Digest(errMsg, 500), LatencyMS: &lat,
	}
	if out != nil {
		rec.OutputDigest = masking.Digest(*out, 500)
	}
	if err := h.db.Create(&rec).Error; err != nil {
		h.log.Error("ai audit write failed", "feature", feature, "status", status, "err", err.Error())
	}
}

// Chat 配置助手对话
func (h *Handler) Chat(c *gin.Context) {
	var req struct {
		Message string `json:"message" binding:"required"`
		Context string `json:"context"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	llm := h.client()
	if llm == nil {
		h.unavailable(c, "assistant", fmt.Errorf("未启用或未配置"))
		return
	}
	user, _ := c.Get(middleware.CtxUsername)
	un, _ := user.(string)
	// 强制脱敏（服务端兜底）
	safe := masking.SanitizeForAI(req.Context + "\n" + req.Message)
	start := time.Now()
	chatReq := ChatReq{
		Feature: "assistant",
		Messages: []Message{
			{Role: "system", Content: "你是容量管理平台的配置助手。用户描述采集/清洗配置问题或需求, 你给出配置建议与诊断。回答使用中文, 简洁可执行。"},
			{Role: "user", Content: safe},
		},
		MaxTokens: 1500,
	}
	out, err := llm.Chat(c.Request.Context(), chatReq)
	if err != nil {
		h.audit("assistant", un, "failed", err.Error(), nil, time.Since(start).Milliseconds())
		h.unavailable(c, "assistant", err)
		return
	}
	h.audit("assistant", un, "success", "", &out.Content, time.Since(start).Milliseconds())
	resp.OK(c, gin.H{"reply": out.Content, "model": out.Model})
}

// FlowGenerate 自然语言 → 流程配置建议（FR-AI-01）
func (h *Handler) FlowGenerate(c *gin.Context) {
	var req struct {
		Description string `json:"description" binding:"required"`
		SystemCode  string `json:"systemCode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	llm := h.client()
	if llm == nil {
		h.unavailable(c, "flow_gen", fmt.Errorf("未启用或未配置"))
		return
	}
	user, _ := c.Get(middleware.CtxUsername)
	un, _ := user.(string)
	safe := masking.SanitizeForAI(req.Description)
	start := time.Now()
	chatReq := ChatReq{
		Feature: "flow_gen",
		Messages: []Message{
			{Role: "system", Content: `你是采集流程配置专家。根据用户描述生成 JSON 采集流程建议, 格式:
{"collectType":"sql|logparse|python","collectConfig":{...},"cleanConfig":{...},"scheduleMode":"normal|trading_day","cronExpr":"...","recommendation":"说明"}
只输出 JSON, 不要解释。变量可用 ${biz_date}/${prev_trading_day}/${system_code}/${hour}。`},
			{Role: "user", Content: safe},
		},
		MaxTokens: 2000,
	}
	out, err := llm.Chat(c.Request.Context(), chatReq)
	if err != nil {
		h.audit("flow_gen", un, "failed", err.Error(), nil, time.Since(start).Milliseconds())
		h.unavailable(c, "flow_gen", err)
		return
	}
	h.audit("flow_gen", un, "success", "", &out.Content, time.Since(start).Milliseconds())
	// 提取 JSON 块
	draft := extractJSON(out.Content)
	resp.OK(c, gin.H{
		"draft":        draft,
		"raw":          out.Content,
		"notice":       "AI 生成内容仅供参考, 请人工审核并预览试跑后再保存生效",
		"reviewNeeded": true,
	})
}

// CleanGenerate 样例+需求 → 清洗脚本（FR-AI-02）
func (h *Handler) CleanGenerate(c *gin.Context) {
	var req struct {
		SampleData  string `json:"sampleData" binding:"required"`
		Requirement string `json:"requirement" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	llm := h.client()
	if llm == nil {
		h.unavailable(c, "clean_gen", fmt.Errorf("未启用或未配置"))
		return
	}
	user, _ := c.Get(middleware.CtxUsername)
	un, _ := user.(string)
	safe := masking.SanitizeForAI(req.SampleData + "\n需求: " + req.Requirement)
	start := time.Now()
	chatReq := ChatReq{
		Feature: "clean_gen",
		Messages: []Message{
			{Role: "system", Content: `你是 Python 数据清洗脚本专家。按用户样例与需求生成清洗脚本。
约定: 输入从环境变量 CAP_INPUT 读取(JSON), 输出 JSON 数组到 stdout 最后一行, 元素 {"value":数值,"unit":"单位"}。
只允许使用白名单模块(json/math/re/datetime/decimal/collections)。只输出 Python 代码, 不要解释。`},
			{Role: "user", Content: safe},
		},
		MaxTokens: 3000,
	}
	out, err := llm.Chat(c.Request.Context(), chatReq)
	if err != nil {
		h.audit("clean_gen", un, "failed", err.Error(), nil, time.Since(start).Milliseconds())
		h.unavailable(c, "clean_gen", err)
		return
	}
	h.audit("clean_gen", un, "success", "", &out.Content, time.Since(start).Milliseconds())
	script := extractPythonCode(out.Content)
	resp.OK(c, gin.H{
		"script":       script,
		"notice":       "请保存为脚本新版本并试跑对比后生效",
		"reviewNeeded": true,
	})
}

// CleanTryRun 清洗脚本试跑对比（FR-AI-02: 样例数据试跑验证, 输出清洗前后对比）
func (h *Handler) CleanTryRun(c *gin.Context) {
	var req struct {
		Script     string `json:"script" binding:"required"`
		SampleData string `json:"sampleData" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if h.py == nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "沙箱 Worker 未配置"))
		return
	}
	// 样例数据须为合法 JSON（数组）
	var input any
	if err := json.Unmarshal([]byte(req.SampleData), &input); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "样例数据不是合法 JSON: %v", err))
		return
	}
	if arr, ok := input.([]any); !ok || len(arr) == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "样例数据须为非空 JSON 数组"))
		return
	}
	// 语法校验（不执行）
	ck, err := h.py.Check(c.Request.Context(), req.Script)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "Worker 不可达: %v", err))
		return
	}
	if !ck.OK {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "脚本语法错误: %s", ck.Error))
		return
	}
	start := time.Now()
	taskID := "ai-tryrun-" + time.Now().Format("20060102150405")
	er, err := h.py.Execute(c.Request.Context(), pyclient.ExecuteReq{
		TaskID: taskID, ScriptType: "clean", ScriptContent: req.Script,
		Input: req.SampleData, Params: map[string]string{"biz_date": time.Now().Format("2006-01-02")},
		TimeoutMs: 30000,
	})
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "Worker 调用失败: %v", err))
		return
	}
	out := parseTryRunOutput(er.Data)
	resp.OK(c, gin.H{
		"status":     er.Status,
		"input":      input,
		"inputRows":  jsonArrLen(input),
		"output":     out,
		"outputRows": jsonArrLen(out),
		"durationMs": time.Since(start).Milliseconds(),
		"workerMs":   er.DurationMS,
		"error":      er.ErrorMsg(),
		"stdoutTail": tailStr(er.Stdout, 1000),
		"stderrTail": tailStr(er.Stderr, 1000),
		"notice":     "试跑在沙箱内执行（不落库/不判定）, 请对比清洗前后数据确认无误后再保存生效",
	})
}

func parseTryRunOutput(data string) any {
	s := strings.TrimSpace(data)
	if s == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}

func jsonArrLen(v any) int {
	if arr, ok := v.([]any); ok {
		return len(arr)
	}
	return 0
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Logs AI 调用审计检索
func (h *Handler) Logs(c *gin.Context) {
	var items []persistence.AICallLog
	_ = h.db.Order("created_at DESC").Limit(200).Find(&items).Error
	resp.OK(c, items)
}

func extractJSON(s string) any {
	idx := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if idx < 0 || end < 0 || end < idx {
		return map[string]any{"raw": s}
	}
	var v any
	if err := json.Unmarshal([]byte(s[idx:end+1]), &v); err != nil {
		return map[string]any{"raw": s, "parseError": err.Error()}
	}
	return v
}

func extractPythonCode(s string) string {
	if i := strings.Index(s, "```python"); i >= 0 {
		rest := s[i+len("```python"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+len("```"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return strings.TrimSpace(s)
}

var _ context.Context
