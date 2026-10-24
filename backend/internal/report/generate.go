package report

// 日报生成（FR-RPT-01 自动生成 + FR-RPT-12 Excel/PDF 导出）
// 结构: CollectDaily(DB 查询) → BuildExcel / BuildPDF（纯函数, 可单测）→ GenerateDaily（编排落盘）

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"sort"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/chartimg"
	"capacity-manage/backend/internal/pkg/store"

	"github.com/jung-kurt/gofpdf"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// DailyRow 日报单指标行
type DailyRow struct {
	MetricCode string   `json:"metricCode" gorm:"column:metric_code"`
	Name       string   `json:"name" gorm:"column:name"`
	SystemCode string   `json:"systemCode" gorm:"column:system_code"`
	SystemName string   `json:"systemName" gorm:"column:system_name"`
	Unit       string   `json:"unit" gorm:"column:unit"`
	StatValue  *float64 `json:"statValue" gorm:"column:stat_value"`
	WaterLevel *float64 `json:"waterLevel" gorm:"column:water_level"`
	Level      string   `json:"level" gorm:"column:level"` // normal/warn/danger/missing/nodata
	Baseline   *float64 `json:"baseline" gorm:"column:baseline"`
	WarnLine   *float64 `json:"warnLine" gorm:"column:warn_line"`
	DangerLine *float64 `json:"dangerLine" gorm:"column:danger_line"`
	// Forecast columns are attached from the latest successful 30-day run
	// whose run date is not later than the report business date.
	ForecastModel  string     `json:"forecastModel" gorm:"-"`
	ForecastStatus string     `json:"forecastStatus" gorm:"-"`
	ETAWarnDate    *time.Time `json:"etaWarnDate" gorm:"-"`
	ETADangerDate  *time.Time `json:"etaDangerDate" gorm:"-"`
	RiskScore      *float64   `json:"riskScore" gorm:"-"`
}

// LevelChange is one immutable alert level transition exposed in the daily
// report. LegacyLevelMapping documents the R2.1 level migration even when a
// historical day has no transition rows of its own.
type LevelChange struct {
	AlertID    int64     `json:"alertId" gorm:"column:alert_id"`
	AlertNo    string    `json:"alertNo" gorm:"column:alert_no"`
	MetricCode string    `json:"metricCode" gorm:"column:metric_code"`
	SystemCode string    `json:"systemCode" gorm:"column:system_code"`
	AlertType  string    `json:"alertType" gorm:"column:alert_type"`
	FromLevel  string    `json:"fromLevel" gorm:"column:from_level"`
	ToLevel    string    `json:"toLevel" gorm:"column:to_level"`
	Action     string    `json:"action" gorm:"column:action"`
	Reason     string    `json:"reason" gorm:"column:reason"`
	Operator   string    `json:"operator" gorm:"column:operator"`
	ChangedAt  time.Time `json:"changedAt" gorm:"column:changed_at"`
}

type LegacyLevelMapping struct {
	AlertType string `json:"alertType"`
	FromLevel string `json:"fromLevel"`
	ToLevel   string `json:"toLevel"`
	Reason    string `json:"reason"`
}

var legacyLevelMappings = []LegacyLevelMapping{
	{AlertType: "threshold", FromLevel: "p0", ToLevel: "p3", Reason: "一期危险线首次触发迁移为二期待观察级别"},
	{AlertType: "threshold", FromLevel: "p1", ToLevel: "p3", Reason: "一期阈值告警迁移为二期待观察级别，后续按累积规则升级"},
	{AlertType: "collect_fail/missing/anomaly/ds_error", FromLevel: "p2", ToLevel: "p4", Reason: "数据质量告警与容量风险级别解耦"},
}

// CollectFail 当日采集异常
type CollectFail struct {
	FlowCode    string    `json:"flowCode" gorm:"column:flow_code"`
	MetricCode  string    `json:"metricCode" gorm:"column:metric_code"`
	Status      string    `json:"status" gorm:"column:status"`
	TriggeredAt time.Time `json:"triggeredAt" gorm:"column:triggered_at"`
	ErrMsg      string    `json:"errorMsg" gorm:"column:error_msg"`
}

// StatusCounts 状态分布
type StatusCounts struct {
	Normal  int `json:"normal"`
	Warn    int `json:"warn"`
	Danger  int `json:"danger"`
	Missing int `json:"missing"`
	Nodata  int `json:"nodata"`
}

