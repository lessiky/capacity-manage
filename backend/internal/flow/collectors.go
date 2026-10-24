package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/datasource"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pyclient"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	gmysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// CollectMeta 采集元信息（行数/解析失败/执行日志）
type CollectMeta struct {
	RowsTotal      int
	ParseFailCount int
	RunLog         string
	DsOK           []int64 // 读取成功的数据源 ID（分类采集按数据源粒度记录）
	DsFailed       []int64 // 读取失败的数据源 ID
	Glob           string  // 本次实际使用的文件匹配模式(流程 fileGlob > 数据源 globTemplate > pathTemplate)
	Files          []string // 实际读取到的文件路径（可追溯读了哪些日志文件）
}

// Collectors 三类采集方式分发（sql/logparse/python）
type Collectors struct {
	db     *gorm.DB
	cred   *datasource.Registry
	py     *pyclient.Client
	params *system.Params
	cal    *tradingday.Calendar
	remote *RemoteRunner
}

func NewCollectors(db *gorm.DB, cred *datasource.Registry, py *pyclient.Client, params *system.Params, cal *tradingday.Calendar) *Collectors {
	return &Collectors{db: db, cred: cred, py: py, params: params, cal: cal, remote: NewSSHRunner(nil)}
}

type sqlConfig struct {
	SQLTemplate string   `json:"sqlTemplate"`
	Aggregate   string   `json:"aggregate"` // single/sum/avg/max/min
	ValueColumn string   `json:"valueColumn"`
}

type logConfig struct {
	Mode       string         `json:"mode"` // regex/delimited/json
	Pattern    string         `json:"pattern"`
	Delimiters string         `json:"delimiters"`
	Fields     map[string]int `json:"fields"`
	ValueField string         `json:"valueField"`
	UnitField  string         `json:"unitField"`
	FileGlob   string         `json:"fileGlob"`

	// section 模式: 提取起止标记间段落, 逐行解析功能号统计并按前缀过滤聚合, 输出单一汇总值
	StartMarker     string   `json:"startMarker"`     // 段落起始标记(行内含即命中)
	EndMarker       string   `json:"endMarker"`       // 段落结束标记(行内含即命中, 空=到文件尾)
	LinePattern     string   `json:"linePattern"`     // 行正则(命名分组)
	IDField         string   `json:"idField"`         // 功能号字段, 默认 func
	CountField      string   `json:"countField"`      // 次数字段, 默认 count
	TotalField      string   `json:"totalField"`      // 总用时字段, 默认 total
	MinField        string   `json:"minField"`        // 最快字段, 默认 fastest
	MaxField        string   `json:"maxField"`        // 最慢字段, 默认 slowest
	AvgField        string   `json:"avgField"`        // 平均字段, 默认 avg
	Prefixes        []string `json:"prefixes"`        // 仅统计这些前缀的功能号(空=全部)
	ExcludePrefixes []string `json:"excludePrefixes"` // 排除这些前缀的功能号(用于"其他"类)
	Output          string   `json:"output"`          // count/total/fastest/slowest/avg(avg=按次数加权)
}

type pyConfig struct {
	ScriptID int64 `json:"scriptId"`
	Version  int   `json:"version"`
}

// scriptConfig 远程脚本采集(collectType=script): 类型 ssh 的执行目标上跑 python/bash/powershell
type scriptConfig struct {
	ScriptID int64             `json:"scriptId"`
	Version  int               `json:"version"`
	Params   map[string]string `json:"params"`
}

// Collect 执行采集 → 原始行（base 为本次运行业务基准时刻, 供交易日变量推导）
// dsList 为流程归属的数据源集合: 单数据源流程为 1 个; 分类采集流程为分类下全部日志数据源(共用同一处理逻辑)
func (c *Collectors) Collect(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource, metric *persistence.CapacityMetric, vars map[string]string, base time.Time) ([]RawRow, *CollectMeta, error) {
	// python 采集在平台沙箱执行, 不读数据源 → 允许流程未绑定数据源(FR-COL-17)
	if fl.CollectType == "python" {
		return c.collectPython(ctx, fl, metric, vars)
	}
	if len(dsList) == 0 {
		return nil, nil, fmt.Errorf("流程未关联可用数据源")
	}
	switch fl.CollectType {
	case "sql":
		return c.collectSQL(ctx, fl, dsList[0], vars, base)
	case "logparse":
		return c.collectLog(ctx, fl, dsList, vars, base)
	case "script":
		return c.collectScript(ctx, fl, dsList[0], metric, vars)
	default:
		return nil, nil, fmt.Errorf("未知采集方式: %s", fl.CollectType)
	}
}

