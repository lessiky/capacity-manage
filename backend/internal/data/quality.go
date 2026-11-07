package data

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QualityInput is the normalized daily evidence used by the quality score.
// Counts are point-based; ValidDays/TimelyDays are day-based.
type QualityInput struct {
	RangeDays       int
	ValidDays       int
	TimelyDays      int
	TotalPoints     int
	AnomalyPoints   int
	DuplicatePoints int
	LatestBizDate   time.Time
	RangeEnd        time.Time
}

// QualityResult exposes the aggregate score and each daily row that was
// evaluated. The daily rows are also persisted by QualityService.
type QualityResult struct {
	Score         float64                        `json:"score"`
	Completeness  float64                        `json:"completeness"`
	Timeliness    float64                        `json:"timeliness"`
	AnomalyRate   float64                        `json:"anomalyRate"`
	DuplicateRate float64                        `json:"duplicateRate"`
	Freshness     float64                        `json:"freshness"`
	ValidPoints   int                            `json:"validPoints"`
	TotalPoints   int                            `json:"totalPoints"`
	Flags         []string                       `json:"flags"`
	Days          []persistence.MetricQualityDay `json:"days"`
}

// CalculateQuality computes a deterministic 0..1 score from completeness,
// timeliness, anomaly ratio, duplicate ratio and freshness. Any score below
// 0.8 receives explanatory flags for the prediction/report quality gate.
func CalculateQuality(in QualityInput) QualityResult {
	rangeDays := in.RangeDays
	if rangeDays <= 0 {
		rangeDays = 1
	}
	completeness := ratio(float64(in.ValidDays), float64(rangeDays))
	timeliness := ratio(float64(in.TimelyDays), float64(rangeDays))
	anomalyRate := ratio(float64(in.AnomalyPoints), float64(in.TotalPoints))
	duplicateRate := ratio(float64(in.DuplicatePoints), float64(in.TotalPoints))
	freshness := calculateFreshness(in.LatestBizDate, in.RangeEnd, rangeDays)

	score := 0.35*completeness +
		0.20*timeliness +
		0.20*(1-anomalyRate) +
		0.15*(1-duplicateRate) +
		0.10*freshness
	if in.TotalPoints == 0 || in.ValidDays == 0 {
		score = 0
	}
	score = roundQuality(score)

	// 初始化空切片, 保证无告警时序列化为 "[]" 而非 "null"。
	flags := make([]string, 0, 6)
	if in.TotalPoints == 0 || in.ValidDays == 0 {
		flags = append(flags, "no_data")
	}
	if in.ValidDays < rangeDays {
		flags = append(flags, "missing_days")
	}
	if in.TimelyDays < rangeDays {
		flags = append(flags, "late_days")
	}
	if anomalyRate > 0.1 {
		flags = append(flags, "anomaly_ratio")
	}
	if duplicateRate > 0 {
		flags = append(flags, "duplicates")
	}
	if freshness < 1 {
		flags = append(flags, "stale")
	}

	return QualityResult{
		Score: score, Completeness: completeness, Timeliness: timeliness,
		AnomalyRate: anomalyRate, DuplicateRate: duplicateRate, Freshness: freshness,
		ValidPoints: maxInt(0, in.TotalPoints-in.AnomalyPoints-in.DuplicatePoints),
		TotalPoints: in.TotalPoints, Flags: flags,
	}
}

type QualityService struct {
	db     *gorm.DB
	params *system.Params
	cal    *tradingday.Calendar
	now    func() time.Time
}

func NewQualityService(db *gorm.DB, params *system.Params, cals ...*tradingday.Calendar) *QualityService {
	svc := &QualityService{db: db, params: params, now: time.Now}
	if len(cals) > 0 && cals[0] != nil {
		svc.cal = cals[0]
	}
	return svc
}

func (s *QualityService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *QualityService) calendar() *tradingday.Calendar {
	if s == nil {
		return nil
	}
	if s.cal != nil {
		return s.cal
	}
	if s.db == nil {
		return nil
	}
	return tradingday.NewCalendar(s.db, nil, s.params)
}