func (c StatusCounts) Chart() chartimg.StatusCounts {
	return chartimg.StatusCounts{Normal: c.Normal, Warn: c.Warn, Danger: c.Danger, Missing: c.Missing}
}

// DailyData 单日日报数据
type DailyData struct {
	BizDate             string               `json:"bizDate"`
	Rows                []DailyRow           `json:"rows"`
	Fails               []CollectFail        `json:"collectFailures"`
	LevelChanges        []LevelChange        `json:"levelChanges"`
	LegacyLevelMappings []LegacyLevelMapping `json:"legacyLevelMappings"`
	GeneratedAt         time.Time            `json:"generatedAt"`
}

func (d *DailyData) Status() StatusCounts {
	var c StatusCounts
	for _, r := range d.Rows {
		switch r.Level {
		case "warn":
			c.Warn++
		case "danger":
			c.Danger++
		case "missing":
			c.Missing++
		case "nodata":
			c.Nodata++
		default:
			c.Normal++
		}
	}
	return c
}

// OverRows 超线清单（危险优先, 同级按水位降序）
func (d *DailyData) OverRows() []DailyRow {
	out := make([]DailyRow, 0)
	for _, r := range d.Rows {
		if r.Level == "warn" || r.Level == "danger" {
			out = append(out, r)
		}
	}
	sortOver(out)
	return out
}

func (d *DailyData) MissingRows() []DailyRow {
	out := make([]DailyRow, 0)
	for _, r := range d.Rows {
		if r.Level == "missing" {
			out = append(out, r)
		}
	}
	return out
}

// ForecastRows returns rows that have a forecast summary, risk-ranked first.
// Rows with never/insufficient_data/insufficient_quality are intentionally
// retained so operators can distinguish "not reached" from missing results.
func (d *DailyData) ForecastRows() []DailyRow {
	out := make([]DailyRow, 0)
	for _, r := range d.Rows {
		if r.ForecastStatus != "" {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := -1.0, -1.0
		if out[i].RiskScore != nil {
			wi = *out[i].RiskScore
		}
		if out[j].RiskScore != nil {
			wj = *out[j].RiskScore
		}
		if wi != wj {
			return wi > wj
		}
		return out[i].MetricCode < out[j].MetricCode
	})
	return out
}

func sortOver(rows []DailyRow) {
	rank := func(l string) int {
		if l == "danger" {
			return 0
		}
		return 1
	}
	wl := func(r DailyRow) float64 {
		if r.WaterLevel != nil {
			return *r.WaterLevel
		}
		return -1
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rank(rows[i].Level) != rank(rows[j].Level) {
			return rank(rows[i].Level) < rank(rows[j].Level)
		}
		if wl(rows[i]) != wl(rows[j]) {
			return wl(rows[i]) > wl(rows[j])
		}
		return rows[i].MetricCode < rows[j].MetricCode
	})
}