// formatSQLValue SQL 结果值字符串化: DECIMAL/NUMERIC 等驱动返回 []byte, fmt.Sprint 会打成字节列表
func formatSQLValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func (c *Collectors) collectSQL(ctx context.Context, fl *persistence.CollectFlow, ds *persistence.DataSource, vars map[string]string, base time.Time) ([]RawRow, *CollectMeta, error) {
	var cfg sqlConfig
	if err := json.Unmarshal([]byte(fl.CollectConfig), &cfg); err != nil {
		return nil, nil, fmt.Errorf("sql 配置解析失败: %w", err)
	}
	if cfg.SQLTemplate == "" {
		return nil, nil, fmt.Errorf("sqlTemplate 为空")
	}
	sqlText, rerr := c.renderSQL(ctx, fl, cfg.SQLTemplate, vars, base)
	if rerr != nil {
		return nil, nil, rerr
	}
	var dbCfg datasource.Config
	if err := json.Unmarshal([]byte(ds.Config), &dbCfg); err != nil {
		return nil, nil, fmt.Errorf("数据源配置解析失败: %w", err)
	}
	if err := c.cred.DecryptForCollect(&dbCfg); err != nil {
		return nil, nil, err
	}
	var (
		conn *sql.DB
		err  error
	)
	switch dbCfg.Driver {
	case "mysql":
		// Config + NewConnector 直建, 凭据含 @/#/? 等特殊字符不影响解析
		conf := gmysql.NewConfig()
		conf.User = dbCfg.Username
		conf.Passwd = dbCfg.Password
		conf.Net = "tcp"
		conf.Addr = fmt.Sprintf("%s:%d", dbCfg.Host, dbCfg.Port)
		conf.DBName = dbCfg.Database
		conf.Timeout = 30 * time.Second
		conf.InterpolateParams = true
		connector, perr := gmysql.NewConnector(conf)
		if perr != nil {
			return nil, nil, fmt.Errorf("DSN 构建失败: %w", perr)
		}
		conn = sql.OpenDB(connector)
	case "oracle":
		service := dbCfg.Service
		if service == "" {
			service = dbCfg.Database
		}
		if service == "" {
			return nil, nil, fmt.Errorf("Oracle 数据源须配置 service 或 database")
		}
		// URL 形态: 用户名/密码自动 percent 编码, 特殊字符不破坏 DSN
		oracleURL := &url.URL{
			Scheme: "oracle",
			User:   url.UserPassword(dbCfg.Username, dbCfg.Password),
			Host:   fmt.Sprintf("%s:%d", dbCfg.Host, dbCfg.Port),
			Path:   "/" + service,
		}
		conn, err = sql.Open("oracle", oracleURL.String())
	default:
		return nil, nil, fmt.Errorf("不支持的驱动: %s", dbCfg.Driver)
	}
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	qctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rows, err := conn.QueryContext(qctx, sqlText)
	if err != nil {
		return nil, nil, fmt.Errorf("SQL 执行失败: %w", err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	meta := &CollectMeta{}
	out := make([]RawRow, 0, 8)
	valIdx := 0
	if cfg.ValueColumn != "" {
		for i, cn := range cols {
			if strings.EqualFold(cn, cfg.ValueColumn) {
				valIdx = i
				break
			}
		}
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			meta.ParseFailCount++
			continue
		}
		meta.RowsTotal++
		out = append(out, RawRow{Raw: formatSQLValue(vals[valIdx])})
	}
	if len(out) == 0 {
		return out, meta, fmt.Errorf("SQL 无结果行")
	}
	return out, meta, nil
}

func (c *Collectors) collectLog(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource, vars map[string]string, base time.Time) ([]RawRow, *CollectMeta, error) {
	var cfg logConfig
	if err := json.Unmarshal([]byte(fl.CollectConfig), &cfg); err != nil {
		return nil, nil, fmt.Errorf("log 配置解析失败: %w", err)
	}
	if len(dsList) > 1 {
		return c.collectLogCategory(ctx, fl, dsList, vars, base, cfg)
	}
	return c.collectLogOne(ctx, fl, dsList[0], vars, base, cfg)
}

// collectLogOne 单数据源日志采集（含 section 段落聚合模式）
func (c *Collectors) collectLogOne(ctx context.Context, fl *persistence.CollectFlow, ds *persistence.DataSource, vars map[string]string, base time.Time, cfg logConfig) ([]RawRow, *CollectMeta, error) {
	if cfg.Mode == "section" {
		return c.collectSection(ctx, fl, ds, vars, base, cfg)
	}
	var dsCfg datasource.Config
	if err := json.Unmarshal([]byte(ds.Config), &dsCfg); err != nil {
		return nil, nil, fmt.Errorf("数据源配置解析失败: %w", err)
	}
	if err := c.cred.DecryptForCollect(&dsCfg); err != nil {
		return nil, nil, err
	}
	pattern := cfg.FileGlob
	if pattern == "" {
		pattern = dsCfg.GlobTemplate
	}
	if pattern == "" {
		pattern = dsCfg.PathTemplate
	}
	pattern, err := c.renderPattern(ctx, fl, pattern, vars, base)
	if err != nil {
		return nil, nil, err
	}
	data, matches, err := datasource.ReadLogFile(ctx, &dsCfg, pattern)
	if err != nil {
		return nil, &CollectMeta{Glob: pattern}, err
	}
	meta := &CollectMeta{Glob: pattern, Files: matches}
	out := make([]RawRow, 0, 16)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		meta.RowsTotal++
		val, unit, ok := parseLine(line, cfg)
		if !ok {
			meta.ParseFailCount++
			continue
		}
		out = append(out, RawRow{Raw: val, Unit: unit})
	}
	meta.RunLog = fmt.Sprintf("glob=%s files=%d(%s)", pattern, len(matches), fileSummary(matches))
	if len(out) == 0 {
		return out, meta, fmt.Errorf("日志无有效行(匹配 0 行, 解析失败 %d 行)", meta.ParseFailCount)
	}
	return out, meta, nil
}

