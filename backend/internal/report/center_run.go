package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/chartimg"
	"capacity-manage/backend/internal/pkg/clock"

	"github.com/jung-kurt/gofpdf"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PushFunc func(ctx context.Context, channelIDs []int64, recipients []string, subject, body, dedupeKey string) error

// RunCenter generates and persists one center report with Excel/PDF files.
func (s *Service) RunCenter(ctx context.Context, definition persistence.ReportDefinition,
	period ReportPeriod, generatedBy string, systemCodes []string) (*persistence.ReportRun, error) {
	period = normalizeReportPeriod(definition, period)
	now := time.Now()
	run := persistence.ReportRun{
		DefinitionID: definition.ID, RunDate: period.RunDate,
		PeriodStart: period.Start, PeriodEnd: period.End,
		Status: "running", GeneratedBy: generatedBy, StartedAt: &now, CreatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return nil, err
	}
	data, err := s.Generate(ctx, definition, period, systemCodes)
	if err != nil {
		return s.failCenterRun(ctx, &run, err)
	}
	files, err := s.persistCenterFiles(ctx, &run, data)
	if err != nil {
		return s.failCenterRun(ctx, &run, err)
	}
	rawFiles, _ := json.Marshal(files)
	finished := time.Now()
	run.Status = "success"
	run.MetricsTotal = len(data.Rows)
	run.Files = string(rawFiles)
	run.FinishedAt = &finished
	if err := s.db.WithContext(ctx).Model(&run).Updates(map[string]any{
		"status": "success", "metrics_total": run.MetricsTotal,
		"files": string(rawFiles), "finished_at": finished, "error_msg": "",
	}).Error; err != nil {
		return &run, err
	}
	return &run, nil
}

func (s *Service) failCenterRun(ctx context.Context, run *persistence.ReportRun, err error) (*persistence.ReportRun, error) {
	finished := time.Now()
	run.Status = "failed"
	run.ErrorMsg = err.Error()
	run.FinishedAt = &finished
	_ = s.db.WithContext(ctx).Model(run).Updates(map[string]any{
		"status": "failed", "error_msg": err.Error(), "finished_at": finished,
	}).Error
	return run, err
}

func (s *Service) persistCenterFiles(ctx context.Context, run *persistence.ReportRun, data *CenterData) (map[string]string, error) {
	if s.files == nil {
		return map[string]string{}, nil
	}
	xlsx, err := BuildCenterExcel(data)
	if err != nil {
		return nil, fmt.Errorf("Excel 生成失败: %w", err)
	}
	pdf, err := BuildCenterPDF(data)
	if err != nil {
		return nil, fmt.Errorf("PDF 生成失败: %w", err)
	}
	xlsxKey := fmt.Sprintf("reports/center/%d.xlsx", run.ID)
	pdfKey := fmt.Sprintf("reports/center/%d.pdf", run.ID)
	if err := s.files.Save(xlsxKey, bytes.NewReader(xlsx)); err != nil {
		return nil, err
	}
	if err := s.files.Save(pdfKey, bytes.NewReader(pdf)); err != nil {
		return nil, err
	}
	_ = ctx
	return map[string]string{"xlsx": xlsxKey, "pdf": pdfKey}, nil
}