// CollectDaily 采集日报数据。systemCodes 为 nil 表示全部（自动生成/平台管理员），
// 非 nil 时按数据权限过滤（FR-RPT-14）。
func CollectDaily(db *gorm.DB, systemCodes []string, bd time.Time) (*DailyData, error) {
	var rows []DailyRow
	scopeSQL := ""
	args := []any{bd}
	if systemCodes != nil {
		scopeSQL = " AND m.system_code IN ?"
		args = append(args, systemCodes)
	}
	q := db.Raw(`
		SELECT m.metric_code, m.name, m.system_code, s.name AS system_name, m.unit,
		       d.stat_value, d.water_level, d.level AS level,
		       m.baseline, m.warn_line, m.danger_line
		FROM capacity_metric m
		JOIN business_system s ON s.system_code = m.system_code
		LEFT JOIN metric_value_day d ON d.metric_code = m.metric_code AND d.biz_date = ?
		WHERE m.status = 'active'`+scopeSQL, args...)
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}

	// 缺数判定: 当日无日数据 且 存在建档早于该业务日的活动采集流程 → missing, 否则 nodata
	metricCodes := make([]string, 0, len(rows))
	for _, r := range rows {
		metricCodes = append(metricCodes, r.MetricCode)
	}
	dayEnd := bd.AddDate(0, 0, 1)
	flowBefore := map[string]bool{}
	if len(metricCodes) > 0 {
		var flows []struct {
			MetricCode string
			CreatedAt  time.Time
		}
		_ = db.Table("collect_flow").
			Select("metric_code, created_at").
			Where("status = 'active' AND metric_code IN ?", metricCodes).
			Scan(&flows).Error
		for _, f := range flows {
			if !f.CreatedAt.After(dayEnd) {
				flowBefore[f.MetricCode] = true
			}
		}
	}
	for i := range rows {
		if rows[i].StatValue != nil {
			if rows[i].Level == "" {
				rows[i].Level = "normal"
			}
			continue
		}
		if rows[i].Level == "" || rows[i].Level == "normal" {
			if flowBefore[rows[i].MetricCode] {
				rows[i].Level = "missing"
			} else {
				rows[i].Level = "nodata"
			}
		}
	}
	if err := attachDailyForecast(db, rows, bd); err != nil {
		return nil, err
	}

	var fails []CollectFail
	_ = db.Table("collect_task_log").
		Select("flow_code, metric_code, status, triggered_at, error_msg").
		Where("triggered_at >= ? AND triggered_at < ? AND status IN ('failed','timeout')", bd, dayEnd).
		Order("triggered_at").Limit(100).
		Scan(&fails).Error
	if fails == nil {
		fails = []CollectFail{}
	}
	levelChanges, err := collectDailyLevelChanges(db, systemCodes, bd)
	if err != nil {
		return nil, err
	}
	mappings := make([]LegacyLevelMapping, len(legacyLevelMappings))
	copy(mappings, legacyLevelMappings)

	return &DailyData{
		BizDate:             bd.Format("2006-01-02"),
		Rows:                rows,
		Fails:               fails,
		LevelChanges:        levelChanges,
		LegacyLevelMappings: mappings,
		GeneratedAt:         time.Now(),
	}, nil
}

func attachDailyForecast(db *gorm.DB, rows []DailyRow, bd time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	var run persistence.ForecastRun
	err := db.Where("status = ? AND run_date <= ?", "success", bd).
		Order("run_date DESC, id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		codes = append(codes, row.MetricCode)
	}
	var results []persistence.ForecastResult
	if err := db.Where("run_id = ? AND horizon_days = ? AND metric_code IN ?", run.ID, 30, codes).
		Find(&results).Error; err != nil {
		return err
	}
	byMetric := make(map[string]persistence.ForecastResult, len(results))
	for _, result := range results {
		byMetric[result.MetricCode] = result
	}
	for i := range rows {
		result, ok := byMetric[rows[i].MetricCode]
		if !ok {
			continue
		}
		rows[i].ForecastModel = result.ModelType
		rows[i].ForecastStatus = result.Status
		rows[i].ETAWarnDate = result.ETAWarnDate
		rows[i].ETADangerDate = result.ETADangerDate
		rows[i].RiskScore = result.RiskScore
	}
	return nil
}

func collectDailyLevelChanges(db *gorm.DB, systemCodes []string, bd time.Time) ([]LevelChange, error) {
	dayEnd := bd.AddDate(0, 0, 1)
	q := db.Table("alert_level_history h").
		Select(`h.alert_id, a.alert_no, a.metric_code, a.system_code, a.alert_type,
			h.from_level, h.to_level, h.action, h.reason, h.operator, h.changed_at`).
		Joins("JOIN alert_record a ON a.id = h.alert_id").
		Where("h.changed_at >= ? AND h.changed_at < ?", bd, dayEnd)
	if systemCodes != nil {
		if len(systemCodes) == 0 {
			q = q.Where("1 = 0")
		} else {
			q = q.Where("a.system_code IN ?", systemCodes)
		}
	}
	var changes []LevelChange
	if err := q.Order("h.changed_at, h.id").Scan(&changes).Error; err != nil {
		return nil, err
	}
	if changes == nil {
		changes = []LevelChange{}
	}
	return changes, nil
}

