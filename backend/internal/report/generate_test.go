package report

import (
	"bytes"
	"context"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/store"
	"capacity-manage/backend/internal/testutil"
)

func i64ptr(v int64) *int64 { return &v }

func sampleData() *DailyData {
	f := func(v float64) *float64 { return &v }
	rows := make([]DailyRow, 0, 130)
	for i := 0; i < 100; i++ {
		rows = append(rows, DailyRow{
			MetricCode: "M_NORMAL_" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			Name:       "正常指标" + string(rune('A'+i%26)), SystemCode: "SYS_A", SystemName: "系统A",
			Unit: "GB", StatValue: f(100 + float64(i)), WaterLevel: f(0.2 + float64(i%50)/100),
			Level: "normal",
		})
	}
	for i := 0; i < 20; i++ {
		rows = append(rows, DailyRow{
			MetricCode: "M_WARN_" + string(rune('a'+i)),
			Name:       "预警指标" + string(rune('a'+i)), SystemCode: "SYS_B", SystemName: "系统B",
			Unit: "GB", StatValue: f(800 + float64(i)), WaterLevel: f(0.8 + float64(i)/100),
			Level: "warn",
		})
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, DailyRow{
			MetricCode: "M_DANGER_" + string(rune('b'+i)),
			Name:       "危险指标" + string(rune('b'+i)), SystemCode: "SYS_C", SystemName: "系统C",
			Unit: "GB", StatValue: f(950 + float64(i)), WaterLevel: f(0.95 + float64(i)/100),
			Level: "danger",
		})
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, DailyRow{
			MetricCode: "M_MISS_" + string(rune('c'+i)),
			Name:       "缺数指标" + string(rune('c'+i)), SystemCode: "SYS_A", SystemName: "系统A",
			Unit: "GB", Level: "missing",
		})
	}
	return &DailyData{
		BizDate: "2026-10-15",
		Rows:    rows,
		Fails: []CollectFail{{
			FlowCode: "F01", MetricCode: "M_MISS_a", Status: "failed",
			TriggeredAt: time.Now(), ErrMsg: "连接超时: test",
		}},
		GeneratedAt: time.Now(),
	}
}

func TestChartsPNG(t *testing.T) {
	bar, donut, err := sampleData().Charts()
	if err != nil {
		t.Fatalf("charts: %v", err)
	}
	if len(bar) < 100 || bar[0] != 0x89 || bar[1] != 'P' || bar[2] != 'N' || bar[3] != 'G' {
		t.Fatal("bar not PNG")
	}
	if len(donut) < 100 || donut[0] != 0x89 || donut[1] != 'P' {
		t.Fatal("donut not PNG")
	}
	// 空数据不报错
	empty := &DailyData{BizDate: "2026-10-15"}
	bar2, donut2, err := empty.Charts()
	if err != nil || bar2 == nil || donut2 == nil {
		t.Fatalf("empty charts: %v", err)
	}
}

func TestBuildExcel(t *testing.T) {
	b, err := BuildExcel(sampleData())
	if err != nil {
		t.Fatalf("build excel: %v", err)
	}
	if len(b) < 5000 || !bytes.HasPrefix(b, []byte("PK")) {
		t.Fatalf("excel magic bad, len=%d", len(b))
	}
}

