package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/datasource"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
)

func TestCollectInfluxQLRendersTemplateAndAggregatesValueColumn(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = r.ParseForm()
		query = r.Form.Get("q")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"statement_id": 0,
					"series": []any{
						map[string]any{
							"name":    "cpu",
							"columns": []string{"time", "usage"},
							"values": []any{
								[]any{"2026-10-27T00:00:00Z", 10},
								[]any{"2026-10-27T01:00:00Z", 20},
								[]any{"2026-10-27T02:00:00Z", 30},
							},
						},
					},
				},
			},
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
	cipher, err := aesgcm.New(strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	collectors := &Collectors{cred: datasource.NewRegistry(nil, cipher, nil, slog.Default())}
	ds := &persistence.DataSource{
		Name: "influx-capacity", Type: "influxdb", Status: "available",
		Config: fmt.Sprintf(`{"type":"influxdb","host":%q,"port":%d,"database":"capacity"}`, host, port),
	}
	fl := &persistence.CollectFlow{
		CollectType: "influxql",
		CollectConfig: `{"queryTemplate":"SELECT mean(\"usage\") FROM \"cpu\" WHERE time >= '${biz_date}'",` +
			`"valueColumn":"usage","aggregate":"avg"}`,
	}
	metric := &persistence.CapacityMetric{MetricCode: "CPU_USAGE", Unit: "%"}

	rows, meta, err := collectors.Collect(context.Background(), fl, []*persistence.DataSource{ds}, metric,
		map[string]string{"biz_date": "2026-10-27"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if query != `SELECT mean("usage") FROM "cpu" WHERE time >= '2026-10-27'` {
		t.Fatalf("query = %q", query)
	}
	if len(rows) != 1 || rows[0].Raw != "20" {
		t.Fatalf("rows = %+v, want one avg row 20", rows)
	}
	if meta == nil || meta.RowsTotal != 3 || meta.ParseFailCount != 0 {
		t.Fatalf("meta = %+v", meta)
	}
}