// Charts 生成图表快照 PNG（水位条形图 + 状态分布环形图）
func (d *DailyData) Charts() (barPNG, donutPNG []byte, err error) {
	items := make([]chartimg.BarItem, 0, 10)
	top := make([]DailyRow, 0)
	for _, r := range d.Rows {
		if r.WaterLevel != nil {
			top = append(top, r)
		}
	}
	sort.Slice(top, func(i, j int) bool { return *top[i].WaterLevel > *top[j].WaterLevel })
	if len(top) > 10 {
		top = top[:10]
	}
	for _, r := range top {
		col := color.RGBA{0x34, 0xc7, 0x59, 0xff}
		switch r.Level {
		case "danger":
			col = color.RGBA{0xff, 0x3b, 0x30, 0xff}
		case "warn":
			col = color.RGBA{0xff, 0x95, 0x00, 0xff}
		}
		items = append(items, chartimg.BarItem{Label: r.Name, Value: *r.WaterLevel, Color: col})
	}
	barImg, err := chartimg.WaterLevelBars(items, 460, 60+len(items)*26)
	if err != nil {
		return nil, nil, err
	}
	if barPNG, err = chartimg.PNG(barImg); err != nil {
		return nil, nil, err
	}
	donutImg, err := chartimg.StatusDonut(d.Status().Chart(), 460, 240)
	if err != nil {
		return nil, nil, err
	}
	if donutPNG, err = chartimg.PNG(donutImg); err != nil {
		return nil, nil, err
	}
	return barPNG, donutPNG, nil
}

var levelName = map[string]string{
	"normal": "正常", "warn": "预警", "danger": "危险", "missing": "缺数", "nodata": "无数据",
}

func LevelName(l string) string {
	if n, ok := levelName[l]; ok {
		return n
	}
	return l
}

func fmtVal(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.4f", *v)
}

func fmtWL(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.2f%%", *v*100)
}

func fmtDayPtr(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.Format("2006-01-02")
}

func forecastStatusName(status string) string {
	switch status {
	case "success":
		return "成功"
	case "never":
		return "窗口内未到线"
	case "insufficient_data":
		return "数据不足"
	case "insufficient_quality":
		return "质量不足"
	default:
		return status
	}
}