// collectLogCategory 分类采集: 同一分类下全部日志数据源的文件使用同一处理逻辑(同一采集/清洗配置),
// 原始行合并后按指标 statMode 统一聚合。单数据源读取失败不中断整体, 失败明细记录 RunLog;
// 全部失败或均无有效行才判定采集失败。section 段落聚合模式在分类采集时跨服务器合并聚合。
func (c *Collectors) collectLogCategory(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource, vars map[string]string, base time.Time, cfg logConfig) ([]RawRow, *CollectMeta, error) {
	if cfg.Mode == "section" {
		return c.collectSectionCategory(ctx, fl, dsList, vars, base, cfg)
	}
	meta := &CollectMeta{}
	var out []RawRow
	notes := make([]string, 0, len(dsList))
	for _, ds := range dsList {
		rows, m, err := c.collectLogOne(ctx, fl, ds, vars, base, cfg)
		if err != nil {
			meta.DsFailed = append(meta.DsFailed, ds.ID)
			note := fmt.Sprintf("[%s] 读取失败: %v", ds.Name, err)
			if g := metaGlob(m); g != "" {
				note += " (glob=" + g + ")"
			}
			notes = append(notes, note)
			continue
		}
		meta.DsOK = append(meta.DsOK, ds.ID)
		meta.RowsTotal += m.RowsTotal
		meta.ParseFailCount += m.ParseFailCount
		out = append(out, rows...)
		notes = append(notes, fmt.Sprintf("[%s] glob=%s files=%d(%s) 有效 %d 行, 解析失败 %d 行",
			ds.Name, m.Glob, len(m.Files), fileSummary(m.Files), len(rows), m.ParseFailCount))
		if meta.Glob == "" {
			meta.Glob = m.Glob
		}
		meta.Files = append(meta.Files, m.Files...)
	}
	meta.RunLog = strings.Join(notes, "\n")
	if len(meta.DsOK) == 0 {
		return nil, meta, fmt.Errorf("分类下全部数据源读取失败: %s", meta.RunLog)
	}
	if len(out) == 0 {
		return out, meta, fmt.Errorf("分类下日志无有效行: %s", meta.RunLog)
	}
	return out, meta, nil
}

func (c *Collectors) collectPython(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric, vars map[string]string) ([]RawRow, *CollectMeta, error) {
	var cfg pyConfig
	if err := json.Unmarshal([]byte(fl.CollectConfig), &cfg); err != nil {
		return nil, &CollectMeta{}, fmt.Errorf("python 配置解析失败: %w", err)
	}
	scriptContent, err := c.loadScriptContent(cfg.ScriptID, cfg.Version)
	if err != nil {
		return nil, &CollectMeta{}, err
	}
	res, err := c.py.Execute(ctx, pyclient.ExecuteReq{
		TaskID:        fmt.Sprintf("flow-%d", fl.ID),
		ScriptType:    "collect",
		ScriptID:      cfg.ScriptID,
		Version:       cfg.Version,
		ScriptContent: scriptContent,
		Params:        vars,
		TimeoutMs:     int64(c.params.Int("flow_timeout_seconds", 300)) * 1000,
	})
	if err != nil {
		return nil, &CollectMeta{}, fmt.Errorf("沙箱执行失败: %w", err)
	}
	// FR-COL-19: 执行日志与异常堆栈全量记录
	meta := &CollectMeta{RunLog: buildRunLog(res)}
	if res.Status != "success" {
		return nil, meta, fmt.Errorf("脚本执行 %s: %s", res.Status, res.ErrorMsg())
	}
	out, total, perr := filterScriptItems(res.Data, metric)
	if perr != nil {
		return nil, meta, perr
	}
	meta.RowsTotal = total
	return out, meta, nil
}

type scriptOutItem struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
}

// filterScriptItems 脚本出参 → 原始行; 仅保留本流程指标(metric 留空视为通用), 返回总条数
func filterScriptItems(data string, metric *persistence.CapacityMetric) ([]RawRow, int, error) {
	var items []scriptOutItem
	if err := json.Unmarshal([]byte(data), &items); err != nil {
		return nil, 0, fmt.Errorf("脚本出参解析失败: %w", err)
	}
	out := make([]RawRow, 0, len(items))
	for _, it := range items {
		if it.Metric != "" && it.Metric != metric.MetricCode && it.Metric != metric.Name {
			continue
		}
		out = append(out, RawRow{Raw: fmt.Sprintf("%v", it.Value), Unit: it.Unit})
	}
	return out, len(items), nil
}

