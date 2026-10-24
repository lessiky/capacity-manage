package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/pkg/masking"
)

// LLMClient 可插拔适配器接口（docs/10-design/05 §2）
type LLMClient interface {
	Chat(ctx context.Context, req ChatReq) (ChatResp, error)
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatReq struct {
	Feature     string    `json:"feature"` // flow_gen/clean_gen/assistant
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"maxTokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type ChatResp struct {
	Content string
	Model   string
	Usage   struct{ Prompt, Completion int }
}

// openaiCompat OpenAI 协议兼容实现（行内网关/通义/文心等）
type openaiCompat struct {
	base    string
	key     string
	model   string
	client  *http.Client
	timeout time.Duration
}

func NewOpenAICompat(cfg *config.Config) LLMClient {
	return &openaiCompat{
		base:    cfg.AIBaseURL,
		key:     cfg.AIKey,
		model:   cfg.AIModel,
		client:  &http.Client{Timeout: cfg.AITimeout},
		timeout: cfg.AITimeout,
	}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

func (o *openaiCompat) Chat(ctx context.Context, req ChatReq) (ChatResp, error) {
	body, _ := json.Marshal(chatRequest{
		Model: o.model, Messages: req.Messages,
		MaxTokens: req.MaxTokens, Temperature: req.Temperature,
	})
	hctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(hctx, http.MethodPost, o.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResp{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return ChatResp{}, fmt.Errorf("llm 请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return ChatResp{}, fmt.Errorf("llm 返回 %d: %s", resp.StatusCode, masking.Digest(string(data), 200))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return ChatResp{}, fmt.Errorf("llm 响应解析失败: %w", err)
	}
	if len(cr.Choices) == 0 {
		return ChatResp{}, fmt.Errorf("llm 返回空 choices")
	}
	out := ChatResp{Content: cr.Choices[0].Message.Content, Model: cr.Model}
	out.Usage.Prompt = cr.Usage.PromptTokens
	out.Usage.Completion = cr.Usage.CompletionTokens
	return out, nil
}