// Evaluate calculates and upserts one quality row per inclusive business day.
func (s *QualityService) Evaluate(ctx context.Context, metricCode string, from, to time.Time) (QualityResult, error) {
	var metric persistence.CapacityMetric
	if err := s.db.WithContext(ctx).Where("metric_code = ?", metricCode).First(&metric).Error; err != nil {
		return QualityResult{}, err
	}
	flow, err := s.activeFlow(ctx, metricCode)
	if err != nil {
		return QualityResult{}, err
	}
	asOf := s.currentTime()
	if err := s.CleanupPremature(ctx, asOf, metricCode); err != nil {
		return QualityResult{}, err
	}
	start, end := normalizeRange(from, to)
	bizDates, err := s.qualityBizDates(ctx, flow, start, end, asOf)
	if err != nil {
		return QualityResult{}, err
	}
	if len(bizDates) == 0 {
		// 指定范围内没有该指标应生产数据的交易日，返回中性结果且不落质量行。
		return QualityResult{
			Score: 1, Completeness: 1, Timeliness: 1, Freshness: 1,
			Flags: []string{}, Days: []persistence.MetricQualityDay{},
		}, nil
	}
	rows := make([]persistence.MetricQualityDay, 0, len(bizDates))
	latest := time.Time{}
	totalPoints, anomalyPoints, duplicatePoints := 0, 0, 0
	validDays, timelyDays := 0, 0
	timelyHours := 24
	if s.params != nil {
		timelyHours = s.params.Int("quality_timely_hours", 24)
	}

	for _, bizDate := range bizDates {
		day, err := s.evaluateDay(ctx, &metric, flow, bizDate, timelyHours)
		if err != nil {
			return QualityResult{}, err
		}
		rows = append(rows, day)
		totalPoints += day.TotalPoints
		anomalyPoints += countFromRate(day.AnomalyRate, day.TotalPoints)
		duplicatePoints += countFromRate(day.DuplicateRate, day.TotalPoints)
		if day.ValidPoints > 0 {
			validDays++
			latest = bizDate
		}
		if day.Timeliness >= 1 && day.ValidPoints > 0 {
			timelyDays++
		}
	}

	result := CalculateQuality(QualityInput{
		RangeDays: len(bizDates), ValidDays: validDays, TimelyDays: timelyDays,
		TotalPoints: totalPoints, AnomalyPoints: anomalyPoints, DuplicatePoints: duplicatePoints,
		LatestBizDate: latest, RangeEnd: bizDates[len(bizDates)-1],
	})
	result.Days = rows
	for i := range rows {
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "metric_code"}, {Name: "biz_date"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"system_code", "score", "completeness", "timeliness", "anomaly_rate",
				"duplicate_rate", "freshness", "valid_points", "total_points", "flags", "evaluated_at",
			}),
		}).Create(&rows[i]).Error; err != nil {
			return QualityResult{}, err
		}
	}
	return result, nil
}

func (s *QualityService) activeFlow(ctx context.Context, metricCode string) (*persistence.CollectFlow, error) {
	var flow persistence.CollectFlow
	err := s.db.WithContext(ctx).
		Where("metric_code = ? AND status = ? AND enabled = ?", metricCode, "active", true).
		Order("id").First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &flow, nil
}

func (s *QualityService) qualityBizDates(ctx context.Context, flow *persistence.CollectFlow,
	start, end, asOf time.Time) ([]time.Time, error) {
	naturalDates := make([]time.Time, 0, daysBetween(start, end))
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		naturalDates = append(naturalDates, d)
	}

	if flow == nil || flow.ScheduleMode != "trading_day" {
		return naturalDates, nil
	}
	mode := flow.BizDateMode
	if mode == "" {
		mode = "natural"
	}
	if mode != "calendar" && mode != "natural" {
		return naturalDates, nil
	}

	market := flow.TradingMarket
	if market == "" {
		market = "SH"
	}
	var calendars []persistence.TradingDayCalendar
	if err := s.db.WithContext(ctx).
		Where("market = ? AND cal_date >= ? AND cal_date <= ?", market, start, end).
		Find(&calendars).Error; err != nil {
		return nil, err
	}
	tradingByDate := make(map[string]bool, len(calendars))
	for i := range calendars {
		tradingByDate[calendars[i].CalDate.Format("2006-01-02")] = calendars[i].IsTrading
	}

	out := make([]time.Time, 0, len(naturalDates))
	for _, d := range naturalDates {
		isTrading, ok := tradingByDate[d.Format("2006-01-02")]
		if !ok {
			isTrading = d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
		}
		if isTrading {
			out = append(out, d)
		}
	}
	due, dueKnown, err := s.latestDueBizDate(ctx, flow, asOf)
	if err != nil {
		return nil, err
	}
	if dueKnown {
		filtered := out[:0]
		for _, d := range out {
			if d.After(due) {
				continue
			}
			if !flow.CreatedAt.IsZero() && flow.CreatedAt.After(d) {
				continue
			}
			filtered = append(filtered, d)
		}
		out = filtered
	}
	return out, nil
}

