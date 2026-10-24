package flow

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/data"
	"capacity-manage/backend/internal/datasource"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
	"capacity-manage/backend/internal/tradingday"
)

// TestEngine_RunSectionCategoryFlow 端到端: 分类采集 + section 段落聚合
// 分类下两台服务器各自成段 → 跨服务器合并(次数求和/最快取最小/最慢取最大/加权平均),
// 第三台服务器目录无匹配文件 → 单台失败不影响整体(记入 DsFailed)。
func TestEngine_RunSectionCategoryFlow(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CollectFlow{}, &persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.CollectTaskLog{}, &persistence.CleaningTrace{}, &persistence.MetricValueRT{},
		&persistence.AlertRecord{}, &persistence.TradingDayCalendar{}, &persistence.BusinessSystem{}, &persistence.SysParam{})
	cipher, cerr := aesgcm.New("0000000000000000000000000000000000000000000000000000000000000000")
	if cerr != nil {
		t.Fatal(cerr)
	}
	params := system.NewParams(h.DB)
	cal := tradingday.NewCalendar(h.DB, h.RDB, params)
	if err := h.DB.Create(&persistence.BusinessSystem{SystemCode: "S1", Name: "test", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	metric := persistence.CapacityMetric{
		MetricCode: "m_cat", Name: "后台交易请求平均耗时", SystemCode: "S1", MetricType: "absolute",
		Unit: "秒", StatMode: "avg", JudgeDirection: "higher_worse", Status: "active",
	}
	if err := h.DB.Create(&metric).Error; err != nil {
		t.Fatal(err)
	}

	writeServer := func(name, body string) *persistence.DataSource {
		dir := filepath.Join(t.TempDir())
		if body != "" {
			text := "20261016 22:00:00.010 " + sectionStartMarker + "\n" + body + "\n20261016 22:00:00.040 1个服务被调用."
			if err := os.WriteFile(filepath.Join(dir, "a5_run_"+time.Now().Format("20060102")+".txt"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cfg, _ := json.Marshal(map[string]any{
			"type": "log", "scheme": "local", "encoding": "utf-8",
			"globTemplate": filepath.ToSlash(dir) + "/a5_run_*${biz_date:YYYYMMDD}.txt",
		})
		ds := &persistence.DataSource{
			Name: name, Type: "log", Status: "available", CategoryID: int64Ptr(7), Config: string(cfg),
		}
		if err := h.DB.Create(ds).Error; err != nil {
			t.Fatal(err)
		}
		return ds
	}
	writeServer("srv-a", "20261016 22:00:00.020 DT0101 100, 5000.00, 10.00, 90.00, 50.00, X source=[],rule=[]")
	writeServer("srv-b", "20261016 22:00:00.020 DT0102 50, 2500.00, 8.00, 60.00, 50.00, Y source=[],rule=[]")
	ds3 := writeServer("srv-c", "")

	collectCfg, _ := json.Marshal(map[string]any{
		"mode": "section", "startMarker": sectionStartMarker, "endMarker": sectionEndMarker,
		"linePattern": sectionTestPattern, "prefixes": []string{"DT01"}, "output": "avg",
	})
	// 平均耗时单位毫秒 → 指标单位秒: 内置线性换算
	cleanCfg, _ := json.Marshal(map[string]any{
		"transforms": []map[string]any{{"type": "linear", "a": 0.001, "b": 0}},
	})
	fl := &persistence.CollectFlow{
		FlowCode: "f_cat", Name: "section-category-flow", MetricCode: "m_cat",
		DataSourceCategoryID: int64Ptr(7), CollectType: "logparse",
		CollectConfig: string(collectCfg), CleanConfig: string(cleanCfg),
		ScheduleMode: "normal", CronExpr: "0 0 2 * * ?", Status: "active", Enabled: true,
	}
	if err := h.DB.Create(fl).Error; err != nil {
		t.Fatal(err)
	}

	cred := datasource.NewRegistry(h.DB, cipher, nil, slog.Default())
	collectors := NewCollectors(h.DB, cred, nil, params, cal)
	e := NewEngine(h.DB, data.NewStore(h.DB), judge.New(params, nil),
		alert.New(h.DB, params, nil), collectors, cal, params, slog.Default())

	res, err := e.Run(context.Background(), fl.ID, "manual", "test")
	if err != nil {
		t.Fatalf("Run 失败: %v (errMsg=%s)", err, res.ErrMsg)
	}
	if !res.OK || res.Status != "success" {
		t.Fatalf("执行未成功: status=%s errMsg=%s runLog=%s", res.Status, res.ErrMsg, res.RunLog)
	}
	if res.CleanValue == nil {
		t.Fatalf("清洗值为空, runLog=%s", res.RunLog)
	}
	// 跨服务器: (5000+2500)/(100+50) = 50 毫秒 → 0.05 秒
	if math.Abs(*res.CleanValue-0.05) > 1e-9 {
		t.Errorf("采集值 = %v, want 0.05 (跨服务器加权平均 50ms → 0.05s)", *res.CleanValue)
	}
	if len(res.DsOK) != 2 {
		t.Errorf("DsOK = %v, want 2 台成功", res.DsOK)
	}
	if len(res.DsFailed) != 1 || res.DsFailed[0] != ds3.ID {
		t.Errorf("DsFailed = %v, want [%d](无匹配文件服务器)", res.DsFailed, ds3.ID)
	}
	if math.Abs(float64(res.RowsCollected)-2) > 1e-9 {
		t.Errorf("RowsCollected = %d, want 2 (两台各 1 行)", res.RowsCollected)
	}
	t.Logf("runLog: %s", res.RunLog)
}

// TestEngine_RunSectionFlow 端到端: 真实 Engine + 本地日志源(GBK 样例) + section 段落聚合 → 清洗 → 落地
func TestEngine_RunSectionFlow(t *testing.T) {
	sample := "../../../docs/a5_ss_fos_zx_41_run_20261016.txt"
	raw, err := os.ReadFile(sample)
	if err != nil {
		t.Skipf("样例文件不可用: %v", err)
	}
	tmp := t.TempDir()
	// 文件名须含当日 YYYYMMDD(与 ${biz_date:YYYYMMDD} 变量渲染一致)
	today := time.Now().Format("20060102")
	remoteName := filepath.Join(tmp, "a5_ss_fos_zx_41_run_"+today+".txt")
	if err := os.WriteFile(remoteName, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CollectFlow{}, &persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.CollectTaskLog{}, &persistence.CleaningTrace{}, &persistence.MetricValueRT{},
		&persistence.AlertRecord{}, &persistence.TradingDayCalendar{}, &persistence.BusinessSystem{}, &persistence.SysParam{})
	for off := -20; off <= 20; off++ {
		d := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, off)
		wd := d.Weekday()
		isTrading := wd != time.Saturday && wd != time.Sunday
		_ = h.DB.Create(&persistence.TradingDayCalendar{
			Market: "SH", CalDate: d, IsTrading: isTrading,
			DayType: map[bool]string{true: "trading", false: "weekend"}[isTrading], Source: "test",
		})
	}
	cipher, cerr := aesgcm.New("0000000000000000000000000000000000000000000000000000000000000000")
	if cerr != nil {
		t.Fatal(cerr)
	}
	params := system.NewParams(h.DB)
	cal := tradingday.NewCalendar(h.DB, h.RDB, params)

	sys := persistence.BusinessSystem{SystemCode: "S1", Name: "test", Status: "normal"}
	if err := h.DB.Create(&sys).Error; err != nil {
		t.Fatal(err)
	}
	metric := persistence.CapacityMetric{
		MetricCode: "m_sec", Name: "非交易请求平均耗时", SystemCode: "S1", MetricType: "absolute",
		Unit: "毫秒", StatMode: "latest", JudgeDirection: "higher_worse", Status: "active",
	}
	if err := h.DB.Create(&metric).Error; err != nil {
		t.Fatal(err)
	}
	dsCfg, _ := json.Marshal(map[string]any{
		"type": "log", "scheme": "local", "encoding": "gbk",
		"globTemplate": filepath.ToSlash(tmp) + "/*run_*${biz_date:YYYYMMDD}.txt",
	})
	ds := persistence.DataSource{Name: "ds_sec", Type: "log", Status: "available", Config: string(dsCfg)}
	if err := h.DB.Create(&ds).Error; err != nil {
		t.Fatal(err)
	}
	collectCfg, _ := json.Marshal(map[string]any{
		"mode":        "section",
		"startMarker": "功能号,次数,总用时,最快,最慢,平均,名称",
		"endMarker":   "个服务被调用",
		"linePattern": `^\S+ \S+ (?P<func>[A-Za-z]+\d+)\s+(?P<count>\d+),\s*(?P<total>\d+(?:\.\d+)?),\s*(?P<fastest>\d+(?:\.\d+)?),\s*(?P<slowest>\d+(?:\.\d+)?),\s*(?P<avg>\d+(?:\.\d+)?)`,
		"output":      "avg",
	})
	fl := &persistence.CollectFlow{
		FlowCode: "f_sec", Name: "section-flow", MetricCode: "m_sec", DataSourceID: &ds.ID,
		CollectType: "logparse", CollectConfig: string(collectCfg), CleanConfig: "{}",
		ScheduleMode: "normal", CronExpr: "0 0 2 * * ?", Status: "active", Enabled: true,
	}
	if err := h.DB.Create(fl).Error; err != nil {
		t.Fatal(err)
	}

	cred := datasource.NewRegistry(h.DB, cipher, nil, slog.Default())
	collectors := NewCollectors(h.DB, cred, nil, params, cal)
	e := NewEngine(h.DB, data.NewStore(h.DB), judge.New(params, nil),
		alert.New(h.DB, params, nil), collectors, cal, params, slog.Default())

	res, err := e.Run(context.Background(), fl.ID, "manual", "test")
	if err != nil {
		t.Fatalf("Run 失败: %v (errMsg=%s)", err, res.ErrMsg)
	}
	if !res.OK || res.Status != "success" {
		t.Fatalf("执行未成功: status=%s errMsg=%s runLog=%s", res.Status, res.ErrMsg, res.RunLog)
	}
	if res.CleanValue == nil {
		t.Fatalf("清洗值为空, runLog=%s", res.RunLog)
	}
	if math.Abs(*res.CleanValue-103.244606) > 1e-4 {
		t.Errorf("采集值 = %v, want 103.244606 (33 个功能号 Σ总用时/Σ次数)", *res.CleanValue)
	}
	// 落库校验
	var pt persistence.MetricValueRT
	if err := h.DB.Where("metric_code = ? AND clean_value IS NOT NULL", "m_sec").First(&pt).Error; err != nil {
		t.Fatalf("数据未落地: %v", err)
	}
	if math.Abs(*pt.CleanValue-103.244606) > 1e-4 {
		t.Errorf("落库值 = %v, want 103.244606", *pt.CleanValue)
	}
	t.Logf("runLog: %s", res.RunLog)
}