// BuildExcel 日报 Excel（明细 + 超线/缺数/异常清单 + 图表快照）
func BuildExcel(d *DailyData) ([]byte, error) {
	f := excelize.NewFile()

	st, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"E8EEF5"}}})

	writeHeader := func(sheet string, headers []string) {
		for i, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			_ = f.SetCellValue(sheet, cell, h)
			_ = f.SetCellStyle(sheet, cell, cell, st)
		}
	}
	rowCells := func(sheet string, row int, vals ...string) {
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}

	// Sheet1 指标明细
	s1 := "指标明细"
	f.SetSheetName(f.GetSheetName(0), s1)
	writeHeader(s1, []string{
		"系统编码", "系统名称", "指标编码", "指标名称", "单位", "当日值", "水位",
		"基线", "预警线", "危险线", "状态", "预测模型", "预测状态",
		"预警线ETA", "危险线ETA", "风险分",
	})
	for i, r := range d.Rows {
		rowCells(s1, i+2, r.SystemCode, r.SystemName, r.MetricCode, r.Name, r.Unit,
			fmtVal(r.StatValue), fmtWL(r.WaterLevel), fmtVal(r.Baseline), fmtVal(r.WarnLine),
			fmtVal(r.DangerLine), LevelName(r.Level), r.ForecastModel,
			forecastStatusName(r.ForecastStatus), fmtDayPtr(r.ETAWarnDate),
			fmtDayPtr(r.ETADangerDate), fmtVal(r.RiskScore))
	}

	// Sheet2 超线清单（危险优先）
	s2 := "超线清单"
	if _, e := f.NewSheet(s2); e != nil {
		return nil, e
	}
	writeHeader(s2, []string{"指标编码", "指标名称", "系统", "当日值", "水位", "状态"})
	for i, r := range d.OverRows() {
		rowCells(s2, i+2, r.MetricCode, r.Name, r.SystemName, fmtVal(r.StatValue), fmtWL(r.WaterLevel), LevelName(r.Level))
	}

	// Sheet3 缺数清单
	s3 := "缺数清单"
	if _, e := f.NewSheet(s3); e != nil {
		return nil, e
	}
	writeHeader(s3, []string{"指标编码", "指标名称", "系统"})
	for i, r := range d.MissingRows() {
		rowCells(s3, i+2, r.MetricCode, r.Name, r.SystemName)
	}

	// Sheet4 采集异常清单
	s4 := "采集异常清单"
	if _, e := f.NewSheet(s4); e != nil {
		return nil, e
	}
	writeHeader(s4, []string{"流程编码", "指标编码", "状态", "触发时间", "错误信息"})
	for i, t := range d.Fails {
		msg := t.ErrMsg
		if len(msg) > 300 {
			msg = msg[:300]
		}
		rowCells(s4, i+2, t.FlowCode, t.MetricCode, t.Status, t.TriggeredAt.Format("2006-01-02 15:04:05"), msg)
	}

	// Sheet5 报表概览（含图表快照）
	s5 := "报表概览"
	if _, e := f.NewSheet(s5); e != nil {
		return nil, e
	}
	c := d.Status()
	meta := []string{
		"容量日报", d.BizDate,
		"生成时间", d.GeneratedAt.Format("2006-01-02 15:04:05"),
		"指标总数", fmt.Sprintf("%d", len(d.Rows)),
		"超预警/超危险", fmt.Sprintf("%d / %d", c.Warn, c.Danger),
		"缺数", fmt.Sprintf("%d", c.Missing),
		"采集异常", fmt.Sprintf("%d", len(d.Fails)),
	}
	for i, v := range meta {
		cell, _ := excelize.CoordinatesToCellName(1+(i%2)*2, 1+i/2)
		_ = f.SetCellValue(s5, cell, v)
	}
	if bar, donut, err := d.Charts(); err == nil && len(bar) > 0 {
		_ = f.AddPictureFromBytes(s5, "A10", &excelize.Picture{Extension: "png", File: bar})
		if len(donut) > 0 {
			_ = f.AddPictureFromBytes(s5, "L10", &excelize.Picture{Extension: "png", File: donut})
		}
	}

	// Sheet6 级别变化：固定迁移映射 + 当日实际级别变化。
	s6 := "级别变化"
	if _, e := f.NewSheet(s6); e != nil {
		return nil, e
	}
	writeHeader(s6, []string{"变更类型", "告警类型", "原级别", "新级别", "告警号", "指标", "系统", "操作人", "时间", "原因"})
	row := 2
	for _, m := range d.LegacyLevelMappings {
		rowCells(s6, row, "迁移映射", m.AlertType, m.FromLevel, m.ToLevel, "", "", "", "", "", m.Reason)
		row++
	}
	for _, c := range d.LevelChanges {
		rowCells(s6, row, "实际变化", c.AlertType, c.FromLevel, c.ToLevel, c.AlertNo,
			c.MetricCode, c.SystemCode, c.Operator, c.ChangedAt.Format("2006-01-02 15:04:05"), c.Reason)
		row++
	}

	buf := &bytes.Buffer{}
	if err := f.Write(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BuildPDF 日报 PDF（图文版式: 概要 + 图表 + 清单表格）
func BuildPDF(d *DailyData) ([]byte, error) {
	p := gofpdf.New("P", "mm", "A4", "")
	p.SetAutoPageBreak(true, 15)
	p.SetTitle("容量日报 "+d.BizDate, true)

	// 中文字体（SimHei TTF 直接内存加载; 不用临时文件, 规避 gofpdf fontpath 相对路径拼接在 Linux 下丢前导斜杠的问题）
	p.AddUTF8FontFromBytes("SimHei", "", chartimg.FontBytes())

	p.SetHeaderFunc(func() {
		p.SetFont("SimHei", "", 9)
		p.SetTextColor(150, 150, 150)
		p.CellFormat(190, 6, "容量管理平台 · 每日容量日报", "0", 1, "L", false, 0, "")
	})
	p.SetFooterFunc(func() {
		p.SetFont("SimHei", "", 8)
		p.SetTextColor(150, 150, 150)
		p.SetY(-10)
		p.CellFormat(0, 6, fmt.Sprintf("第 %d 页", p.PageNo()), "0", 0, "C", false, 0, "")
	})

	p.AddPage()
	p.SetFont("SimHei", "", 20)
	p.CellFormat(0, 12, "容量日报", "0", 1, "L", false, 0, "")
	p.SetFont("SimHei", "", 11)
	p.SetTextColor(90, 90, 90)
	p.CellFormat(0, 7, fmt.Sprintf("业务日期: %s    生成时间: %s", d.BizDate, d.GeneratedAt.Format("2006-01-02 15:04:05")), "0", 1, "L", false, 0, "")
	p.SetTextColor(0, 0, 0)

	c := d.Status()
	p.SetFont("SimHei", "", 11)
	summary := fmt.Sprintf("指标总数 %d    预警 %d    危险 %d    缺数 %d    采集异常 %d",
		len(d.Rows), c.Warn, c.Danger, c.Missing, len(d.Fails))
	p.SetFillColor(240, 244, 250)
	p.CellFormat(190, 9, summary, "1", 1, "L", true, 0, "")

	if bar, donut, err := d.Charts(); err == nil && len(bar) > 0 {
		p.RegisterImageReader("bar", "PNG", bytes.NewReader(bar))
		p.RegisterImageReader("donut", "PNG", bytes.NewReader(donut))
		// 条形图高 = 88mm * (60+26*条目数)/460
		n := 0
		for _, r := range d.Rows {
			if r.WaterLevel != nil {
				n++
			}
		}
		if n > 10 {
			n = 10
		}
		barH := 88.0 * float64(60+n*26) / 460.0
		{
			_, pageH := p.GetPageSize()
			if p.GetY()+barH+10 > pageH-20 {
				p.AddPage()
			}
		}
		y := p.GetY() + 2
		p.ImageOptions("bar", 12, y, 88, 0, false, gofpdf.ImageOptions{}, 0, "")
		p.ImageOptions("donut", 108, y, 80, 0, false, gofpdf.ImageOptions{}, 0, "")
		p.SetY(y + barH + 4)
	}

	section(p, "一、超线指标清单（危险优先）")
	writeTable(p,
		[]float64{28, 44, 28, 24, 20, 18},
		[]string{"指标编码", "指标名称", "系统", "当日值", "水位", "状态"},
		tableRows(d.OverRows(), func(r DailyRow) []string {
			return []string{r.MetricCode, r.Name, r.SystemName, fmtVal(r.StatValue), fmtWL(r.WaterLevel), LevelName(r.Level)}
		}))

	section(p, "二、预测摘要")
	writeTable(p,
		[]float64{25, 39, 20, 23, 26, 27, 30},
		[]string{"指标编码", "指标名称", "模型", "状态", "预警线ETA", "危险线ETA", "风险分"},
		tableRows(d.ForecastRows(), func(r DailyRow) []string {
			return []string{
				r.MetricCode, r.Name, r.ForecastModel, forecastStatusName(r.ForecastStatus),
				fmtDayPtr(r.ETAWarnDate), fmtDayPtr(r.ETADangerDate), fmtVal(r.RiskScore),
			}
		}))

	section(p, "三、缺数指标清单")
	writeTable(p,
		[]float64{28, 44, 28},
		[]string{"指标编码", "指标名称", "系统"},
		tableRows(d.MissingRows(), func(r DailyRow) []string {
			return []string{r.MetricCode, r.Name, r.SystemName}
		}))

	section(p, "四、采集异常清单")
	failRows := make([][]string, 0, len(d.Fails))
	for _, t := range d.Fails {
		msg := t.ErrMsg
		if len(msg) > 60 {
			msg = msg[:60] + "…"
		}
		failRows = append(failRows, []string{t.FlowCode, t.MetricCode, t.TriggeredAt.Format("15:04:05"), t.Status, msg})
	}
	writeTable(p,
		[]float64{30, 30, 20, 18, 58},
		[]string{"流程编码", "指标编码", "触发时间", "状态", "错误信息"},
		failRows)

	section(p, "五、级别变化")
	levelRows := make([][]string, 0, len(d.LegacyLevelMappings)+len(d.LevelChanges))
	for _, m := range d.LegacyLevelMappings {
		levelRows = append(levelRows, []string{
			"迁移映射", m.AlertType, m.FromLevel + " → " + m.ToLevel, "-", "-", m.Reason,
		})
	}
	for _, c := range d.LevelChanges {
		levelRows = append(levelRows, []string{
			"实际变化", c.AlertType, c.FromLevel + " → " + c.ToLevel,
			c.AlertNo + " / " + c.MetricCode, c.Operator + " / " + c.ChangedAt.Format("2006-01-02 15:04"), c.Reason,
		})
	}
	writeTable(p,
		[]float64{20, 28, 22, 42, 36, 42},
		[]string{"类型", "告警类型", "级别", "告警/指标", "操作人/时间", "原因"},
		levelRows)

	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func section(p *gofpdf.Fpdf, title string) {
	if p.GetY() > 250 {
		p.AddPage()
	}
	p.Ln(4)
	p.SetFont("SimHei", "", 13)
	p.CellFormat(0, 8, title, "0", 1, "L", false, 0, "")
	p.SetFont("SimHei", "", 9)
}

func tableRows(rows []DailyRow, f func(DailyRow) []string) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, f(r))
	}
	return out
}

