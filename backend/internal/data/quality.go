package data

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/system"

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

	var flags []string
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
}

func NewQualityService(db *gorm.DB, params *system.Params) *QualityService {
	return &QualityService{db: db, params: params}
}

// Evaluate calculates and upserts one quality row per inclusive business day.
func (s *QualityService) Evaluate(ctx context.Context, metricCode string, from, to time.Time) (QualityResult, error) {
	var metric persistence.CapacityMetric
	if err := s.db.WithContext(ctx).Where("metric_code = ?", metricCode).First(&metric).Error; err != nil {
		return QualityResult{}, err
	}
	start, end := normalizeRange(from, to)
	days := daysBetween(start, end)
	rows := make([]persistence.MetricQualityDay, 0, days)
	latest := time.Time{}
	totalPoints, anomalyPoints, duplicatePoints := 0, 0, 0
	validDays, timelyDays := 0, 0
	timelyHours := 24
	if s.params != nil {
		timelyHours = s.params.Int("quality_timely_hours", 24)
	}

	for i := 0; i < days; i++ {
		bizDate := start.AddDate(0, 0, i)
		day, err := s.evaluateDay(ctx, &metric, bizDate, timelyHours)
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
		RangeDays: days, ValidDays: validDays, TimelyDays: timelyDays,
		TotalPoints: totalPoints, AnomalyPoints: anomalyPoints, DuplicatePoints: duplicatePoints,
		LatestBizDate: latest, RangeEnd: end,
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

func (s *QualityService) evaluateDay(ctx context.Context, metric *persistence.CapacityMetric,
	bizDate time.Time, timelyHours int) (persistence.MetricQualityDay, error) {
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
		if firstAt.Sub(dayStart) <= time.Duration(timelyHours)*time.Hour {
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
