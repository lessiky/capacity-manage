package report

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// TestPDFReproM4 复现 M4 e2e 真实数据形态（含 missing/nodata 行）
func TestPDFReproM4(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	rows := []DailyRow{
		{MetricCode: "M4_HIGHER", Name: "M4M4_HIGHER", SystemCode: "TEST_SYS01", SystemName: "M4测试系统一", Unit: "GB",
			StatValue: f(520.1), WaterLevel: f(0.5201), Level: "warn", Baseline: f(1000), WarnLine: f(500), DangerLine: f(600)},
		{MetricCode: "M4_DANGER", Name: "M4M4_DANGER", SystemCode: "TEST_SYS01", SystemName: "M4测试系统一", Unit: "GB",
			StatValue: f(520.1), WaterLevel: f(0.5201), Level: "danger", Baseline: f(1000), WarnLine: f(500), DangerLine: f(510)},
		{MetricCode: "M3_ALERT_D", Name: "M3M3_ALERT_D", SystemCode: "TEST_SYS01", SystemName: "M4测试系统一", Unit: "GB",
			StatValue: f(520.1), WaterLevel: f(0.5201), Level: "danger", Baseline: f(1000), WarnLine: f(500), DangerLine: f(510)},
		{MetricCode: "M4_MISS", Name: "M4M4_MISS", SystemCode: "TEST_SYS01", SystemName: "M4测试系统一", Unit: "GB",
			Level: "missing", Baseline: f(1000), WarnLine: f(500), DangerLine: f(600)},
		{MetricCode: "M4_NODATA", Name: "M4M4_NODATA", SystemCode: "TEST_SYS01", SystemName: "M4测试系统一", Unit: "GB",
			Level: "nodata", Baseline: f(1000), WarnLine: f(500), DangerLine: f(600)},
		{MetricCode: "M4_OTHER", Name: "M4M4_OTHER", SystemCode: "TEST_SYS02", SystemName: "M4第二系统", Unit: "GB",
			Level: "missing", Baseline: f(1000), WarnLine: f(500), DangerLine: f(600)},
		{MetricCode: "M3_OK", Name: "M3M3_OK", SystemCode: "TEST_SYS01", SystemName: "M4测试系统一", Unit: "GB",
			StatValue: f(100), WaterLevel: f(0.1), Level: "normal", Baseline: f(1000), WarnLine: f(9000), DangerLine: f(9500)},
	}
	data := &DailyData{BizDate: "2026-10-16", Rows: rows, Fails: []CollectFail{}, GeneratedAt: time.Now()}
	pdf, err := BuildPDF(data)
	if err != nil {
		t.Fatalf("BuildPDF error: %v", err)
	}
	if len(pdf) < 2000 || !bytes.Equal(pdf[:4], []byte("%PDF")) {
		t.Fatalf("bad pdf: len=%d", len(pdf))
	}
	fmt.Println("pdf bytes:", len(pdf))
}