// writeTable 简易表格: 表头 + 数据行（含跨页）; 无数据时输出"无"
func writeTable(p *gofpdf.Fpdf, widths []float64, headers []string, rows [][]string) {
	lm, _, _, _ := p.GetMargins()
	rowH := 6.5
	writeHeader := func() {
		p.SetFont("SimHei", "", 9)
		p.SetFillColor(232, 238, 245)
		x := lm
		for i, h := range headers {
			p.SetX(x)
			p.CellFormat(widths[i], rowH, h, "1", 0, "L", true, 0, "")
			x += widths[i]
		}
		p.Ln(rowH)
	}
	if len(rows) == 0 {
		p.SetFont("SimHei", "", 9)
		p.CellFormat(0, rowH, "无", "0", 1, "L", false, 0, "")
		return
	}
	writeHeader()
	p.SetFont("SimHei", "", 9)
	for i, row := range rows {
		{
			_, pageH := p.GetPageSize()
			if p.GetY()+rowH > pageH-15 {
				p.AddPage()
				writeHeader()
				p.SetFont("SimHei", "", 9)
			}
		}
		x := lm
		for j, v := range row {
			w := widths[j]
			s := v
			if p.GetStringWidth(s) > w-2 {
				for len(s) > 1 && p.GetStringWidth(s+"…") > w-2 {
					s = s[:len(s)-1]
				}
				s += "…"
			}
			p.SetX(x)
			if i%2 == 1 {
				p.SetFillColor(248, 250, 252)
			} else {
				p.SetFillColor(255, 255, 255)
			}
			p.CellFormat(w, rowH, s, "1", 0, "L", true, 0, "")
			x += w
		}
		p.Ln(rowH)
	}
}

