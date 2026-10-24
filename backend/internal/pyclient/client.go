package pyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client Go → Python Worker HTTP 客户端（127.0.0.1:9100）
type Client struct {
	base    string
	timeout time.Duration
	http    *http.Client
}

func New(base string, timeout time.Duration) *Client {
	return &Client{
		base:    base,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout + 5*time.Second},
	}
}

type ExecuteReq struct {
	TaskID        string          `json:"taskId"`
	ScriptType    string          `json:"scriptType"` // collect/clean
	ScriptID      int64           `json:"scriptId,omitempty"`
	Version       int             `json:"version,omitempty"`
	ScriptContent string          `json:"scriptContent,omitempty"` // 平台服务端从 DB 读取后下发
	Input         string          `json:"input,omitempty"`         // clean 类型: 原始数据 JSON
	Params        map[string]string `json:"params"`
	TimeoutMs     int64           `json:"timeoutMs"`
}

type ExecuteResp struct {
	Status     string `json:"status"` // success/failed/timeout
	Data       string `json:"data"`
	ExitCode   int    `json:"exitCode"`
	DurationMS int64  `json:"durationMs"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ErrType    string `json:"errorType,omitempty"`
	ErrMsg     string `json:"errorMessage,omitempty"`
}

func (r *ExecuteResp) ErrorMsg() string {
	if r.ErrType == "" {
		if len(r.Stderr) > 300 {
			return r.Stderr[:300]
		}
		return r.Stderr
	}
	return r.ErrType + ": " + r.ErrMsg
}

// Execute 调用 /v1/execute（脚本内容从平台 DB 由服务端注入——
// MVP 简化: Worker 通过 callback 从平台拉取脚本, 或此处随请求下发; 本实现随请求下发 content）
func (c *Client) Execute(ctx context.Context, req ExecuteReq) (*ExecuteResp, error) {
	body := map[string]any{
		"taskId": req.TaskID, "scriptType": req.ScriptType,
		"scriptId": req.ScriptID, "version": req.Version,
		"scriptContent": req.ScriptContent,
		"params": req.Params, "timeoutMs": req.TimeoutMs,
	}
	if req.Input != "" {
		body["input"] = req.Input
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(hctx, http.MethodPost, c.base+"/v1/execute", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if hctx.Err() == context.DeadlineExceeded {
			return &ExecuteResp{Status: "timeout", ErrMsg: "worker 调用超时"}, nil
		}
		return nil, fmt.Errorf("worker 不可达: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("worker 返回 %d: %s", resp.StatusCode, string(data[:minInt(len(data), 200)]))
	}
	var er ExecuteResp
	if err := json.Unmarshal(data, &er); err != nil {
		return nil, fmt.Errorf("worker 响应解析失败: %w", err)
	}
	return &er, nil
}

// CheckResp 语法校验结果
type CheckResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Line  *int   `json:"line,omitempty"`
}

// Check 调用 /v1/check 做语法校验(不执行)
func (c *Client) Check(ctx context.Context, scriptContent string) (*CheckResp, error) {
	body, _ := json.Marshal(map[string]any{"scriptContent": scriptContent})
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(hctx, http.MethodPost, c.base+"/v1/check", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("worker 不可达: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("worker 返回 %d: %s", resp.StatusCode, string(data[:minInt(len(data), 200)]))
	}
	var cr CheckResp
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, fmt.Errorf("worker 响应解析失败: %w", err)
	}
	return &cr, nil
}

// Health 探测 Worker 存活
func (c *Client) Health(ctx context.Context) (bool, error) {
	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, herr := http.NewRequestWithContext(hctx, http.MethodGet, c.base+"/v1/health", nil)
	if herr != nil {
		return false, herr
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}