package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultInfluxDBPort = 8086

// InfluxSeries is the query result shape returned by InfluxDB 1.x.
type InfluxSeries struct {
	Columns []string `json:"columns"`
	Values  [][]any  `json:"values"`
}

type influxQueryResponse struct {
	Results []struct {
		Error  string         `json:"error"`
		Series []InfluxSeries `json:"series"`
	} `json:"results"`
}

type influxdbAdapter struct{}

func (influxdbAdapter) Test(ctx context.Context, cfg *Config) (TestResult, error) {
	q := fmt.Sprintf(`SHOW MEASUREMENTS ON %s LIMIT 1`, influxIdentifier(cfg.Database))
	series, err := queryInfluxDB(ctx, cfg, q)
	if err != nil {
		return TestResult{OK: false, Message: "InfluxDB 连接失败: " + err.Error()}, nil
	}
	rows := 0
	for _, s := range series {
		rows += len(s.Values)
	}
	return TestResult{OK: true, Message: "连接成功", Rows: rows}, nil
}

// QueryInfluxDB executes an InfluxQL statement against an InfluxDB 1.x server.
func QueryInfluxDB(ctx context.Context, cfg *Config, statement string) ([]InfluxSeries, error) {
	return queryInfluxDB(ctx, cfg, statement)
}

func queryInfluxDB(ctx context.Context, cfg *Config, statement string) ([]InfluxSeries, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if err := influxPing(ctx, client, cfg); err != nil {
		return nil, err
	}
	endpoint, err := influxEndpoint(cfg, "/query")
	if err != nil {
		return nil, err
	}
	form := url.Values{"q": {statement}, "db": {cfg.Database}, "epoch": {"ms"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	setInfluxAuth(req, cfg)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out influxQueryResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	if len(out.Results) == 0 {
		return nil, fmt.Errorf("响应缺少 results")
	}
	var series []InfluxSeries
	for _, r := range out.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("%s", r.Error)
		}
		series = append(series, r.Series...)
	}
	return series, nil
}

func influxPing(ctx context.Context, client *http.Client, cfg *Config) error {
	endpoint, err := influxEndpoint(cfg, "/ping")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	setInfluxAuth(req, cfg)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ping HTTP %d", resp.StatusCode)
	}
	return nil
}

func influxEndpoint(cfg *Config, path string) (string, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return "", fmt.Errorf("host 为空")
	}
	scheme := "http"
	if cfg.Secure {
		scheme = "https"
	}
	port := cfg.Port
	if port == 0 {
		port = defaultInfluxDBPort
	}
	return (&url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", cfg.Host, port),
		Path:   path,
	}).String(), nil
}

func setInfluxAuth(req *http.Request, cfg *Config) {
	if cfg.Username != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
}

func influxIdentifier(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}
