package flow

import (
	"math"
	"os"
	"regexp"
	"testing"

	"capacity-manage/backend/internal/datasource"
)

func TestFormatSQLValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"decimal as bytes (mysql DECIMAL 返回 []byte)", []byte("520.10"), "520.10"},
		{"string", "950.2", "950.2"},
		{"nil", nil, ""},
		{"int64", int64(32000), "32000"},
	}
	for _, tc := range cases {
		if got := formatSQLValue(tc.in); got != tc.want {
			t.Errorf("%s: formatSQLValue(%v) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// ---------- section 模式(段落提取 + 前缀聚合) ----------

const sectionTestPattern = `^\S+ \S+ (?P<func>[A-Za-z]+\d+)\s+(?P<count>\d+),\s*(?P<total>\d+(?:\.\d+)?),\s*(?P<fastest>\d+(?:\.\d+)?),\s*(?P<slowest>\d+(?:\.\d+)?),\s*(?P<avg>\d+(?:\.\d+)?)`

// 生产口径: 功能号形态不固定(字母码/纯数字码/字母数字混合码/脱敏码), 取第 3 个非空字段
const sectionRealPattern = `^\S+ \S+ (?P<func>\S+)\s+(?P<count>\d+),\s*(?P<total>\d+(?:\.\d+)?),\s*(?P<fastest>\d+(?:\.\d+)?),\s*(?P<slowest>\d+(?:\.\d+)?),\s*(?P<avg>\d+(?:\.\d+)?)`

// TestSection_RealFuncCodeForms 真实日志功能号形态: DT01SHZ1001(委托) / 310005(数字码) / DT06****001(脱敏)
func TestSection_RealFuncCodeForms(t *testing.T) {
	text := `20261019 08:44:52.100 ` + sectionStartMarker + `
20261019 08:44:52.110 DT01SHZ1001 7, 910.82, 1.92, 785.94, 130.12, 上海Z1买入委托 source=[],rule=[]
20261019 08:44:52.120 DT01SZZ6001 3, 300.00, 2.00, 500.00, 100.00, 深圳创转债买入 source=[],rule=[]
20261019 08:44:52.130 310005 10, 500.00, 5.00, 90.00, 50.00, 普通股票买委托 source=[],rule=[]
20261019 08:44:52.140 667721 186598, 3769.68, 0.01, 0.97, 0.02, 盘口行情查询（mdb）. source=[],rule=[]
20261019 08:44:52.150 DM0003 4, 8.00, 1.00, 3.00, 2.00, 委托确认消息 source=[],rule=[]
20261019 08:44:52.160 DT06****001 2, 2.04, 0.69, 1.35, 1.02, 查询股票可买数量 source=[],rule=[]
20261019 08:44:52.900 6个服务被调用.`
	secs, starts := extractSections(text, sectionStartMarker, sectionEndMarker)
	if starts != 1 || len(secs) != 1 {
		t.Fatalf("starts/secs = %d/%d, want 1/1", starts, len(secs))
	}
	re, err := regexp.Compile(sectionRealPattern)
	if err != nil {
		t.Fatalf("pattern 编译失败: %v", err)
	}
	cfg := logConfig{Mode: "section", StartMarker: sectionStartMarker, EndMarker: sectionEndMarker, LinePattern: sectionRealPattern}
	// 交易请求: DT01/DZ02 等委托前缀 → 2 个功能号, 10 次, Σ总用时 1210.82
	cfg.Prefixes = []string{"DT01", "DT02", "GT01", "GT02", "JT01", "JT02"}
	trade, fail, err := parseSectionLines(secs[0], cfg, re)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if fail != 0 || trade.lines != 6 || trade.matched != 2 {
		t.Fatalf("交易: fail/lines/matched = %d/%d/%d, want 0/6/2", fail, trade.lines, trade.matched)
	}
	if trade.callSum != 10 {
		t.Errorf("交易 callSum = %v, want 10", trade.callSum)
	}
	if math.Abs(trade.totalSum-1210.82) > 0.001 {
		t.Errorf("交易 totalSum = %v, want 1210.82", trade.totalSum)
	}
	if trade.slowest != 785.94 || trade.fastest != 1.92 {
		t.Errorf("交易 fastest/slowest = %v/%v, want 1.92/785.94", trade.fastest, trade.slowest)
	}
	// 非交易请求: 其余(数字码/消息码/脱敏码) → 4 个功能号
	cfg.Prefixes, cfg.ExcludePrefixes = nil, []string{"DT01", "DT02", "GT01", "GT02", "JT01", "JT02"}
	other, _, err := parseSectionLines(secs[0], cfg, re)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if other.matched != 4 {
		t.Errorf("非交易 matched = %d, want 4", other.matched)
	}
	if other.callSum != 186614 {
		t.Errorf("非交易 callSum = %v, want 186614", other.callSum)
	}
}

const sectionStartMarker = "功能号,次数,总用时,最快,最慢,平均,名称"
const sectionEndMarker = "个服务被调用"

func newSectionCfg(output string, prefixes, excludePrefixes []string) logConfig {
	return logConfig{
		Mode:            "section",
		StartMarker:     sectionStartMarker,
		EndMarker:       sectionEndMarker,
		LinePattern:     sectionTestPattern,
		Prefixes:        prefixes,
		ExcludePrefixes: excludePrefixes,
		Output:          output,
	}
}

// TestSection_RealSample 用真实 A5 业务统计 run 日志(GBK)做回归基准
func TestSection_RealSample(t *testing.T) {
	path := "../../../docs/a5_ss_fos_zx_41_run_20261016.txt"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("样例文件不可用: %v", err)
	}
	text, err := datasource.DecodeLog(data, "gbk")
	if err != nil {
		t.Fatalf("GBK 解码失败: %v", err)
	}
	secs, starts := extractSections(text, sectionStartMarker, sectionEndMarker)
	if starts != 1 {
		t.Fatalf("段落数 = %d, want 1", starts)
	}
	if len(secs) != 1 {
		t.Fatalf("段落组数 = %d, want 1", len(secs))
	}
	re, err := regexp.Compile(sectionTestPattern)
	if err != nil {
		t.Fatalf("pattern 编译失败: %v", err)
	}
	agg, fail, err := parseSectionLines(secs[0], newSectionCfg("avg", nil, nil), re)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if agg.lines != 33 || fail != 0 || agg.parsed != 33 || agg.matched != 33 {
		t.Fatalf("lines/parsed/matched/fail = %d/%d/%d/%d, want 33/33/33/0", agg.lines, agg.parsed, agg.matched, fail)
	}
	if agg.callSum != 9201 {
		t.Errorf("callSum = %v, want 9201", agg.callSum)
	}
	if math.Abs(agg.totalSum-949953.62) > 0.01 {
		t.Errorf("totalSum = %v, want 949953.62", agg.totalSum)
	}
	if agg.fastest != 0.05 {
		t.Errorf("fastest = %v, want 0.05", agg.fastest)
	}
	if agg.slowest != 152631.21 {
		t.Errorf("slowest = %v, want 152631.21", agg.slowest)
	}
	avg, err := agg.Output("avg")
	if err != nil {
		t.Fatalf("Output(avg) 失败: %v", err)
	}
	if math.Abs(avg-103.244606) > 1e-4 {
		t.Errorf("加权平均 = %v, want 103.244606 (Σ总用时/Σ次数)", avg)
	}
	// 简单平均(错误口径)应明显不同, 防止误用
	if math.Abs((68.49/2+19991.10+0.10+11224.19)/4-avg) < 1 {
		t.Errorf("加权平均疑似退化为简单平均: %v", avg)
	}
}

const sectionSyntheticText = `20261016 22:00:00.000 业务统计 demo 20261015 21:00:00.000 - 20261016 22:00:00.000 (单位:毫秒).
20261016 22:00:00.010 功能号,次数,总用时,最快,最慢,平均,名称
20261016 22:00:00.020 DT0101 100, 5000.00, 10.00, 90.00, 50.00, 交易下单 source=[],rule=[]
20261016 22:00:00.030 DT0102 100, 3000.00, 5.00, 55.00, 30.00, 交易委托 source=[],rule=[]
20261016 22:00:00.040 DT0201 50, 2500.00, 4.00, 80.00, 50.00, 撤单 source=[],rule=[]
20261016 22:00:00.050 GT0101 10, 1000.00, 10.00, 200.00, 100.00, 两融交易 source=[],rule=[]
20261016 22:00:00.060 JT0201 5, 150.00, 2.00, 30.00, 30.00, 期权撤单 source=[],rule=[]
20261016 22:00:00.070 S00001 10, 100.00, 1.00, 20.00, 10.00, 系统状态 source=[],rule=[]
20261016 22:00:00.080 7个服务被调用.
20261016 22:00:00.090 service stopped.`

func parseSynthetic(t *testing.T, cfg logConfig) *sectionAgg {
	t.Helper()
	secs, starts := extractSections(sectionSyntheticText, sectionStartMarker, sectionEndMarker)
	if starts != 1 || len(secs) != 1 {
		t.Fatalf("段落提取异常: starts=%d secs=%d", starts, len(secs))
	}
	re, err := regexp.Compile(cfg.LinePattern)
	if err != nil {
		t.Fatalf("pattern 编译失败: %v", err)
	}
	agg, fail, err := parseSectionLines(secs[0], cfg, re)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if fail != 0 {
		t.Fatalf("存在解析失败行: %d", fail)
	}
	return agg
}

// TestSection_PrefixCategories 前缀归类规则: 包含前缀 + 排除前缀("其他"类)
func TestSection_PrefixCategories(t *testing.T) {
	cases := []struct {
		name     string
		prefixes []string
		exclude  []string
		want     sectionAgg
	}{
		{"交易请求 DT01", []string{"DT01"}, nil, sectionAgg{matched: 2, callSum: 200, totalSum: 8000, fastest: 5, slowest: 90}},
		{"撤单请求 DT02", []string{"DT02"}, nil, sectionAgg{matched: 1, callSum: 50, totalSum: 2500, fastest: 4, slowest: 80}},
		{"两融交易 GT01", []string{"GT01"}, nil, sectionAgg{matched: 1, callSum: 10, totalSum: 1000, fastest: 10, slowest: 200}},
		{"期权撤单 JT02", []string{"JT02"}, nil, sectionAgg{matched: 1, callSum: 5, totalSum: 150, fastest: 2, slowest: 30}},
		{"非交易(排除6前缀)", nil, []string{"DT01", "DT02", "GT01", "GT02", "JT01", "JT02"}, sectionAgg{matched: 1, callSum: 10, totalSum: 100, fastest: 1, slowest: 20}},
		{"全部(无过滤)", nil, nil, sectionAgg{matched: 6, callSum: 275, totalSum: 11750, fastest: 1, slowest: 200}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agg := parseSynthetic(t, newSectionCfg("avg", tc.prefixes, tc.exclude))
			if agg.matched != tc.want.matched {
				t.Errorf("matched = %d, want %d", agg.matched, tc.want.matched)
			}
			if agg.callSum != tc.want.callSum {
				t.Errorf("callSum = %v, want %v", agg.callSum, tc.want.callSum)
			}
			if agg.totalSum != tc.want.totalSum {
				t.Errorf("totalSum = %v, want %v", agg.totalSum, tc.want.totalSum)
			}
			if agg.fastest != tc.want.fastest {
				t.Errorf("fastest = %v, want %v", agg.fastest, tc.want.fastest)
			}
			if agg.slowest != tc.want.slowest {
				t.Errorf("slowest = %v, want %v", agg.slowest, tc.want.slowest)
			}
		})
	}
}

// TestSection_WeightedAvg 平均耗时 = Σ总用时/Σ次数(按调用次数加权)
func TestSection_WeightedAvg(t *testing.T) {
	agg := parseSynthetic(t, newSectionCfg("avg", []string{"DT01"}, nil))
	avg, err := agg.Output("avg")
	if err != nil {
		t.Fatalf("Output(avg) 失败: %v", err)
	}
	// (5000+3000)/(100+100) = 40; 若误用简单平均 (50+30)/2 = 40 巧合相同, 再验证次数悬殊场景
	if math.Abs(avg-40) > 1e-9 {
		t.Errorf("DT01 加权平均 = %v, want 40", avg)
	}

	// 次数悬殊: 1 次 100ms + 99 次 10ms → 加权 (100+990)/100 = 10.9, 简单平均 ≈ 9.9
	text := `20261016 22:00:00.010 功能号,次数,总用时,最快,最慢,平均,名称
20261016 22:00:00.020 DT0101 1, 100.00, 100.00, 100.00, 100.00, A source=[],rule=[]
20261016 22:00:00.030 DT0102 99, 990.00, 10.00, 10.00, 10.00, B source=[],rule=[]
20261016 22:00:00.040 2个服务被调用.`
	secs, _ := extractSections(text, sectionStartMarker, sectionEndMarker)
	re, _ := regexp.Compile(sectionTestPattern)
	a2, _, err := parseSectionLines(secs[0], newSectionCfg("avg", []string{"DT01"}, nil), re)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	avg2, _ := a2.Output("avg")
	if math.Abs(avg2-10.9) > 1e-9 {
		t.Errorf("悬殊场景加权平均 = %v, want 10.9 (简单平均 9.99 会错)", avg2)
	}
}

// TestSection_MultiFileMerge 跨文件合并聚合
func TestSection_MultiFileMerge(t *testing.T) {
	re, _ := regexp.Compile(sectionTestPattern)
	cfg := newSectionCfg("avg", []string{"DT01"}, nil)
	agg := &sectionAgg{}
	for i, body := range []string{
		"20261016 22:00:00.020 DT0101 100, 5000.00, 10.00, 90.00, 50.00, X source=[],rule=[]",
		"20261016 22:00:00.020 DT0102 50, 2500.00, 8.00, 60.00, 50.00, Y source=[],rule=[]",
	} {
		text := "20261016 22:00:00.010 功能号,次数,总用时,最快,最慢,平均,名称\n" + body + "\n20261016 22:00:00.040 1个服务被调用."
		secs, _ := extractSections(text, sectionStartMarker, sectionEndMarker)
		a, _, err := parseSectionLines(secs[0], cfg, re)
		if err != nil {
			t.Fatalf("文件 %d 解析失败: %v", i, err)
		}
		agg.Merge(a)
	}
	if agg.matched != 2 || agg.callSum != 150 || agg.totalSum != 7500 {
		t.Fatalf("跨文件聚合 = matched %d callSum %v totalSum %v, want 2/150/7500", agg.matched, agg.callSum, agg.totalSum)
	}
	if agg.fastest != 8 || agg.slowest != 90 {
		t.Errorf("跨文件 fastest/slowest = %v/%v, want 8/90", agg.fastest, agg.slowest)
	}
	avg, _ := agg.Output("avg")
	if math.Abs(avg-50) > 1e-9 {
		t.Errorf("跨文件加权平均 = %v, want 50", avg)
	}
}

// TestSection_Errors 未找到段落 / 无匹配功能号
func TestSection_Errors(t *testing.T) {
	secs, starts := extractSections("没有任何统计段落", sectionStartMarker, sectionEndMarker)
	if starts != 0 || len(secs) != 0 {
		t.Fatalf("starts/secs = %d/%d, want 0/0", starts, len(secs))
	}
	re, _ := regexp.Compile(sectionTestPattern)
	agg, _, _ := parseSectionLines([]string{}, newSectionCfg("avg", []string{"DT01"}, nil), re)
	if _, err := agg.Output("avg"); err == nil {
		t.Errorf("空聚合 Output 应报错")
	}
	// 有功能号但前缀不命中 → matched=0
	secs2, _ := extractSections(sectionSyntheticText, sectionStartMarker, sectionEndMarker)
	a2, _, _ := parseSectionLines(secs2[0], newSectionCfg("avg", []string{"ZZ99"}, nil), re)
	if a2.matched != 0 {
		t.Fatalf("前缀 ZZ99 应无命中, got %d", a2.matched)
	}
	if _, err := a2.Output("count"); err == nil {
		t.Errorf("无匹配功能号 Output 应报错")
	}
	if _, err := agg.Output("bogus"); err == nil {
		t.Errorf("未知 output 应报错")
	}
}

// TestDecodeLog GBK/GB18030/UTF-8 解码与非法编码
func TestDecodeLog(t *testing.T) {
	gbkBytes := []byte{0xB9, 0xA6, 0xC4, 0xDC} // "功能" 的 GBK 编码
	s, err := datasource.DecodeLog(gbkBytes, "gbk")
	if err != nil {
		t.Fatalf("GBK 解码失败: %v", err)
	}
	if s != "功能" {
		t.Errorf("GBK 解码 = %q, want 功能", s)
	}
	s2, err := datasource.DecodeLog([]byte("plain"), "")
	if err != nil || s2 != "plain" {
		t.Errorf("默认 utf-8 解码异常: %q %v", s2, err)
	}
	if _, err := datasource.DecodeLog([]byte("x"), "ascii"); err == nil {
		t.Errorf("非法编码应报错")
	}
}
