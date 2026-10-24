package report

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/store"

	"gorm.io/gorm"
)

type ReportPeriod struct {
	RunDate time.Time `json:"runDate"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
}

type CenterData struct {
	ReportType  string           `json:"reportType"`
	Title       string           `json:"title"`
	PeriodStart string           `json:"periodStart"`
	PeriodEnd   string           `json:"periodEnd"`
	Fields      []string         `json:"fields"`
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
	Summary     map[string]any   `json:"summary"`
	GeneratedAt time.Time        `json:"generatedAt"`
}

type Service struct {
	db    *gorm.DB
	files store.Store
}

func NewService(db *gorm.DB, files store.Store) *Service {
	return &Service{db: db, files: files}
}

// Generate produces a permission-scoped, deterministic center report. The
// same result is used for preview, Excel/PDF export and subscription pushes.
func (s *Service) Generate(ctx context.Context, definition persistence.ReportDefinition,
	period ReportPeriod, systemCodes []string) (*CenterData, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("report service is not configured")
	}
	period = normalizeReportPeriod(definition, period)
	switch strings.ToLower(definition.ReportType) {
	case "weekly", "monthly":
		return s.generateTrendReport(ctx, definition, period, systemCodes)
	case "capacity_analysis":
		return s.generateCapacityAnalysis(ctx, definition, period, systemCodes)
	case "alert_summary":
		return s.generateAlertSummary(ctx, definition, period, systemCodes)
	case "forecast":
		return s.generateForecastReport(ctx, definition, period, systemCodes)
	case "daily", "":
		return s.generateDailyCenter(ctx, period, systemCodes)
	default:
		return nil, fmt.Errorf("不支持的报表类型: %s", definition.ReportType)
	}
}

func normalizeReportPeriod(definition persistence.ReportDefinition, period ReportPeriod) ReportPeriod {
	runDate := clock.DayStart(period.RunDate)
	if runDate.IsZero() {
		runDate = clock.DayStart(time.Now())
	}
	end := clock.DayStart(period.End)
	if end.IsZero() {
		end = runDate
	}
	start := clock.DayStart(period.Start)
	if start.IsZero() {
		switch strings.ToLower(definition.ReportType) {
		case "weekly":
			start = end.AddDate(0, 0, -6)
		case "monthly":
			start = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, end.Location())
		default:
			window := definition.PeriodWindow
			if window <= 0 {
				window = 30
			}
			start = end.AddDate(0, 0, -(window - 1))
		}
	}
	if end.Before(start) {
		start, end = end, start
	}
	return ReportPeriod{RunDate: runDate, Start: start, End: end}
}

type trendAgg struct {
	MetricCode  string
	Name        string
	SystemCode  string
	SystemName  string
	Unit        string
	Category    string
	CurrentSum  float64
	CurrentN    int
	CurrentMax  float64
	CurrentMin  float64
	PreviousSum float64
	PreviousN   int
	OverCount   int
}

func (s *Service) generateTrendReport(ctx context.Context, definition persistence.ReportDefinition,
	period ReportPeriod, systemCodes []string) (*CenterData, error) {
	window := daysInclusive(period.Start, period.End)
	queryStart := period.Start.AddDate(0, 0, -window)
	type row struct {
		MetricCode string    `gorm:"column:metric_code"`
		Name       string    `gorm:"column:name"`
		SystemCode string    `gorm:"column:system_code"`
		SystemName string    `gorm:"column:system_name"`
		Unit       string    `gorm:"column:unit"`
		Category   string    `gorm:"column:category"`
		BizDate    time.Time `gorm:"column:biz_date"`
		StatValue  *float64  `gorm:"column:stat_value"`
		Level      string    `gorm:"column:level"`
	}
	var rows []row
	q := s.db.WithContext(ctx).Table("metric_value_day d").
		Select("d.metric_code, m.name, m.system_code, s.name AS system_name, m.unit, m.category, d.biz_date, d.stat_value, d.level").
		Joins("JOIN capacity_metric m ON m.metric_code = d.metric_code").
		Joins("JOIN business_system s ON s.system_code = m.system_code").
		Where("m.status = 'active' AND d.biz_date >= ? AND d.biz_date <= ?", queryStart, period.End)
	q = applyReportScope(q, "m.system_code", systemCodes)
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	aggregates := map[string]*trendAgg{}
	for _, item := range rows {
		if item.StatValue == nil {
			continue
		}
		agg := aggregates[item.MetricCode]
		if agg == nil {
			agg = &trendAgg{
				MetricCode: item.MetricCode, Name: item.Name, SystemCode: item.SystemCode,
				SystemName: item.SystemName, Unit: item.Unit, Category: item.Category,
				CurrentMin: math.MaxFloat64,
			}
			aggregates[item.MetricCode] = agg
		}
		day := clock.DayStart(item.BizDate)
		if !day.Before(period.Start) {
			agg.CurrentSum += *item.StatValue
			agg.CurrentN++
			agg.CurrentMax = math.Max(agg.CurrentMax, *item.StatValue)
			agg.CurrentMin = math.Min(agg.CurrentMin, *item.StatValue)
			if item.Level == "warn" || item.Level == "danger" {
				agg.OverCount++
			}
		} else if !day.Before(queryStart) {
			agg.PreviousSum += *item.StatValue
			agg.PreviousN++
		}
	}
	alerts := s.countAlertsByMetric(ctx, period.Start, period.End, systemCodes)
	out := make([]*trendAgg, 0, len(aggregates))
	for _, agg := range aggregates {
		if agg.CurrentN == 0 {
			continue
		}
		if agg.CurrentMin == math.MaxFloat64 {
			agg.CurrentMin = 0
		}
		out = append(out, agg)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].MetricCode < out[j].MetricCode
	})
	resultRows := make([]map[string]any, 0, len(out))
	for _, agg := range out {
		avg := agg.CurrentSum / float64(agg.CurrentN)
		prevAvg := 0.0
		if agg.PreviousN > 0 {
			prevAvg = agg.PreviousSum / float64(agg.PreviousN)
		}
		growth := rate(avg, prevAvg)
		resultRows = append(resultRows, map[string]any{
			"metricCode": agg.MetricCode, "name": agg.Name, "systemCode": agg.SystemCode,
			"systemName": agg.SystemName, "unit": agg.Unit, "category": agg.Category,
			"avgValue": round4(avg), "maxValue": round4(agg.CurrentMax), "minValue": round4(agg.CurrentMin),
			"growthRate": growth, "yoyRate": nil, "overCount": agg.OverCount,
			"alertCount": alerts[agg.MetricCode],
		})
	}
	if sortBy := strings.TrimSpace(definition.SortBy); sortBy != "" {
		sortReportRows(resultRows, normalizeSortField(sortBy), definition.SortDesc)
	}
	title := "容量周报"
	if strings.EqualFold(definition.ReportType, "monthly") {
		title = "容量月报"
	}
	return &CenterData{
		ReportType: definition.ReportType, Title: title,
		PeriodStart: period.Start.Format("2006-01-02"), PeriodEnd: period.End.Format("2006-01-02"),
		Fields:      []string{"metricCode", "name", "systemName", "unit", "avgValue", "maxValue", "minValue", "growthRate", "overCount", "alertCount"},
		Columns:     []string{"指标编码", "指标名称", "系统", "单位", "均值", "最大值", "最小值", "环比", "超线次数", "告警数"},
		Rows:        resultRows,
		Summary:     map[string]any{"metricCount": len(resultRows), "alertCount": sumAlertCounts(alerts)},
		GeneratedAt: time.Now(),
	}, nil
}

func (s *Service) generateCapacityAnalysis(ctx context.Context, definition persistence.ReportDefinition,
	period ReportPeriod, systemCodes []string) (*CenterData, error) {
	trend, err := s.generateTrendReport(ctx, persistence.ReportDefinition{
		ReportType: "monthly", PeriodWindow: daysInclusive(period.Start, period.End), SortBy: "waterLevel",
	}, period, systemCodes)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(trend.Rows))
	for _, row := range trend.Rows {
		metricCode := stringValue(row["metricCode"])
		water := s.metricWaterLevel(ctx, metricCode, period.Start, period.End)
		row["waterLevel"] = water
		row["overRate"] = ratio(rowNumber(row["overCount"]), math.Max(1, float64(daysInclusive(period.Start, period.End))))
		rows = append(rows, row)
	}
	sortReportRows(rows, "waterLevel", true)
	trend.ReportType = "capacity_analysis"
	trend.Title = "容量使用分析"
	trend.Fields = []string{"metricCode", "name", "systemName", "unit", "waterLevel", "avgValue", "maxValue", "overRate", "alertCount"}
	trend.Columns = []string{"指标编码", "指标名称", "系统", "单位", "水位", "均值", "最大值", "超线率", "告警数"}
	trend.Rows = rows
	return trend, nil
}

func (s *Service) generateAlertSummary(ctx context.Context, definition persistence.ReportDefinition,
	period ReportPeriod, systemCodes []string) (*CenterData, error) {
	var alerts []persistence.AlertRecord
	q := s.db.WithContext(ctx).Where("trigger_at >= ? AND trigger_at < ?", period.Start, period.End.AddDate(0, 0, 1))
	q = applyReportScope(q, "system_code", systemCodes)
	if err := q.Find(&alerts).Error; err != nil {
		return nil, err
	}
	type agg struct {
		MetricCode string
		SystemCode string
		Count      int
		Open       int
		P1         int
		P2         int
		Escalated  int
		MTTASum    float64
		MTTAN      int
		MTTRSum    float64
		MTTRN      int
	}
	byMetric := map[string]*agg{}
	for _, alert := range alerts {
		a := byMetric[alert.MetricCode]
		if a == nil {
			a = &agg{MetricCode: alert.MetricCode, SystemCode: alert.SystemCode}
			byMetric[alert.MetricCode] = a
		}
		a.Count++
		if alert.Status == "active" || alert.Status == "processing" {
			a.Open++
		}
		if alert.Level == "p1" {
			a.P1++
		}
		if alert.Level == "p2" {
			a.P2++
		}
		a.Escalated += alert.EscalationCount
		if alert.ClaimedAt != nil {
			a.MTTASum += alert.ClaimedAt.Sub(alert.TriggerAt).Minutes()
			a.MTTAN++
		}
		if alert.ClosedAt != nil {
			a.MTTRSum += alert.ClosedAt.Sub(alert.TriggerAt).Minutes()
			a.MTTRN++
		}
	}
	rows := make([]map[string]any, 0, len(byMetric))
	for _, a := range byMetric {
		var metric persistence.CapacityMetric
		_ = s.db.WithContext(ctx).Where("metric_code = ?", a.MetricCode).First(&metric).Error
		mtta, mttr := 0.0, 0.0
		if a.MTTAN > 0 {
			mtta = round2(a.MTTASum / float64(a.MTTAN))
		}
		if a.MTTRN > 0 {
			mttr = round2(a.MTTRSum / float64(a.MTTRN))
		}
		rows = append(rows, map[string]any{
			"metricCode": a.MetricCode, "name": metric.Name, "systemCode": a.SystemCode,
			"alertCount": a.Count, "openCount": a.Open, "p1Count": a.P1, "p2Count": a.P2,
			"escalationCount": a.Escalated, "mttaMinutes": mtta, "mttrMinutes": mttr,
		})
	}
	sortReportRows(rows, normalizeSortField(definition.SortBy), definition.SortDesc)
	return &CenterData{
		ReportType: "alert_summary", Title: "告警汇总报表",
		PeriodStart: period.Start.Format("2006-01-02"), PeriodEnd: period.End.Format("2006-01-02"),
		Fields:      []string{"metricCode", "name", "systemCode", "alertCount", "openCount", "p1Count", "p2Count", "escalationCount", "mttaMinutes", "mttrMinutes"},
		Columns:     []string{"指标编码", "指标名称", "系统", "告警数", "未闭环", "P1", "P2", "升级次数", "MTTA(分)", "MTTR(分)"},
		Rows:        rows,
		Summary:     map[string]any{"alertCount": len(alerts), "metricCount": len(rows)},
		GeneratedAt: time.Now(),
	}, nil
}

func (s *Service) generateForecastReport(ctx context.Context, definition persistence.ReportDefinition,
	period ReportPeriod, systemCodes []string) (*CenterData, error) {
	var run persistence.ForecastRun
	if err := s.db.WithContext(ctx).Where("status = ?", "success").Order("run_date DESC, id DESC").First(&run).Error; err != nil {
		return nil, err
	}
	type row struct {
		persistence.ForecastResult
		Name       string   `gorm:"column:name"`
		SystemCode string   `gorm:"column:system_code"`
		Unit       string   `gorm:"column:unit"`
		WarnLine   *float64 `gorm:"column:warn_line"`
		DangerLine *float64 `gorm:"column:danger_line"`
	}
	var rows []row
	q := s.db.WithContext(ctx).Table("forecast_result r").
		Select("r.*, m.name, m.system_code, m.unit, m.warn_line, m.danger_line").
		Joins("JOIN capacity_metric m ON m.metric_code = r.metric_code").
		Where("r.run_id = ? AND r.horizon_days = 30", run.ID)
	q = applyReportScope(q, "m.system_code", systemCodes)
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, item := range rows {
		out = append(out, map[string]any{
			"metricCode": item.MetricCode, "name": item.Name, "systemCode": item.SystemCode,
			"unit": item.Unit, "modelType": item.ModelType, "status": item.Status,
			"etaWarnDate": item.ETAWarnDate, "etaDangerDate": item.ETADangerDate,
			"riskScore": item.RiskScore, "confidence": item.Confidence,
			"dataQuality": item.DataQuality, "explanation": item.Explanation,
			"warnLine": item.WarnLine, "dangerLine": item.DangerLine,
		})
	}
	sortReportRows(out, "riskScore", true)
	return &CenterData{
		ReportType: "forecast", Title: "容量预测报表",
		PeriodStart: run.DataEndDate.Format("2006-01-02"), PeriodEnd: run.RunDate.Format("2006-01-02"),
		Fields:      []string{"metricCode", "name", "systemCode", "etaWarnDate", "etaDangerDate", "riskScore", "confidence", "dataQuality", "explanation"},
		Columns:     []string{"指标编码", "指标名称", "系统", "预计预警日", "预计危险日", "风险分", "置信度", "数据质量", "解释"},
		Rows:        out,
		Summary:     map[string]any{"metricCount": len(out), "runId": run.ID, "modelVersion": run.ModelVersion},
		GeneratedAt: time.Now(),
	}, nil
}

func (s *Service) generateDailyCenter(ctx context.Context, period ReportPeriod, systemCodes []string) (*CenterData, error) {
	daily, err := CollectDaily(s.db.WithContext(ctx), systemCodes, period.End)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(daily.Rows))
	for _, row := range daily.Rows {
		rows = append(rows, map[string]any{
			"metricCode": row.MetricCode, "name": row.Name, "systemCode": row.SystemCode,
			"systemName": row.SystemName, "unit": row.Unit, "statValue": row.StatValue,
			"waterLevel": row.WaterLevel, "level": row.Level, "warnLine": row.WarnLine,
			"dangerLine": row.DangerLine,
		})
	}
	return &CenterData{
		ReportType: "daily", Title: "容量日报",
		PeriodStart: period.End.Format("2006-01-02"), PeriodEnd: period.End.Format("2006-01-02"),
		Fields:  []string{"metricCode", "name", "systemName", "unit", "statValue", "waterLevel", "level", "warnLine", "dangerLine"},
		Columns: []string{"指标编码", "指标名称", "系统", "单位", "当前值", "水位", "状态", "预警线", "危险线"},
		Rows:    rows, Summary: map[string]any{"metricCount": len(rows), "missingCount": len(daily.MissingRows())},
		GeneratedAt: time.Now(),
	}, nil
}

func (s *Service) metricWaterLevel(ctx context.Context, metricCode string, start, end time.Time) float64 {
	type row struct {
		WaterLevel *float64 `gorm:"column:water_level"`
	}
	var rows []row
	_ = s.db.WithContext(ctx).Table("metric_value_day").
		Select("water_level").
		Where("metric_code = ? AND biz_date >= ? AND biz_date <= ?", metricCode, start, end).
		Scan(&rows).Error
	max := 0.0
	for _, item := range rows {
		if item.WaterLevel != nil && *item.WaterLevel > max {
			max = *item.WaterLevel
		}
	}
	return round4(max)
}

func (s *Service) countAlertsByMetric(ctx context.Context, start, end time.Time, systems []string) map[string]int {
	type row struct {
		MetricCode string `gorm:"column:metric_code"`
		Count      int    `gorm:"column:count"`
	}
	var rows []row
	q := s.db.WithContext(ctx).Table("alert_record").
		Select("metric_code, count(*) AS count").
		Where("trigger_at >= ? AND trigger_at < ?", start, end.AddDate(0, 0, 1))
	q = applyReportScope(q, "system_code", systems)
	_ = q.Group("metric_code").Scan(&rows).Error
	out := make(map[string]int, len(rows))
	for _, item := range rows {
		out[item.MetricCode] = item.Count
	}
	return out
}

func applyReportScope(q *gorm.DB, column string, systems []string) *gorm.DB {
	if systems == nil {
		return q
	}
	if len(systems) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where(column+" IN ?", systems)
}

func normalizeSortField(field string) string {
	switch field {
	case "waterLevel", "riskScore", "alertCount", "growthRate", "avgValue", "mttaMinutes", "mttrMinutes":
		return field
	default:
		return "metricCode"
	}
}

func sortReportRows(rows []map[string]any, field string, desc bool) {
	field = normalizeSortField(field)
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i][field], rows[j][field]
		var less bool
		if av, ok := asFloat(a); ok {
			bv, _ := asFloat(b)
			less = av > bv
			if !desc {
				less = av < bv
			}
		} else {
			less = stringValue(a) < stringValue(b)
			if desc {
				less = stringValue(a) > stringValue(b)
			}
		}
		if asFloatEqual(a, b) {
			return stringValue(rows[i]["metricCode"]) < stringValue(rows[j]["metricCode"])
		}
		return less
	})
}

func rate(current, previous float64) any {
	if math.Abs(previous) < 1e-12 {
		return nil
	}
	return round4((current - previous) / math.Abs(previous))
}

func ratio(n, d float64) float64 {
	if d <= 0 {
		return 0
	}
	return round4(clamp(n/d, 0, 1))
}

func sumAlertCounts(values map[string]int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func daysInclusive(start, end time.Time) int {
	return int(clock.DayStart(end).Sub(clock.DayStart(start)).Hours()/24) + 1
}

func rowNumber(value any) float64 {
	n, _ := asFloat(value)
	return n
}

func numberValue(value any) float64 {
	return rowNumber(value)
}

func asFloat(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case *float64:
		if n != nil {
			return *n, true
		}
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case *int:
		if n != nil {
			return float64(*n), true
		}
	}
	return 0, false
}

func asFloatEqual(a, b any) bool {
	av, aok := asFloat(a)
	bv, bok := asFloat(b)
	return aok && bok && math.Abs(av-bv) < 1e-12
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func clamp(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
