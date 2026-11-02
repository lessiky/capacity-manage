package datasource

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
)

func TestInfluxDBAdapterTestUsesBasicAuthAndConfiguredDatabase(t *testing.T) {
	var (
		pingCalled       bool
		queryCalled      bool
		queryDatabase    string
		queryStatement   string
		username, secret string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if ok {
			username, secret = u, p
		}
		switch r.URL.Path {
		case "/ping":
			pingCalled = r.Method == http.MethodGet
			w.WriteHeader(http.StatusNoContent)
		case "/query":
			queryCalled = r.Method == http.MethodPost
			_ = r.ParseForm()
			queryDatabase = r.Form.Get("db")
			queryStatement = r.Form.Get("q")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []any{
					map[string]any{
						"statement_id": 0,
						"series": []any{
							map[string]any{
								"name":    "capacity",
								"columns": []string{"time", "value"},
								"values":  []any{[]any{"2026-10-27T00:00:00Z", 1}},
							},
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	host, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Type: "influxdb", Host: host, Port: port, Database: "capacity",
		Username: "cap-reader", Password: "secret",
	}

	res, err := (influxdbAdapter{}).Test(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("test result = %+v", res)
	}
	if res.Rows != 1 {
		t.Fatalf("rows = %d, want 1", res.Rows)
	}
	if !pingCalled || !queryCalled {
		t.Fatalf("pingCalled=%v queryCalled=%v", pingCalled, queryCalled)
	}
	if username != "cap-reader" || secret != "secret" {
		t.Fatalf("basic auth = %q/%q", username, secret)
	}
	if queryDatabase != "capacity" {
		t.Fatalf("db = %q, want capacity", queryDatabase)
	}
	if queryStatement != `SHOW MEASUREMENTS ON "capacity" LIMIT 1` {
		t.Fatalf("query = %q", queryStatement)
	}
}

func TestRegistryTestDispatchesInfluxDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{map[string]any{"statement_id": 0}},
		})
	}))
	defer srv.Close()

	host, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := newTestHandler(t)
	src := &persistence.DataSource{
		Type: "influxdb",
		Config: `{"type":"influxdb","host":"` + host + `","port":` +
			strconv.Itoa(port) + `,"database":"capacity"}`,
	}
	res, err := h.cred.Test(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Message, "未知数据源类型") {
		t.Fatalf("influxdb 未接入连接测试分支: %s", res.Message)
	}
}

func TestInfluxEndpointUsesHTTPSWhenConfigured(t *testing.T) {
	got, err := influxEndpoint(&Config{
		Host: "influx.internal", Port: 8086, Secure: true,
	}, "/query")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://influx.internal:8086/query" {
		t.Fatalf("endpoint = %q", got)
	}
}
