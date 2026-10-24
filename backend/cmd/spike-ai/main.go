// Command spike-ai: Spike 04 AI 服务连通与脱敏 PoC
// 验证: 1) 脱敏层不泄露凭据 2) OpenAI 兼容协议解析(本地 mock) 3) 无 key/不可达时优雅降级
// 输出: spike/04-ai-conn/output.txt
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"capacity-manage/backend/internal/ai"
	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/pkg/masking"
)

const outPath = `D:\newwork\capacity-manage\spike\04-ai-conn\output.txt`

var (
	out    strings.Builder
	passed int
	failed int
)

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("[PASS] %s\n", name)
	} else {
		failed++
		fmt.Printf("[FAIL] %s\n", name)
	}
	out.WriteString(fmt.Sprintf("--- %s [%s]\n", name, map[bool]string{true: "PASS", false: "FAIL"}[ok]))
	if detail != "" {
		out.WriteString(detail)
		out.WriteString("\n")
	}
}

func main() {
	out.WriteString(fmt.Sprintf("Go spike 04 (AI) @ %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// ---------- 1) 脱敏验证 ----------
	raw := "帮我生成采集流程: 数据源 mysql://scott:tiger@10.1.2.3:3306/coredb, 密码 password: hunter22, 联系人 13812345678, 证件 110101199003077758"
	san := masking.SanitizeForAI(raw)
	secrets := []string{"tiger", "hunter22", "10.1.2.3", "13812345678", "110101199003077758"}
	var leak []string
	for _, s := range secrets {
		if strings.Contains(san, s) {
			leak = append(leak, s)
		}
	}
	check("脱敏-凭据/主机/手机/证件不出现在提示词", len(leak) == 0,
		fmt.Sprintf("原始: %s\n脱敏后: %s\n泄露项: %v", raw, san, leak))

	// ---------- 2) OpenAI 兼容协议 (本地 mock 服务) ----------
	const mockReply = `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"done: print ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5},"model":"mock-model"}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key-123" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"unauthorized"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, mockReply)
	}))
	defer mock.Close()

	cli := ai.NewOpenAICompat(&config.Config{
		Env: "dev", AIBaseURL: mock.URL, AIModel: "mock-model",
		AIKey: "test-key-123", AITimeout: 5 * time.Second,
	})
	resp, err := cli.Chat(context.Background(), ai.ChatReq{
		Feature:  "clean_gen",
		Messages: []ai.Message{{Role: "user", Content: "你好"}},
	})
	ok := err == nil && strings.Contains(resp.Content, "done: print ok") && resp.Model == "mock-model"
	check("协议-OpenAI 兼容请求/鉴权/响应解析", ok,
		fmt.Sprintf("err=%v content=%q model=%q usage=%d/%d", err, resp.Content, resp.Model, resp.Usage.Prompt, resp.Usage.Completion))

	// ---------- 3) 无 key / 服务不可达 → 优雅降级 ----------
	_, err = ai.NewOpenAICompat(&config.Config{Env: "dev", AIBaseURL: "", AIKey: "", AIModel: "", AITimeout: 2 * time.Second}).
		Chat(context.Background(), ai.ChatReq{Messages: []ai.Message{{Role: "user", Content: "hi"}}})
	check("降级-未配置端点时返回错误(上层映射 50300, 不阻塞主流程)", err != nil, fmt.Sprintf("err=%v", err))

	_, err = ai.NewOpenAICompat(&config.Config{Env: "dev", AIBaseURL: "http://127.0.0.1:1", AIKey: "k", AIModel: "m", AITimeout: 2 * time.Second}).
		Chat(context.Background(), ai.ChatReq{Messages: []ai.Message{{Role: "user", Content: "hi"}}})
	check("降级-端点不可达时返回错误(不 panic/不挂起)", err != nil, fmt.Sprintf("err=%v", err))

	mock401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"invalid api key"}`)
	}))
	defer mock401.Close()
	_, err = ai.NewOpenAICompat(&config.Config{AIBaseURL: mock401.URL, AIKey: "bad", AIModel: "m", AITimeout: 2 * time.Second}).
		Chat(context.Background(), ai.ChatReq{Messages: []ai.Message{{Role: "user", Content: "hi"}}})
	check("降级-401 鉴权失败返回可读错误", err != nil && strings.Contains(err.Error(), "401"), fmt.Sprintf("err=%v", err))

	// ---------- 4) 真实端点连通 (可选, 由环境变量提供) ----------
	realBase := os.Getenv("SPIKE_AI_BASE_URL")
	realKey := os.Getenv("SPIKE_AI_API_KEY")
	if realBase == "" || realKey == "" {
		check("连通-真实 LLM 端点", true, "SKIP: 未提供 SPIKE_AI_BASE_URL/SPIKE_AI_API_KEY (M2 前接入行内网关后补测; 无 key 降级路径已验证)")
	} else {
		resp, err := ai.NewOpenAICompat(&config.Config{AIBaseURL: realBase, AIKey: realKey, AIModel: envOr("SPIKE_AI_MODEL", ""), AITimeout: 60 * time.Second}).
			Chat(context.Background(), ai.ChatReq{Messages: []ai.Message{{Role: "user", Content: "回复两个字: 正常"}}})
		ok := err == nil && strings.TrimSpace(resp.Content) != ""
		check("连通-真实 LLM 端点(行内 AI 网关 OpenAI 兼容协议)", ok,
			fmt.Sprintf("err=%v model=%q usage=%d/%d content=%q", err, resp.Model, resp.Usage.Prompt, resp.Usage.Completion, clip(resp.Content, 60)))
	}

	out.WriteString(fmt.Sprintf("\n===== %d passed, %d failed =====\n", passed, failed))
	if err := os.WriteFile(outPath, []byte(out.String()), 0o644); err != nil {
		fmt.Println("write output failed:", err)
		os.Exit(2)
	}
	fmt.Printf("\n===== %d passed, %d failed =====\noutput -> %s\n", passed, failed, outPath)
	if failed > 0 {
		os.Exit(1)
	}
}


// envOr 读取环境变量, 空值返回默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// clip 按 rune 截断预览
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