// collectScript 远程脚本采集(FR-COL-17 扩展): 脚本与参数 SFTP 落目标机临时目录执行, 执行后删除。
// 与本地沙箱(collectType=python)的区别: 在目标机上真实执行, 可用 python/bash/powershell。
func (c *Collectors) collectScript(ctx context.Context, fl *persistence.CollectFlow, ds *persistence.DataSource,
	metric *persistence.CapacityMetric, vars map[string]string) ([]RawRow, *CollectMeta, error) {
	var cfg scriptConfig
	if err := json.Unmarshal([]byte(fl.CollectConfig), &cfg); err != nil {
		return nil, &CollectMeta{}, fmt.Errorf("脚本配置解析失败: %w", err)
	}
	if cfg.ScriptID == 0 {
		return nil, &CollectMeta{}, fmt.Errorf("脚本采集未指定脚本")
	}
	if ds == nil || ds.Type != "ssh" {
		got := ""
		if ds != nil {
			got = ds.Type
		}
		return nil, &CollectMeta{}, fmt.Errorf("脚本采集须绑定执行目标(数据源类型 ssh), 当前为 %s", got)
	}
	var dsc datasource.Config
	if err := json.Unmarshal([]byte(ds.Config), &dsc); err != nil {
		return nil, &CollectMeta{}, fmt.Errorf("执行目标配置解析失败: %w", err)
	}
	if err := c.cred.DecryptForCollect(&dsc); err != nil {
		return nil, &CollectMeta{}, err
	}
	content, lang, err := c.loadScript(cfg.ScriptID, cfg.Version)
	if err != nil {
		return nil, &CollectMeta{}, err
	}
	if c.remote == nil {
		return nil, &CollectMeta{}, fmt.Errorf("远程执行器未初始化")
	}
	target := RemoteTarget{
		OS: dsc.OS, Host: dsc.Host, Port: dsc.Port,
		Username: dsc.Username, Password: dsc.Password, TempDir: dsc.TempDir,
	}
	params := MergeScriptParams(vars, cfg.Params)
	timeout := time.Duration(c.params.Int("flow_timeout_seconds", 300)) * time.Second
	taskID := fmt.Sprintf("flow-%d-%d", fl.ID, time.Now().UnixNano())
	res, err := c.remote.Run(ctx, target, ScriptSpec{Language: lang, Content: content}, params, taskID, timeout)
	if err != nil {
		return nil, &CollectMeta{}, fmt.Errorf("远程执行失败: %w", err)
	}
	meta := &CollectMeta{RunLog: buildRemoteRunLog(res, dsc)}
	if res.Status != "success" {
		return nil, meta, fmt.Errorf("脚本执行 %s: %s", res.Status, trunc(res.Stderr, 300))
	}
	out, total, perr := filterScriptItems(res.Data, metric)
	if perr != nil {
		return nil, meta, perr
	}
	meta.RowsTotal = total
	return out, meta, nil
}

// buildRemoteRunLog 远程执行留痕(FR-COL-19): 状态/耗时/目标/输出, 不含凭据
func buildRemoteRunLog(res *ScriptRunResult, dsc datasource.Config) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[remote] status=%s exitCode=%d durationMs=%d target=%s(%s:%d) dir=%s",
		res.Status, res.ExitCode, res.DurationMS, dsc.OS, dsc.Host, dsc.Port, res.Dir)
	b.WriteString("\n")
	if res.Stderr != "" {
		b.WriteString("--- stderr ---\n" + res.Stderr + "\n")
	}
	if res.Stdout != "" {
		b.WriteString("--- stdout ---\n" + res.Stdout + "\n")
	}
	str := b.String()
	if len(str) > 64000 {
		return str[:64000]
	}
	return str
}

// loadScriptContent 按版本号(或生效版本)加载脚本内容
func (c *Collectors) loadScriptContent(scriptID int64, version int) (string, error) {
	sv, err := c.pickScriptVersion(scriptID, version)
	if err != nil {
		return "", err
	}
	return sv.Content, nil
}

// loadScript 加载脚本内容与语言(远程执行需要按语言选运行器)
func (c *Collectors) loadScript(scriptID int64, version int) (string, string, error) {
	sv, err := c.pickScriptVersion(scriptID, version)
	if err != nil {
		return "", "", err
	}
	var s persistence.PythonScript
	if err := c.db.First(&s, scriptID).Error; err != nil {
		return "", "", fmt.Errorf("脚本不存在: scriptId=%d", scriptID)
	}
	lang := s.Language
	if lang == "" {
		lang = "python"
	}
	return sv.Content, lang, nil
}

// pickScriptVersion 指定版本号优先, 否则取生效版本
func (c *Collectors) pickScriptVersion(scriptID int64, version int) (*persistence.PythonScriptVersion, error) {
	if version > 0 {
		var sv persistence.PythonScriptVersion
		if err := c.db.Where("script_id = ? AND version = ?", scriptID, version).First(&sv).Error; err == nil {
			return &sv, nil
		}
	}
	var sv persistence.PythonScriptVersion
	if err := c.db.Where("script_id = ? AND active = ?", scriptID, true).First(&sv).Error; err == nil {
		return &sv, nil
	}
	return nil, fmt.Errorf("脚本不存在或无生效版本: scriptId=%d", scriptID)
}

// buildRunLog 生成执行日志文本（状态/资源/堆栈, FR-COL-19）
func buildRunLog(res *pyclient.ExecuteResp) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[worker] status=%s exitCode=%d durationMs=%d", res.Status, res.ExitCode, res.DurationMS)
	if res.ErrType != "" {
		fmt.Fprintf(&b, " errorType=%s", res.ErrType)
	}
	b.WriteString("\n")
	if res.Stderr != "" {
		b.WriteString("--- stderr ---\n" + res.Stderr + "\n")
	}
	if res.Stdout != "" {
		b.WriteString("--- stdout ---\n" + res.Stdout + "\n")
	}
	str := b.String()
	if len(str) > 64000 {
		return str[:64000]
	}
	return str
}

// scriptCleanItem 脚本清洗输入/输出同构元素（契约 §1.1: clean 类型）
type scriptCleanItem struct {
	Raw   string   `json:"raw,omitempty"`
	Value *float64 `json:"value"`
	Unit  string   `json:"unit,omitempty"`
}