func TestBuildPDF(t *testing.T) {
	b, err := BuildPDF(sampleData())
	if err != nil {
		t.Fatalf("build pdf: %v", err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatal("pdf magic bad")
	}
	if len(b) < 30000 {
		t.Fatalf("pdf too small (%d), font likely not embedded", len(b))
	}
	// 空数据
	b2, err := BuildPDF(&DailyData{BizDate: "2026-10-15"})
	if err != nil || !bytes.HasPrefix(b2, []byte("%PDF")) {
		t.Fatalf("empty pdf: %v", err)
	}
}

func TestOverRowsSort(t *testing.T) {
	d := sampleData()
	over := d.OverRows()
	if len(over) != 25 {
		t.Fatalf("over rows = %d, want 25", len(over))
	}
	if over[0].Level != "danger" {
		t.Fatalf("first should be danger, got %s", over[0].Level)
	}
	dangerEnd := 0
	for dangerEnd < len(over) && over[dangerEnd].Level == "danger" {
		dangerEnd++
	}
	if dangerEnd != 5 {
		t.Fatalf("danger count = %d, want 5", dangerEnd)
	}
	// 同级内按水位降序
	for i := 1; i < len(over); i++ {
		if over[i-1].Level != over[i].Level {
			continue
		}
		if over[i-1].WaterLevel == nil || over[i].WaterLevel == nil {
			continue
		}
		if *over[i-1].WaterLevel < *over[i].WaterLevel {
			t.Fatalf("not desc within %s at %d", over[i].Level, i)
		}
	}
}

func seedReportDB(t *testing.T, h *testutil.DBHarness, bd time.Time) {
	t.Helper()
	if err := h.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_A", Name: "系统A", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_B", Name: "系统B", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	f := func(v float64) *float64 { return &v }
	// 指标A: 有日数据
	_ = h.DB.Create(&persistence.CapacityMetric{MetricCode: "M_A", Name: "指标A", SystemCode: "SYS_A", Status: "active"}).Error
	// 指标B: 无日数据, 流程建档早于业务日 → missing
	_ = h.DB.Create(&persistence.CapacityMetric{MetricCode: "M_B", Name: "指标B", SystemCode: "SYS_A", Status: "active"}).Error
	// 指标C: 无日数据, 无流程 → nodata
	_ = h.DB.Create(&persistence.CapacityMetric{MetricCode: "M_C", Name: "指标C", SystemCode: "SYS_B", Status: "active"}).Error
	_ = h.DB.Create(&persistence.MetricValueDay{
		MetricCode: "M_A", BizDate: bd, StatValue: f(520.1), WaterLevel: f(0.5201), Level: "warn",
	}).Error
	_ = h.DB.Create(&persistence.CollectFlow{
		FlowCode: "F_B", Name: "流程B", MetricCode: "M_B", DataSourceID: i64ptr(1),
		CollectType: "sql", CollectConfig: "{}", CleanConfig: "{}",
		Status: "active", Enabled: false,
	}).Error
	// 把流程B的建档时间提前
	_ = h.DB.Model(&persistence.CollectFlow{}).Where("flow_code = 'F_B'").
		Updates(map[string]any{"created_at": bd.AddDate(0, 0, -10)}).Error
}

func TestCollectDailyMissing(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.CapacityMetric{}, &persistence.MetricValueDay{},
		&persistence.CollectFlow{}, &persistence.CollectTaskLog{}, &persistence.ForecastRun{},
		&persistence.ForecastResult{}, &persistence.AlertRecord{}, &persistence.AlertLevelHistory{})
	bd := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	seedReportDB(t, h, bd)

	d, err := CollectDaily(h.DB, nil, bd)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]string{}
	for _, r := range d.Rows {
		got[r.MetricCode] = r.Level
	}
	if got["M_A"] != "warn" {
		t.Fatalf("M_A = %s, want warn", got["M_A"])
	}
	if got["M_B"] != "missing" {
		t.Fatalf("M_B = %s, want missing", got["M_B"])
	}
	if got["M_C"] != "nodata" {
		t.Fatalf("M_C = %s, want nodata", got["M_C"])
	}
	if c := d.Status(); c.Normal != 0 || c.Warn != 1 || c.Missing != 1 || c.Nodata != 1 {
		t.Fatalf("status = %+v", c)
	}
}

func TestCollectDailyScope(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.CapacityMetric{}, &persistence.MetricValueDay{},
		&persistence.CollectFlow{}, &persistence.CollectTaskLog{}, &persistence.ForecastRun{},
		&persistence.ForecastResult{}, &persistence.AlertRecord{}, &persistence.AlertLevelHistory{})
	bd := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	seedReportDB(t, h, bd)

	d, err := CollectDaily(h.DB, []string{"SYS_B"}, bd)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(d.Rows) != 1 || d.Rows[0].MetricCode != "M_C" {
		t.Fatalf("scoped rows = %+v", d.Rows)
	}
	// 空权限 → 无行
	d2, err := CollectDaily(h.DB, []string{}, bd)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(d2.Rows) != 0 {
		t.Fatalf("empty scope rows = %d, want 0", len(d2.Rows))
	}
}

