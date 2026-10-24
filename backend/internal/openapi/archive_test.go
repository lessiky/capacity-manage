package openapi

import (
	"testing"
	"time"
)

func TestCutoffMonthStart(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 4, 5, 0, time.Local)
	cases := []struct {
		retainDays int
		want       string
	}{
		{retainDays: 90, want: "2026-07-01"},
		{retainDays: 1095, want: "2023-10-01"},
		{retainDays: 0, want: "2026-10-01"},
		{retainDays: -5, want: "2026-10-01"}, // 非正数按当日处理，避免越界
	}
	for _, c := range cases {
		got := cutoffMonthStart(now, c.retainDays).Format("2006-01-02")
		if got != c.want {
			t.Fatalf("cutoffMonthStart(now, %d) = %s, want %s", c.retainDays, got, c.want)
		}
	}
}

func TestIsDuePartition(t *testing.T) {
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	due := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local) // 分区次月首日恰好等于 cutoff
	if !isDuePartition(due, cutoff) {
		t.Fatalf("次月首日等于 cutoff 的整月分区应视为超期")
	}
	if isDuePartition(time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local), cutoff) {
		t.Fatalf("尚未完整超期的分区不应归档")
	}
}

func TestArchiveKeyAndIdentValidation(t *testing.T) {
	if got := archiveKey(TableDay, "metric_value_day_y2026m06"); got != "archive/metric_value_day/metric_value_day_y2026m06.jsonl.gz" {
		t.Fatalf("归档文件 key 错误: %s", got)
	}
	if got := archiveKey(TableRT, "metric_value_rt_y2026m06"); got != "archive/metric_value_rt/metric_value_rt_y2026m06.jsonl.gz" {
		t.Fatalf("RT 归档文件 key 错误: %s", got)
	}
	for _, ok := range []string{"metric_value_day_y2026m06", "metric_value_rt_y2026m06"} {
		if !validIdent(ok) {
			t.Fatalf("%q 应为合法标识符", ok)
		}
	}
	for _, bad := range []string{"", "metric_value_day; DROP TABLE x", "metric_value_day y", "a-b", `"quoted"`} {
		if validIdent(bad) {
			t.Fatalf("%q 不应通过标识符校验", bad)
		}
	}
}

func TestSupportedArchiveTable(t *testing.T) {
	if !supportedArchiveTable(TableDay) || !supportedArchiveTable(TableRT) {
		t.Fatalf("日内/日数据表都应支持归档")
	}
	if supportedArchiveTable("api_call_log") {
		t.Fatalf("非数据表不应支持归档")
	}
}

func TestPayloadRowMappingPreservesFields(t *testing.T) {
	created := time.Date(2026, 6, 3, 8, 30, 0, 0, time.Local)
	snapshot := `{"stat_value": 1}`

	dayRows := payloadsToDayRows([]archivePayload{{
		MetricCode: "M1", BizDate: "2026-06-03", StatValue: f64(3.5), Level: "warn",
		PointCount: 12, Source: "backfill", Corrected: true, CorrectReason: "人工修正",
		CorrectedBy: "admin", CorrectedAt: &created, OriginalSnapshot: &snapshot, CreatedAt: &created,
	}})
	if len(dayRows) != 1 {
		t.Fatalf("应映射 1 行日数据")
	}
	if dayRows[0].MetricCode != "M1" || dayRows[0].BizDate.Format("2006-01-02") != "2026-06-03" {
		t.Fatalf("日数据主键映射错误: %+v", dayRows[0])
	}
	if !dayRows[0].Corrected || dayRows[0].CorrectReason != "人工修正" ||
		dayRows[0].OriginalSnapshot == nil || *dayRows[0].OriginalSnapshot != snapshot {
		t.Fatalf("日数据修正字段丢失: %+v", dayRows[0])
	}

	rtRows := payloadsToRTRows([]archivePayload{{
		MetricCode: "M1", TS: &created, RawValue: f64(1.1), CleanValue: f64(1.0),
		IsAnomaly: true, AnomalyReason: "突变", JudgeLevel: "danger", Source: "collect",
	}})
	if len(rtRows) != 1 {
		t.Fatalf("应映射 1 行日内数据")
	}
	if rtRows[0].Ts.IsZero() || !rtRows[0].Ts.Equal(created) {
		t.Fatalf("日内数据时间戳丢失: %+v", rtRows[0])
	}
	if rtRows[0].CleanValue == nil || *rtRows[0].CleanValue != 1.0 || !rtRows[0].IsAnomaly {
		t.Fatalf("日内数据字段映射错误: %+v", rtRows[0])
	}
}

func f64(v float64) *float64 { return &v }