// CleanByScript Python 脚本清洗（FR-COL-18）: 输入内置类型转换后的行, 输出同构数组
func (c *Collectors) CleanByScript(ctx context.Context, fl *persistence.CollectFlow, metric *persistence.CapacityMetric,
	rows []CleanRow, ref ScriptRef, vars map[string]string) ([]CleanRow, error) {
	scriptContent, err := c.loadScriptContent(ref.ScriptID, ref.Version)
	if err != nil {
		return rows, err
	}
	in := make([]scriptCleanItem, 0, len(rows))
	for _, r := range rows {
		in = append(in, scriptCleanItem{Raw: r.Raw, Value: r.Value, Unit: r.Unit})
	}
	inJSON, _ := json.Marshal(in)
	res, err := c.py.Execute(ctx, pyclient.ExecuteReq{
		TaskID:        fmt.Sprintf("flow-%d-clean", fl.ID),
		ScriptType:    "clean",
		ScriptID:      ref.ScriptID,
		Version:       ref.Version,
		ScriptContent: scriptContent,
		Params:        vars,
		TimeoutMs:     int64(c.params.Int("flow_timeout_seconds", 300)) * 1000,
		Input:         string(inJSON),
	})
	if err != nil {
		return rows, fmt.Errorf("沙箱执行失败: %w", err)
	}
	if res.Status != "success" {
		return rows, fmt.Errorf("清洗脚本执行 %s: %s", res.Status, res.ErrorMsg())
	}
	var out []scriptCleanItem
	if err := json.Unmarshal([]byte(res.Data), &out); err != nil {
		return rows, fmt.Errorf("清洗脚本出参解析失败: %w", err)
	}
	for i := range rows {
		if i < len(out) {
			rows[i].Value = out[i].Value
			if out[i].Unit != "" {
				rows[i].Unit = out[i].Unit
			}
			rows[i].Rules = append(rows[i].Rules, "script:clean")
		}
	}
	_ = metric
	return rows, nil
}

func parseLine(line string, cfg logConfig) (string, string, bool) {
	switch cfg.Mode {
	case "regex":
		re, err := regexp.Compile(cfg.Pattern)
		if err != nil {
			return "", "", false
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			return "", "", false
		}
		names := re.SubexpNames()
		val, unit := "", ""
		for i, n := range names {
			if i == 0 || n == "" {
				continue
			}
			if n == cfg.ValueField {
				val = m[i]
			}
			if cfg.UnitField != "" && n == cfg.UnitField {
				unit = m[i]
			}
		}
		if val == "" && len(m) > 1 {
			val = m[1]
		}
		if val == "" {
			return "", "", false
		}
		return val, unit, true
	case "json":
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return "", "", false
		}
		v, ok := obj[cfg.ValueField]
		if !ok {
			return "", "", false
		}
		unit := ""
		if cfg.UnitField != "" {
			if u, ok2 := obj[cfg.UnitField]; ok2 {
				unit = fmt.Sprint(u)
			}
		}
		return fmt.Sprint(v), unit, true
	case "delimited":
		delim := cfg.Delimiters
		if delim == "" {
			delim = ","
		}
		parts := strings.Split(line, delim)
		idx, ok := cfg.Fields[cfg.ValueField]
		if !ok || idx >= len(parts) {
			return "", "", false
		}
		unit := ""
		if cfg.UnitField != "" {
			if uidx, ok2 := cfg.Fields[cfg.UnitField]; ok2 && uidx < len(parts) {
				unit = parts[uidx]
			}
		}
		return strings.TrimSpace(parts[idx]), unit, true
	}
	return "", "", false
}

// renderSQL SQL 模板变量替换（单引号转义防注入 + 交易日偏移推导）
func (c *Collectors) renderSQL(ctx context.Context, fl *persistence.CollectFlow, template string, vars map[string]string, base time.Time) (string, error) {
	return RenderTemplate(template, vars, escapeSQLValue, c.offsetFn(ctx, fl, base))
}

// renderPattern 日志 glob 变量替换（不转义; 支持 trading_day_offset）
func (c *Collectors) renderPattern(ctx context.Context, fl *persistence.CollectFlow, pattern string, vars map[string]string, base time.Time) (string, error) {
	return RenderTemplate(pattern, vars, nil, c.offsetFn(ctx, fl, base))
}