// ReportKeys 日报文件存储键
func ReportKeys(bd time.Time) (xlsx, pdf string) {
	return fmt.Sprintf("reports/daily/%s.xlsx", bd.Format("2006-01-02")),
		fmt.Sprintf("reports/daily/%s.pdf", bd.Format("2006-01-02"))
}

// GenerateDaily 生成（或重新生成）指定业务日的日报并落盘。幂等: 同日重复生成覆盖文件并更新记录。
func GenerateDaily(ctx context.Context, db *gorm.DB, st store.Store, bd time.Time) (*persistence.ReportResult, error) {
	now := time.Now()
	var res persistence.ReportResult
	err := db.Where("report_type = ? AND biz_date = ?", "daily", bd).First(&res).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		res = persistence.ReportResult{ReportType: "daily", BizDate: bd, Status: "running", GeneratedAt: now}
		if err := db.Create(&res).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		res.Status = "running"
		res.GeneratedAt = now
		_ = db.Model(&res).Updates(map[string]any{"status": "running", "generated_at": now, "error_msg": ""}).Error
	}

	data, err := CollectDaily(db, nil, bd)
	if err != nil {
		fail(db, res, "数据查询失败: "+err.Error())
		return &res, err
	}
	xlsx, err := BuildExcel(data)
	if err != nil {
		fail(db, res, "Excel 生成失败: "+err.Error())
		return &res, err
	}
	pdf, err := BuildPDF(data)
	if err != nil {
		fail(db, res, "PDF 生成失败: "+err.Error())
		return &res, err
	}
	xlsxKey, pdfKey := ReportKeys(bd)
	if err := st.Save(xlsxKey, bytes.NewReader(xlsx)); err != nil {
		fail(db, res, "Excel 落盘失败: "+err.Error())
		return &res, err
	}
	if err := st.Save(pdfKey, bytes.NewReader(pdf)); err != nil {
		fail(db, res, "PDF 落盘失败: "+err.Error())
		return &res, err
	}

	c := data.Status()
	total := len(data.Rows)
	over := c.Warn + c.Danger
	_ = ctx
	_ = db.Model(&res).Updates(map[string]any{
		"status": "success", "file_ref": xlsxKey,
		"metrics_total": &total, "metrics_over": &over, "error_msg": "",
		"generated_at": time.Now(),
	}).Error
	res.Status = "success"
	res.FileRef = xlsxKey
	res.MetricsTotal = &total
	res.MetricsOver = &over
	return &res, nil
}

func fail(db *gorm.DB, res persistence.ReportResult, msg string) {
	res.Status = "failed"
	res.ErrMsg = msg
	_ = db.Model(&res).Updates(map[string]any{"status": "failed", "error_msg": msg}).Error
}