func BuildCenterExcel(data *CenterData) ([]byte, error) {
	if data == nil {
		return nil, fmt.Errorf("report data is nil")
	}
	f := excelize.NewFile()
	sheet := "报表数据"
	f.SetSheetName(f.GetSheetName(0), sheet)
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"E8EEF5"}},
	})
	for i, label := range data.Columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, label)
		_ = f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	for rowIndex, row := range data.Rows {
		for colIndex, field := range data.Fields {
			cell, _ := excelize.CoordinatesToCellName(colIndex+1, rowIndex+2)
			_ = f.SetCellValue(sheet, cell, reportCell(row[field]))
		}
	}
	meta := "报表概览"
	if _, err := f.NewSheet(meta); err != nil {
		return nil, err
	}
	values := []string{
		"报表", data.Title,
		"周期", data.PeriodStart + " 至 " + data.PeriodEnd,
		"生成时间", data.GeneratedAt.Format("2006-01-02 15:04:05"),
		"指标数", fmt.Sprintf("%d", len(data.Rows)),
	}
	for i, value := range values {
		cell, _ := excelize.CoordinatesToCellName(1+(i%2)*2, 1+i/2)
		_ = f.SetCellValue(meta, cell, value)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func BuildCenterPDF(data *CenterData) ([]byte, error) {
	if data == nil {
		return nil, fmt.Errorf("report data is nil")
	}
	p := gofpdf.New("L", "mm", "A4", "")
	p.SetAutoPageBreak(true, 12)
	p.AddUTF8FontFromBytes("SimHei", "", chartimg.FontBytes())
	p.SetTitle(data.Title+" "+data.PeriodEnd, true)
	p.SetHeaderFunc(func() {
		p.SetFont("SimHei", "", 8)
		p.SetTextColor(150, 150, 150)
		p.CellFormat(0, 5, "容量管理平台 · "+data.Title, "0", 1, "L", false, 0, "")
	})
	p.SetFooterFunc(func() {
		p.SetY(-8)
		p.SetFont("SimHei", "", 8)
		p.CellFormat(0, 5, fmt.Sprintf("第 %d 页", p.PageNo()), "0", 0, "C", false, 0, "")
	})
	p.AddPage()
	p.SetFont("SimHei", "", 18)
	p.CellFormat(0, 10, data.Title, "0", 1, "L", false, 0, "")
	p.SetFont("SimHei", "", 10)
	p.SetTextColor(90, 90, 90)
	p.CellFormat(0, 6, fmt.Sprintf("统计周期: %s 至 %s    生成时间: %s", data.PeriodStart, data.PeriodEnd,
		data.GeneratedAt.Format("2006-01-02 15:04:05")), "0", 1, "L", false, 0, "")
	p.SetTextColor(0, 0, 0)
	p.Ln(2)

	width := 277.0
	if len(data.Fields) == 0 {
		data.Fields = append([]string(nil), data.Columns...)
	}
	colWidth := width / float64(len(data.Fields))
	if colWidth < 18 {
		colWidth = 18
	}
	writeHeader := func() {
		p.SetFont("SimHei", "", 8)
		p.SetFillColor(232, 238, 245)
		for _, label := range data.Columns {
			p.CellFormat(colWidth, 6, label, "1", 0, "L", true, 0, "")
		}
		p.Ln(6)
	}
	writeHeader()
	p.SetFont("SimHei", "", 8)
	for rowIndex, row := range data.Rows {
		if p.GetY()+6 > 195 {
			p.AddPage()
			writeHeader()
			p.SetFont("SimHei", "", 8)
		}
		if rowIndex%2 == 1 {
			p.SetFillColor(248, 250, 252)
		} else {
			p.SetFillColor(255, 255, 255)
		}
		for _, field := range data.Fields {
			cell := reportCell(row[field])
			if len(cell) > 28 {
				cell = cell[:28] + "..."
			}
			p.CellFormat(colWidth, 6, cell, "1", 0, "L", true, 0, "")
		}
		p.Ln(6)
	}
	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func reportCell(value any) string {
	switch item := value.(type) {
	case nil:
		return ""
	case string:
		return item
	case time.Time:
		return item.Format("2006-01-02")
	case *time.Time:
		if item == nil {
			return ""
		}
		return item.Format("2006-01-02")
	case float64:
		return fmt.Sprintf("%.4f", item)
	case *float64:
		if item == nil {
			return ""
		}
		return fmt.Sprintf("%.4f", *item)
	case int:
		return fmt.Sprintf("%d", item)
	case int64:
		return fmt.Sprintf("%d", item)
	default:
		return fmt.Sprint(item)
	}
}

// RunDueSubscriptions generates and pushes all subscriptions whose next
// scheduled time has arrived. The push callback is supplied by the app and
// reuses the existing channel retry/idempotency implementation.
func (s *Service) RunDueSubscriptions(ctx context.Context, now time.Time, push PushFunc) error {
	if s == nil || s.db == nil {
		return nil
	}
	var subs []persistence.ReportSubscription
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND (next_run_at IS NULL OR next_run_at <= ?)", true, now).
		Order("id").Find(&subs).Error; err != nil {
		return err
	}
	var firstErr error
	for i := range subs {
		sub := &subs[i]
		var definition persistence.ReportDefinition
		if err := s.db.WithContext(ctx).First(&definition, sub.DefinitionID).Error; err != nil {
			firstErr = err
			continue
		}
		period := subscriptionPeriod(definition, now)
		run, err := s.RunCenter(ctx, definition, period, "scheduler", nil)
		if err != nil {
			firstErr = err
			continue
		}
		if push != nil {
			var channelIDs []int64
			var recipients []string
			_ = json.Unmarshal([]byte(sub.ChannelIDs), &channelIDs)
			_ = json.Unmarshal([]byte(sub.Recipients), &recipients)
			body := fmt.Sprintf("%s（%s 至 %s）已生成，运行编号 %d。请在报表中心查看明细和 Excel/PDF。",
				definition.Name, period.Start.Format("2006-01-02"), period.End.Format("2006-01-02"), run.ID)
			dedupe := fmt.Sprintf("report-subscription:%d:run:%d", sub.ID, run.ID)
			if err := push(ctx, channelIDs, recipients, definition.Name, body, dedupe); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		next := ComputeNextRun(*sub, now)
		_ = s.db.WithContext(ctx).Model(sub).Updates(map[string]any{
			"last_run_at": now, "next_run_at": next,
		}).Error
	}
	return firstErr
}

func subscriptionPeriod(definition persistence.ReportDefinition, now time.Time) ReportPeriod {
	end := clock.DayStart(now).AddDate(0, 0, -1)
	start := end
	switch strings.ToLower(definition.ReportType) {
	case "weekly":
		start = end.AddDate(0, 0, -6)
	case "monthly":
		start = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, end.Location())
	default:
		window := definition.PeriodWindow
		if window <= 1 {
			window = 1
		}
		start = end.AddDate(0, 0, -(window - 1))
	}
	return ReportPeriod{RunDate: end, Start: start, End: end}
}

// ComputeNextRun returns the next local-time occurrence in the subscription's
// timezone. Monthly schedules use days 1..28 to keep every month valid.
func ComputeNextRun(sub persistence.ReportSubscription, after time.Time) *time.Time {
	loc, err := time.LoadLocation(sub.Timezone)
	if err != nil || sub.Timezone == "" {
		loc = time.Local
	}
	base := after.In(loc)
	hour, minute := parseHHMM(sub.ScheduleTime)
	next := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, loc)
	switch strings.ToLower(sub.ScheduleType) {
	case "weekly":
		target := sub.Weekday
		if target < 1 || target > 7 {
			target = 1
		}
		targetWeekday := time.Weekday(target % 7)
		for next.Weekday() != targetWeekday || !next.After(after) {
			next = next.AddDate(0, 0, 1)
		}
	case "monthly":
		day := sub.DayOfMonth
		if day < 1 || day > 28 {
			day = 1
		}
		next = time.Date(base.Year(), base.Month(), day, hour, minute, 0, 0, loc)
		if !next.After(after) {
			next = time.Date(base.Year(), base.Month()+1, day, hour, minute, 0, 0, loc)
		}
	default:
		if !next.After(after) {
			next = next.AddDate(0, 0, 1)
		}
	}
	return &next
}

func parseHHMM(raw string) (int, int) {
	var hour, minute int
	if _, err := fmt.Sscanf(raw, "%d:%d", &hour, &minute); err != nil {
		return 8, 30
	}
	if hour < 0 || hour > 23 {
		hour = 8
	}
	if minute < 0 || minute > 59 {
		minute = 30
	}
	return hour, minute
}

// EnsureReportDefinitions is called at app start so fresh databases have the
// standard M7 report templates even when migrations ran against an older DB.
func EnsureReportDefinitions(ctx context.Context, db *gorm.DB) error {
	defaults := []persistence.ReportDefinition{
		{Name: "容量周报", ReportType: "weekly", GroupDimension: "system", SortBy: "growthRate", SortDesc: true, IncludeForecast: true, PeriodWindow: 7, ChartOptions: "{}", Enabled: true},
		{Name: "容量月报", ReportType: "monthly", GroupDimension: "system", SortBy: "growthRate", SortDesc: true, IncludeForecast: true, PeriodWindow: 30, ChartOptions: "{}", Enabled: true},
		{Name: "容量使用分析", ReportType: "capacity_analysis", GroupDimension: "system", SortBy: "waterLevel", SortDesc: true, PeriodWindow: 30, ChartOptions: "{}", Enabled: true},
		{Name: "告警汇总报表", ReportType: "alert_summary", GroupDimension: "system", SortBy: "alertCount", SortDesc: true, PeriodWindow: 30, ChartOptions: "{}", Enabled: true},
		{Name: "容量预测报表", ReportType: "forecast", GroupDimension: "system", SortBy: "riskScore", SortDesc: true, IncludeForecast: true, PeriodWindow: 90, ChartOptions: "{}", Enabled: true},
		{Name: "容量日报", ReportType: "daily", GroupDimension: "system", SortBy: "waterLevel", SortDesc: true, IncludeForecast: true, PeriodWindow: 1, ChartOptions: "{}", Enabled: true},
	}
	for i := range defaults {
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&defaults[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func roundFileSize(n int) int {
	return int(math.Max(0, float64(n)))
}