// offsetFn 构造交易日偏移变量函数（按流程市场; cal 为 nil 时返回 nil 保持旧宽容行为）
func (c *Collectors) offsetFn(ctx context.Context, fl *persistence.CollectFlow, base time.Time) OffsetFn {
	if c.cal == nil {
		return nil
	}
	market := fl.TradingMarket
	if market == "" {
		market = "SH"
	}
	return func(n int, format string) (string, bool) {
		td, err := c.cal.Offset(ctx, market, base, n)
		if err != nil {
			return "", false
		}
		s := td.Format("2006-01-02")
		if format != "" {
			s, err = FormatDateValue(s, format)
			if err != nil {
				return "", false
			}
		}
		return s, true
	}
}
func escapeSQLValue(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// ---------- section 模式: 段落提取 + 多行聚合(如 A5 业务统计 run 日志) ----------

// funcStat 单行功能号统计
type funcStat struct {
	ID       string
	Count    float64
	Total    float64 // 总用时; 日志无该字段时 HasTotal=false
	HasTotal bool
	Fastest  float64
	Slowest  float64
	Avg      float64
}

// sectionAgg 段落内(跨行/跨文件)聚合结果
type sectionAgg struct {
	lines     int     // 段落行数(不含起止标记)
	parsed    int     // 成功解析行数
	matched   int     // 前缀过滤后功能号数
	callSum   float64 // Σ调用次数
	totalSum  float64 // Σ总用时
	avgSum    float64 // Σ(平均×次数), 总用时缺失时的加权回退
	hasTotal  bool
	hasMinMax bool
	fastest   float64
	slowest   float64
}

func (a *sectionAgg) Add(fs funcStat) {
	a.matched++
	a.callSum += fs.Count
	if fs.HasTotal {
		a.totalSum += fs.Total
		a.hasTotal = true
	} else {
		a.avgSum += fs.Avg * fs.Count
	}
	if !a.hasMinMax {
		a.fastest, a.slowest, a.hasMinMax = fs.Fastest, fs.Slowest, true
	} else {
		if fs.Fastest < a.fastest {
			a.fastest = fs.Fastest
		}
		if fs.Slowest > a.slowest {
			a.slowest = fs.Slowest
		}
	}
}

func (a *sectionAgg) Merge(o *sectionAgg) {
	a.lines += o.lines
	a.parsed += o.parsed
	a.matched += o.matched
	a.callSum += o.callSum
	a.totalSum += o.totalSum
	a.avgSum += o.avgSum
	a.hasTotal = a.hasTotal || o.hasTotal
	if o.hasMinMax {
		if !a.hasMinMax {
			a.fastest, a.slowest, a.hasMinMax = o.fastest, o.slowest, true
		} else {
			if o.fastest < a.fastest {
				a.fastest = o.fastest
			}
			if o.slowest > a.slowest {
				a.slowest = o.slowest
			}
		}
	}
}

// Output 按 cfg.Output 输出汇总值; avg = Σ总用时/Σ次数(即按调用次数加权平均)
func (a *sectionAgg) Output(output string) (float64, error) {
	if a.matched == 0 {
		return 0, fmt.Errorf("无匹配功能号")
	}
	switch output {
	case "count":
		return a.callSum, nil
	case "total":
		if !a.hasTotal {
			return a.avgSum, nil
		}
		return a.totalSum, nil
	case "fastest":
		return a.fastest, nil
	case "slowest":
		return a.slowest, nil
	case "avg", "":
		if a.callSum <= 0 {
			return 0, fmt.Errorf("总调用次数为 0, 无法计算加权平均")
		}
		num := a.totalSum
		if !a.hasTotal {
			num = a.avgSum
		}
		return num / a.callSum, nil
	default:
		return 0, fmt.Errorf("未知输出值: %s (可选 count/total/fastest/slowest/avg)", output)
	}
}

// extractSections 返回 text 中所有"起始标记(行内含)~结束标记(行内含)"段落行组,
// 同时返回命中起始标记的次数; startMarker 空=从头, endMarker 空=到文件尾
func extractSections(text, startMarker, endMarker string) ([][]string, int) {
	var out [][]string
	starts := 0
	in := startMarker == ""
	if in {
		starts = 1
	}
	var cur []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if in {
			if endMarker != "" && strings.Contains(line, endMarker) {
				if len(cur) > 0 {
					out = append(out, cur)
				}
				cur, in = nil, false
			} else {
				cur = append(cur, line)
			}
			continue
		}
		if startMarker != "" && strings.Contains(line, startMarker) {
			in = true
			starts++
		}
	}
	if in && len(cur) > 0 {
		out = append(out, cur)
	}
	return out, starts
}

// prefixMatch 前缀过滤: prefixes 非空时仅保留命中前缀; 否则 excludePrefixes 非空时排除命中前缀(用于"其他"类); 否则全收
func prefixMatch(id string, cfg logConfig) bool {
	if len(cfg.Prefixes) > 0 {
		for _, p := range cfg.Prefixes {
			if strings.HasPrefix(id, p) {
				return true
			}
		}
		return false
	}
	for _, p := range cfg.ExcludePrefixes {
		if strings.HasPrefix(id, p) {
			return false
		}
	}
	return true
}

// parseSectionLines 解析段落各行并前缀过滤, 返回聚合结果与解析失败行数
func parseSectionLines(lines []string, cfg logConfig, re *regexp.Regexp) (*sectionAgg, int, error) {
	agg := &sectionAgg{}
	fail := 0
	names := re.SubexpNames()
	idx := make(map[string]int, len(names))
	for i, n := range names {
		if n != "" {
			idx[n] = i
		}
	}
	idKey := orDefault(cfg.IDField, "func")
	countKey := orDefault(cfg.CountField, "count")
	totalKey := orDefault(cfg.TotalField, "total")
	minKey := orDefault(cfg.MinField, "fastest")
	maxKey := orDefault(cfg.MaxField, "slowest")
	avgKey := orDefault(cfg.AvgField, "avg")
	if _, ok := idx[idKey]; !ok {
		return nil, 0, fmt.Errorf("linePattern 缺少 %q 命名分组", idKey)
	}
	if _, ok := idx[countKey]; !ok {
		return nil, 0, fmt.Errorf("linePattern 缺少 %q 命名分组", countKey)
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		agg.lines++
		m := re.FindStringSubmatch(line)
		if m == nil {
			fail++
			continue
		}
		id := strings.TrimSpace(m[idx[idKey]])
		if !prefixMatch(id, cfg) {
			agg.parsed++
			continue
		}
		cnt, err := strconv.ParseFloat(m[idx[countKey]], 64)
		if err != nil {
			fail++
			continue
		}
		agg.parsed++
		fs := funcStat{ID: id, Count: cnt}
		if i, ok := idx[totalKey]; ok && m[i] != "" {
			if v, perr := strconv.ParseFloat(m[i], 64); perr == nil {
				fs.Total = v
				fs.HasTotal = true
			}
		}
		if i, ok := idx[minKey]; ok && m[i] != "" {
			if v, perr := strconv.ParseFloat(m[i], 64); perr == nil {
				fs.Fastest = v
			}
		}
		if i, ok := idx[maxKey]; ok && m[i] != "" {
			if v, perr := strconv.ParseFloat(m[i], 64); perr == nil {
				fs.Slowest = v
			}
		}
		if i, ok := idx[avgKey]; ok && m[i] != "" {
			if v, perr := strconv.ParseFloat(m[i], 64); perr == nil {
				fs.Avg = v
			}
		}
		agg.Add(fs)
	}
	return agg, fail, nil
}