func (s *QualityService) latestDueBizDate(ctx context.Context, flow *persistence.CollectFlow,
	asOf time.Time) (time.Time, bool, error) {
	if flow == nil || flow.ScheduleMode != "trading_day" {
		return time.Time{}, false, nil
	}
	cal := s.calendar()
	if cal == nil {
		return time.Time{}, false, nil
	}
	market := flow.TradingMarket
	if market == "" {
		market = "SH"
	}
	window := flow.WindowTime
	if window == "" {
		window = "17:30:00"
	}
	lastRun, err := cal.PrevTriggerOnOrBefore(ctx, market, window, asOf)
	if err != nil {
		return time.Time{}, false, err
	}
	bizDate, err := cal.BizDateForRun(ctx, market, flow.BizDateMode, lastRun)
	if err != nil {
		return time.Time{}, false, err
	}
	return bizDate, true, nil
}

func (s *QualityService) scheduledRunForBizDate(ctx context.Context, flow *persistence.CollectFlow,
	bizDate time.Time) (time.Time, bool, error) {
	if flow == nil || flow.ScheduleMode != "trading_day" {
		return time.Time{}, false, nil
	}
	cal := s.calendar()
	if cal == nil {
		return time.Time{}, false, nil
	}
	market := flow.TradingMarket
	if market == "" {
		market = "SH"
	}
	window := flow.WindowTime
	if window == "" {
		window = "17:30:00"
	}
	from := clock.DayStart(bizDate).Add(-time.Nanosecond)
	if flow.BizDateMode == "prev_day" || flow.BizDateMode == "calendar" {
		from = clock.DayStart(bizDate).AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	run, err := cal.NextTriggerAfter(ctx, market, window, from)
	if err != nil {
		return time.Time{}, false, err
	}
	return run, true, nil
}

// CleanupPremature removes no-data quality rows for business dates whose
// scheduled collection window has not completed yet. Without this guard a
// 00:15 previous-day flow can be scored on Saturday for Friday even though
// Friday's data is not collected until the next trading-day run.
func (s *QualityService) CleanupPremature(ctx context.Context, asOf time.Time, metricCodes ...string) error {
	var rows []persistence.MetricQualityDay
	q := s.db.WithContext(ctx).Where("valid_points <= 0 AND total_points <= 0")
	if len(metricCodes) > 0 {
		q = q.Where("metric_code IN ?", metricCodes)
	}
	if err := q.Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var flows []persistence.CollectFlow
	if err := s.db.WithContext(ctx).
		Where("status = ? AND enabled = ? AND schedule_mode = ?", "active", true, "trading_day").
		Order("id").
		Find(&flows).Error; err != nil {
		return err
	}
	flowByMetric := make(map[string]*persistence.CollectFlow, len(flows))
	for i := range flows {
		if _, ok := flowByMetric[flows[i].MetricCode]; !ok {
			flowByMetric[flows[i].MetricCode] = &flows[i]
		}
	}
	for i := range rows {
		flow := flowByMetric[rows[i].MetricCode]
		if flow == nil {
			continue
		}
		due, known, err := s.latestDueBizDate(ctx, flow, asOf)
		if err != nil || !known || !rows[i].BizDate.After(due) {
			continue
		}
		if err := s.db.WithContext(ctx).
			Where("metric_code = ? AND biz_date = ?", rows[i].MetricCode, rows[i].BizDate).
			Delete(&persistence.MetricQualityDay{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *QualityService) evaluateDay(ctx context.Context, metric *persistence.CapacityMetric,
	flow *persistence.CollectFlow, bizDate time.Time, timelyHours int) (persistence.MetricQualityDay, error) {
	dayStart := clock.DayStart(bizDate)
	dayEnd := dayStart.AddDate(0, 0, 1)
	type rtRow struct {
		CleanValue *float64  `gorm:"column:clean_value"`
		IsAnomaly  bool      `gorm:"column:is_anomaly"`
		CreatedAt  time.Time `gorm:"column:created_at"`
	}
	var points []rtRow
	if err := s.db.WithContext(ctx).Raw(`SELECT clean_value, is_anomaly, created_at
		FROM metric_value_rt WHERE metric_code = ? AND ts >= ? AND ts < ?`,
		metric.MetricCode, dayStart, dayEnd).Scan(&points).Error; err != nil {
		return persistence.MetricQualityDay{}, err
	}
	var stored persistence.MetricValueDay
	hasDay := s.db.WithContext(ctx).Where("metric_code = ? AND biz_date = ?", metric.MetricCode, dayStart).
		First(&stored).Error == nil

	total := len(points)
	anomaly := 0
	valid := 0
	duplicates := 0
	seen := map[string]bool{}
	var firstAt time.Time
	for i := range points {
		key := points[i].CreatedAt.UTC().Format(time.RFC3339Nano)
		if seen[key] && !points[i].CreatedAt.IsZero() {
			duplicates++
		}
		seen[key] = true
		if points[i].IsAnomaly {
			anomaly++
			continue
		}
		if points[i].CleanValue != nil {
			valid++
		}
		if firstAt.IsZero() || (!points[i].CreatedAt.IsZero() && points[i].CreatedAt.Before(firstAt)) {
			firstAt = points[i].CreatedAt
		}
	}
	if hasDay && stored.StatValue != nil && stored.Level != "missing" {
		if valid == 0 {
			valid = stored.PointCount
			total += stored.PointCount
		} else if stored.PointCount > total {
			total = stored.PointCount
		}
	}
	if hasDay && !stored.UpdatedAt.IsZero() && (firstAt.IsZero() || stored.UpdatedAt.Before(firstAt)) {
		firstAt = stored.UpdatedAt
	}
	timely := 0.0
	if valid > 0 && !firstAt.IsZero() {
		// prev_day/calendar 流程会在业务日之后的凌晨采集, 因此及时性窗口
		// 从该业务日对应的计划触发时刻起算, 跨周末也按下一交易日计算。
		deadline := dayEnd.Add(time.Duration(timelyHours) * time.Hour)
		if run, known, err := s.scheduledRunForBizDate(ctx, flow, bizDate); err != nil {
			return persistence.MetricQualityDay{}, err
		} else if known {
			deadline = run.Add(time.Duration(timelyHours) * time.Hour)
		}
		if !firstAt.After(deadline) {
			timely = 1
		}
	}
	anomalyRate := ratio(float64(anomaly), float64(total))
	duplicateRate := ratio(float64(duplicates), float64(total))
	var latest time.Time
	if valid > 0 {
		latest = dayStart
	}
	dayQuality := CalculateQuality(QualityInput{
		RangeDays: 1, ValidDays: boolInt(valid > 0), TimelyDays: boolInt(timely == 1),
		TotalPoints: total, AnomalyPoints: anomaly, DuplicatePoints: duplicates,
		LatestBizDate: latest, RangeEnd: dayStart,
	})
	flags, _ := json.Marshal(dayQuality.Flags)
	return persistence.MetricQualityDay{
		MetricCode: metric.MetricCode, BizDate: dayStart, SystemCode: metric.SystemCode,
		Score: dayQuality.Score, Completeness: dayQuality.Completeness,
		Timeliness: dayQuality.Timeliness, AnomalyRate: anomalyRate,
		DuplicateRate: duplicateRate, Freshness: dayQuality.Freshness,
		ValidPoints: valid, TotalPoints: total, Flags: string(flags), EvaluatedAt: time.Now(),
	}, nil
}

func normalizeRange(from, to time.Time) (time.Time, time.Time) {
	start := clock.DayStart(from)
	end := clock.DayStart(to)
	if end.Before(start) {
		end = start
	}
	return start, end
}

func daysBetween(from, to time.Time) int {
	return int(clock.DayStart(to).Sub(clock.DayStart(from)).Hours()/24) + 1
}

func ratio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	v := numerator / denominator
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return roundQuality(v)
}

func calculateFreshness(latest, end time.Time, rangeDays int) float64 {
	if latest.IsZero() || rangeDays <= 0 {
		return 0
	}
	staleDays := int(clock.DayStart(end).Sub(clock.DayStart(latest)).Hours() / 24)
	if staleDays < 0 {
		staleDays = 0
	}
	fresh := 1 - float64(staleDays)/float64(rangeDays)
	if fresh < 0 {
		return 0
	}
	return roundQuality(fresh)
}

func roundQuality(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func countFromRate(rate float64, total int) int {
	if total <= 0 || rate <= 0 {
		return 0
	}
	n := int(math.Round(rate * float64(total)))
	if n > total {
		return total
	}
	return n
}