func TestCollectDailyForecastAndLevelHistory(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.BusinessSystem{},
		&persistence.CapacityMetric{},
		&persistence.MetricValueDay{},
		&persistence.CollectFlow{},
		&persistence.CollectTaskLog{},
		&persistence.ForecastRun{},
		&persistence.ForecastResult{},
		&persistence.AlertRecord{},
		&persistence.AlertLevelHistory{},
	)
	bd := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	seedReportDB(t, h, bd)

	run := persistence.ForecastRun{
		RunDate: bd, DataEndDate: bd, ModelVersion: "stat-v1", Status: "success",
		MetricsTotal: 1, MetricsDone: 1,
	}
	if err := h.DB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	etaWarn := bd.AddDate(0, 0, 8)
	etaDanger := bd.AddDate(0, 0, 19)
	risk := 0.73
	forecast := persistence.ForecastResult{
		RunID: run.ID, MetricCode: "M_A", HorizonDays: 30,
		ModelType: "linear", Status: "never", ETAWarnDate: &etaWarn,
		ETADangerDate: &etaDanger, RiskScore: &risk, Curve: "[]", InputDigest: "digest-a",
	}
	if err := h.DB.Create(&forecast).Error; err != nil {
		t.Fatal(err)
	}
	// A later horizon must not replace the 30-day summary used by the daily report.
	forecast90 := persistence.ForecastResult{
		RunID: run.ID, MetricCode: "M_A", HorizonDays: 90,
		ModelType: "growth", Status: "insufficient_quality", Curve: "[]", InputDigest: "digest-b",
	}
	if err := h.DB.Create(&forecast90).Error; err != nil {
		t.Fatal(err)
	}

	alert := persistence.AlertRecord{
		AlertNo: "AL-M7-LEVEL-1", MetricCode: "M_A", SystemCode: "SYS_A",
		Level: "p2", AlertType: "threshold", Status: "active", TriggerAt: bd.Add(2 * time.Hour),
		SourceLine: "warn", EscalationCount: 1,
	}
	if err := h.DB.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	history := persistence.AlertLevelHistory{
		AlertID: alert.ID, FromLevel: "p3", ToLevel: "p2", Action: "escalate",
		Reason: "最近7天内有3天超预警线", Evidence: `{"hitDays":3}`,
		Operator: "system", ChangedAt: bd.Add(3 * time.Hour),
	}
	if err := h.DB.Create(&history).Error; err != nil {
		t.Fatal(err)
	}

	daily, err := CollectDaily(h.DB, nil, bd)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var row *DailyRow
	for i := range daily.Rows {
		if daily.Rows[i].MetricCode == "M_A" {
			row = &daily.Rows[i]
			break
		}
	}
	if row == nil {
		t.Fatal("M_A row missing")
	}
	if row.ForecastModel != "linear" || row.ForecastStatus != "never" {
		t.Fatalf("forecast summary = %+v", row)
	}
	if row.ETAWarnDate == nil || !row.ETAWarnDate.Equal(etaWarn) {
		t.Fatalf("etaWarnDate = %v, want %v", row.ETAWarnDate, etaWarn)
	}
	if row.RiskScore == nil || *row.RiskScore != risk {
		t.Fatalf("riskScore = %v, want %v", row.RiskScore, risk)
	}
	if len(daily.LevelChanges) != 1 || daily.LevelChanges[0].FromLevel != "p3" ||
		daily.LevelChanges[0].ToLevel != "p2" {
		t.Fatalf("levelChanges = %+v", daily.LevelChanges)
	}
	if len(daily.LegacyLevelMappings) != 3 {
		t.Fatalf("legacy mappings = %+v", daily.LegacyLevelMappings)
	}
}

func TestGenerateDaily(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.CapacityMetric{}, &persistence.MetricValueDay{},
		&persistence.CollectFlow{}, &persistence.CollectTaskLog{}, &persistence.ReportResult{},
		&persistence.ForecastRun{}, &persistence.ForecastResult{},
		&persistence.AlertRecord{}, &persistence.AlertLevelHistory{})
	bd := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)
	seedReportDB(t, h, bd)

	st, err := store.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := GenerateDaily(context.Background(), h.DB, st, bd)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %s, err = %s", res.Status, res.ErrMsg)
	}
	if !st.Exists(res.FileRef) {
		t.Fatal("xlsx not saved")
	}
	xlsxKey, pdfKey := ReportKeys(bd)
	if xlsxKey != res.FileRef {
		t.Fatalf("fileRef = %s, want %s", res.FileRef, xlsxKey)
	}
	if !st.Exists(pdfKey) {
		t.Fatal("pdf not saved")
	}
	// 幂等重跑: 仍只有一条记录, 状态 success
	res2, err := GenerateDaily(context.Background(), h.DB, st, bd)
	if err != nil {
		t.Fatalf("regen: %v", err)
	}
	if res2.Status != "success" || res2.ID != res.ID {
		t.Fatalf("regen idempotency: id=%d status=%s", res2.ID, res2.Status)
	}
}