// orDefault 空串回退默认字段名
func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// sectionRead section 模式单数据源读取: 读取全部匹配文件 → 按数据源编码解码 → 段落提取 →
// 逐行解析 → 前缀过滤 → 聚合。返回聚合结果、匹配文件数、命中起始标记的段落数与行级元信息。
// 单数据源采集与分类采集(跨服务器)共用此逻辑, 保证同一分类文件处理口径一致。
func (c *Collectors) sectionRead(ctx context.Context, fl *persistence.CollectFlow, ds *persistence.DataSource,
	vars map[string]string, base time.Time, cfg logConfig, re *regexp.Regexp) (*sectionAgg, []string, int, *CollectMeta, error) {
	var dsCfg datasource.Config
	if err := json.Unmarshal([]byte(ds.Config), &dsCfg); err != nil {
		return nil, nil, 0, nil, fmt.Errorf("数据源配置解析失败: %w", err)
	}
	if err := c.cred.DecryptForCollect(&dsCfg); err != nil {
		return nil, nil, 0, nil, err
	}
	pattern := cfg.FileGlob
	if pattern == "" {
		pattern = dsCfg.GlobTemplate
	}
	if pattern == "" {
		pattern = dsCfg.PathTemplate
	}
	pattern, err := c.renderPattern(ctx, fl, pattern, vars, base)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	files, err := datasource.ReadLogFiles(ctx, &dsCfg, pattern)
	if err != nil {
		return nil, nil, 0, &CollectMeta{Glob: pattern}, err
	}
	meta := &CollectMeta{Glob: pattern}
	agg := &sectionAgg{}
	sections := 0
	for _, f := range files {
		meta.Files = append(meta.Files, f.Path)
		text, derr := datasource.DecodeLog(f.Data, dsCfg.Encoding)
		if derr != nil {
			return nil, nil, 0, meta, fmt.Errorf("文件 %s 解码失败: %w", f.Path, derr)
		}
		secs, starts := extractSections(text, cfg.StartMarker, cfg.EndMarker)
		sections += starts
		for _, sec := range secs {
			a, fail, perr := parseSectionLines(sec, cfg, re)
			if perr != nil {
				return nil, nil, 0, meta, perr
			}
			meta.RowsTotal += a.lines
			meta.ParseFailCount += fail
			agg.Merge(a)
		}
	}
	return agg, meta.Files, sections, meta, nil
}

// compileSectionCfg 校验并编译 section 配置(单数据源/分类采集共用)
func compileSectionCfg(cfg logConfig) (*regexp.Regexp, error) {
	if cfg.StartMarker == "" {
		return nil, fmt.Errorf("section 模式须配置 startMarker")
	}
	if cfg.LinePattern == "" {
		return nil, fmt.Errorf("section 模式须配置 linePattern")
	}
	re, err := regexp.Compile(cfg.LinePattern)
	if err != nil {
		return nil, fmt.Errorf("linePattern 编译失败: %w", err)
	}
	return re, nil
}

// sectionOutputText 汇总值字符串化(原始行与 runLog 共用)
func sectionOutputText(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// metaGlob 取采集元信息中的实际匹配模式(nil 安全, 便于失败信息定位读了哪个 glob)
func metaGlob(m *CollectMeta) string {
	if m == nil {
		return ""
	}
	return m.Glob
}

// fileSummary 文件清单摘要: 前 3 个文件名 + 总数(过长清单不展开)
func fileSummary(paths []string) string {
	if len(paths) == 0 {
		return "-"
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, path.Base(p))
	}
	sort.Strings(names)
	if len(names) > 3 {
		return fmt.Sprintf("%s 等 %d 个", strings.Join(names[:3], ","), len(names))
	}
	return strings.Join(names, ",")
}

// sectionNote 单数据源聚合明细(分类采集 RunLog 用)
func sectionNote(name, glob string, files []string, sections int, a *sectionAgg) string {
	return fmt.Sprintf("[%s] glob=%s files=%d(%s) sections=%d funcs(parsed/matched)=%d/%d callSum=%.0f totalSum=%.2f fastest=%.2f slowest=%.2f",
		name, glob, len(files), fileSummary(files), sections, a.parsed, a.matched, a.callSum, a.totalSum, a.fastest, a.slowest)
}

// collectSection section 模式采集: 读取全部匹配文件 → 按数据源编码解码 → 段落提取 →
// 逐行解析 → 前缀过滤 → 聚合 → 单一汇总值输出(供清洗/落地/判定)
func (c *Collectors) collectSection(ctx context.Context, fl *persistence.CollectFlow, ds *persistence.DataSource,
	vars map[string]string, base time.Time, cfg logConfig) ([]RawRow, *CollectMeta, error) {
	re, err := compileSectionCfg(cfg)
	if err != nil {
		return nil, nil, err
	}
	agg, files, sections, meta, err := c.sectionRead(ctx, fl, ds, vars, base, cfg, re)
	if err != nil {
		return nil, meta, err
	}
	if sections == 0 {
		return nil, meta, fmt.Errorf("未找到段落(起始标记: %s, glob=%s, 匹配文件 %d 个: %s)",
			cfg.StartMarker, meta.Glob, len(files), fileSummary(files))
	}
	if agg.matched == 0 {
		return nil, meta, fmt.Errorf("段落内无匹配聚合前缀的功能号(glob=%s, 解析 %d 行, 失败 %d 行)",
			meta.Glob, agg.parsed, meta.ParseFailCount)
	}
	val, err := agg.Output(cfg.Output)
	if err != nil {
		return nil, meta, err
	}
	meta.RunLog = fmt.Sprintf("glob=%s files=%d(%s) sections=%d funcs(parsed/matched)=%d/%d callSum=%.0f totalSum=%.2f fastest=%.2f slowest=%.2f output=%s=%s",
		meta.Glob, len(files), fileSummary(files), sections, agg.parsed, agg.matched, agg.callSum, agg.totalSum, agg.fastest, agg.slowest, cfg.Output, sectionOutputText(val))
	return []RawRow{{Raw: sectionOutputText(val)}}, meta, nil
}

// collectSectionCategory section 模式的分类采集: 分类下每台服务器使用同一段落处理逻辑,
// 逐服务器聚合后跨服务器合并(次数求和 / 最快取最小 / 最慢取最大 / 平均按调用次数加权 =
// Σ总用时/Σ次数), 输出单一汇总值。单台服务器读取失败或未生成段落不中断整体,
// 失败明细写入 RunLog 并记录 DsFailed; 全部失败或全部无有效段落才判定采集失败。
func (c *Collectors) collectSectionCategory(ctx context.Context, fl *persistence.CollectFlow, dsList []*persistence.DataSource,
	vars map[string]string, base time.Time, cfg logConfig) ([]RawRow, *CollectMeta, error) {
	re, err := compileSectionCfg(cfg)
	if err != nil {
		return nil, nil, err
	}
	meta := &CollectMeta{}
	agg := &sectionAgg{}
	notes := make([]string, 0, len(dsList))
	sections, emptyDs := 0, 0
	for _, ds := range dsList {
		a, files, starts, m, err := c.sectionRead(ctx, fl, ds, vars, base, cfg, re)
		if err != nil {
			meta.DsFailed = append(meta.DsFailed, ds.ID)
			notes = append(notes, fmt.Sprintf("[%s] 读取失败: %v (glob=%s)", ds.Name, err, metaGlob(m)))
			continue
		}
		meta.DsOK = append(meta.DsOK, ds.ID)
		meta.RowsTotal += m.RowsTotal
		meta.ParseFailCount += m.ParseFailCount
		meta.Files = append(meta.Files, m.Files...)
		if meta.Glob == "" {
			meta.Glob = m.Glob
		}
		sections += starts
		if a.matched == 0 {
			emptyDs++
			notes = append(notes, sectionNote(ds.Name, m.Glob, files, starts, a)+" 无命中聚合前缀")
			continue
		}
		agg.Merge(a)
		notes = append(notes, sectionNote(ds.Name, m.Glob, files, starts, a))
	}
	if len(meta.DsOK) == 0 {
		return nil, meta, fmt.Errorf("分类下全部数据源读取失败: %s", strings.Join(notes, "\n"))
	}
	if sections == 0 {
		return nil, meta, fmt.Errorf("分类下全部数据源均未找到段落(起始标记: %s, glob=%s, 读取文件 %d 个: %s)",
			cfg.StartMarker, meta.Glob, len(meta.Files), fileSummary(meta.Files))
	}
	if agg.matched == 0 {
		return nil, meta, fmt.Errorf("分类下段落内无匹配聚合前缀的功能号(glob=%s, 解析 %d 行, 失败 %d 行)",
			meta.Glob, agg.parsed, meta.ParseFailCount)
	}
	val, err := agg.Output(cfg.Output)
	if err != nil {
		return nil, meta, err
	}
	meta.RunLog = fmt.Sprintf("分类采集 glob=%s 服务器(成功/失败/无命中)=%d/%d/%d 文件合计=%d 段落=%d funcs(parsed/matched)=%d/%d callSum=%.0f totalSum=%.2f fastest=%.2f slowest=%.2f output=%s=%s\n%s",
		meta.Glob, len(meta.DsOK), len(meta.DsFailed), emptyDs, len(meta.Files), sections, agg.parsed, agg.matched,
		agg.callSum, agg.totalSum, agg.fastest, agg.slowest, cfg.Output, sectionOutputText(val), strings.Join(notes, "\n"))
	return []RawRow{{Raw: sectionOutputText(val)}}, meta, nil
}
