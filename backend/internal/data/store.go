package data

import (
	"context"
	"fmt"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/judge"
	"capacity-manage/backend/internal/metricagg"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/tradingday"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Point 日内数据点
type Point struct {
	MetricCode    string
	Ts            time.Time
	RawValue      *float64
	CleanValue    *float64
	IsAnomaly     bool
	AnomalyReason string
	Source        string
}

// Store 日内/日数据存储（FR-DAY-01/02/03/08）
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// SavePoints 批量 upsert 日内数据点（按 指标+时间戳 去重, FR-COL-22）
func (s *Store) SavePoints(ctx context.Context, pts []Point) error {
	if len(pts) == 0 {
		return nil
	}
	rows := make([]persistence.MetricValueRT, 0, len(pts))
	for _, p := range pts {
		src := p.Source
		if src == "" {
			src = "collect"
		}
		rows = append(rows, persistence.MetricValueRT{
			MetricCode: p.MetricCode, Ts: p.Ts,
			RawValue: p.RawValue, CleanValue: p.CleanValue,
			IsAnomaly: p.IsAnomaly, AnomalyReason: p.AnomalyReason,
			Source: src,
		})
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "metric_code"}, {Name: "ts"}},
		DoUpdates: clause.AssignmentColumns([]string{"clean_value", "is_anomaly", "anomaly_reason", "source"}),
	}).Create(&rows).Error
}

// BackfillJudgeLevel 回写判定结果
func (s *Store) BackfillJudgeLevel(metricCode string, ts time.Time, level string) error {
	return s.db.Model(&persistence.MetricValueRT{}).
		Where("metric_code = ? AND ts = ?", metricCode, ts).
		Update("judge_level", level).Error
}

// QueryRT 日内数据查询
func (s *Store) QueryRT(ctx context.Context, metricCodes []string, from, to time.Time, limit int) ([]persistence.MetricValueRT, error) {
	q := s.db.Model(&persistence.MetricValueRT{}).Where("ts >= ? AND ts < ?", from, to)
	if len(metricCodes) > 0 {
		q = q.Where("metric_code IN ?", metricCodes)
	}
	if limit <= 0 {
		limit = 1000
	}
	var rows []persistence.MetricValueRT
	err := q.Order("metric_code, ts").Limit(limit).Find(&rows).Error
	return rows, err
}

// QueryDay 日数据查询
func (s *Store) QueryDay(ctx context.Context, metricCodes []string, from, to time.Time) ([]persistence.MetricValueDay, error) {
	q := s.db.Model(&persistence.MetricValueDay{}).Where("biz_date >= ? AND biz_date < ?", from, to)
	if len(metricCodes) > 0 {
		q = q.Where("metric_code IN ?", metricCodes)
	}
	var rows []persistence.MetricValueDay
	err := q.Order("metric_code, biz_date").Find(&rows).Error
	return rows, err
}

// QueryRTPage 日内数据分页查询（page 从 1 开始）
func (s *Store) QueryRTPage(ctx context.Context, metricCodes []string, from, to time.Time, page, pageSize int) ([]persistence.MetricValueRT, int64, error) {
	q := s.db.Model(&persistence.MetricValueRT{}).Where("ts >= ? AND ts < ?", from, to)
	if len(metricCodes) > 0 {
		q = q.Where("metric_code IN ?", metricCodes)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []persistence.MetricValueRT
	err := q.Order("metric_code, ts").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// QueryDayPage 日数据分页查询（page 从 1 开始）
func (s *Store) QueryDayPage(ctx context.Context, metricCodes []string, from, to time.Time, page, pageSize int) ([]persistence.MetricValueDay, int64, error) {
	q := s.db.Model(&persistence.MetricValueDay{}).Where("biz_date >= ? AND biz_date < ?", from, to)
	if len(metricCodes) > 0 {
		q = q.Where("metric_code IN ?", metricCodes)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []persistence.MetricValueDay
	err := q.Order("metric_code, biz_date").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// SummaryDeps 日汇总依赖: judge 计算水位/级别(FR-DAY-08), cal 推导交易日业务日(FR-DAY-02),
// alerts 汇总指标阈值告警联动(日值超线生成告警/恢复)
type SummaryDeps struct {
	Judge  *judge.Judge
	Cal    *tradingday.Calendar
	Alerts *alert.Service
}

// PickBizDate 确定指标业务日（数据日期）: prev_day 取 anchor 前一自然日,
// calendar 取 anchor 之前(不含)的上一交易日, 其余取 anchor 自然日。推导失败回退自然日, 不阻断汇总。
func PickBizDate(ctx context.Context, cal *tradingday.Calendar, fl *persistence.CollectFlow, anchor time.Time) time.Time {
	anchor = clock.DayStart(anchor)
	if fl == nil {
		return anchor
	}
	switch fl.BizDateMode {
	case "prev_day":
		return anchor.AddDate(0, 0, -1)
	case "calendar":
		if cal != nil {
			market := fl.TradingMarket
			if market == "" {
				market = "SH"
			}
			if td, err := cal.PrevTradingDay(ctx, market, anchor); err == nil {
				return td
			}
		}
		return anchor
	default:
		return anchor
	}
}

type dayAgg struct {
	MetricCode string   `gorm:"column:metric_code"`
	MaxV       *float64 `gorm:"column:max_v"`
	MinV       *float64 `gorm:"column:min_v"`
	AvgV       *float64 `gorm:"column:avg_v"`
	SumV       *float64 `gorm:"column:sum_v"`
	LastV      *float64 `gorm:"column:last_v"`
	MaxLevel   string   `gorm:"column:max_level"`
	Cnt        int      `gorm:"column:cnt"`
}

// aggregateDay 按业务日窗口聚合日内数据(方言无关: 拉取当日明细在 Go 内聚合,
// 与测试库 sqlite 兼容; 单日数据量有限, 无需 PG 端聚合)
func (s *Store) aggregateDay(ctx context.Context, dayStart, dayEnd time.Time) ([]dayAgg, error) {
	type rtRow struct {
		MetricCode string    `gorm:"column:metric_code"`
		Ts         time.Time `gorm:"column:ts"`
		Value      float64   `gorm:"column:value"`
		Level      string    `gorm:"column:judge_level"`
	}
	var rows []rtRow
	err := s.db.Raw(`SELECT metric_code, ts, clean_value AS value, judge_level
		FROM metric_value_rt WHERE ts >= ? AND ts < ? AND clean_value IS NOT NULL
		ORDER BY metric_code, ts`, dayStart, dayEnd).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	type acc struct {
		maxV, minV, sumV, lastV float64
		cnt                     int
		levelRank               int
	}
	accs := map[string]*acc{}
	var order []string
	for i := range rows {
		r := &rows[i]
		a, ok := accs[r.MetricCode]
		if !ok {
			a = &acc{maxV: r.Value, minV: r.Value}
			accs[r.MetricCode] = a
			order = append(order, r.MetricCode)
		}
		a.cnt++
		a.sumV += r.Value
		if r.Value > a.maxV {
			a.maxV = r.Value
		}
		if r.Value < a.minV {
			a.minV = r.Value
		}
		a.lastV = r.Value // ts 升序, 最后一条即最新值
		if lr := levelRank(r.Level); lr > a.levelRank {
			a.levelRank = lr
		}
	}
	aggs := make([]dayAgg, 0, len(order))
	for _, code := range order {
		a := accs[code]
		avg := a.sumV / float64(a.cnt)
		aggs = append(aggs, dayAgg{
			MetricCode: code, MaxV: &a.maxV, MinV: &a.minV,
			AvgV: &avg, SumV: &a.sumV, LastV: &a.lastV,
			MaxLevel: levelOfRank(a.levelRank), Cnt: a.cnt,
		})
	}
	return aggs, nil
}

func levelOfRank(r int) string {
	switch {
	case r >= 3:
		return "danger"
	case r == 2:
		return "warn"
	default:
		return "normal"
	}
}

func levelRank(l string) int {
	switch l {
	case "danger":
		return 3
	case "warn":
		return 2
	case "missing":
		return 1
	default:
		return 0
	}
}

// SummarizeDay 每日汇总（FR-DAY-02/08, 幂等）:
// 对每个启用指标按其业务日汇总日内数据 → 日数据(max/min/avg/sum/last + 水位 + 最高级别)
func (s *Store) SummarizeDay(ctx context.Context, anchor time.Time, dep *SummaryDeps) (done, missing int, err error) {
	anchor = clock.DayStart(anchor)
	var metrics []persistence.CapacityMetric
	// 仅普通指标参与日汇总(group/aggregate 为虚拟节点, 无日内数据)
	if err := s.db.Where("status = 'active' AND metric_kind = 'normal'").Find(&metrics).Error; err != nil {
		return 0, 0, err
	}
	if len(metrics) == 0 {
		return 0, 0, nil
	}
	// 活动流程(metric → flow): 用于判定交易日历径
	var flows []persistence.CollectFlow
	if err := s.db.Where("status = 'active' AND enabled = ?", true).Find(&flows).Error; err != nil {
		return 0, 0, err
	}
	flowMap := make(map[string]*persistence.CollectFlow, len(flows))
	for i := range flows {
		flowMap[flows[i].MetricCode] = &flows[i]
	}
	// 按业务日分组
	groups := map[time.Time][]int{}
	for i := range metrics {
		biz := PickBizDate(ctx, dep.Cal, flowMap[metrics[i].MetricCode], anchor)
		groups[biz] = append(groups[biz], i)
	}
	now := time.Now()
	var summarized []string
	for biz, idxs := range groups {
		aggs, aerr := s.aggregateDay(ctx, biz, biz.AddDate(0, 0, 1))
		if aerr != nil {
			return done, missing, aerr
		}
		aggMap := map[string]dayAgg{}
		for _, a := range aggs {
			aggMap[a.MetricCode] = a
		}
		var existingRows []persistence.MetricValueDay
		if eerr := s.db.Where("biz_date = ?", biz).Find(&existingRows).Error; eerr != nil {
			return done, missing, eerr
		}
		existingMap := make(map[string]persistence.MetricValueDay, len(existingRows))
		for i := range existingRows {
			existingMap[existingRows[i].MetricCode] = existingRows[i]
		}
		for _, i := range idxs {
			m := &metrics[i]
			a, hasData := aggMap[m.MetricCode]
			if existing, ok := existingMap[m.MetricCode]; ok &&
				(existing.Corrected || (!hasData && hasStoredDayValue(existing))) {
				done++
				continue
			}
			row := buildDayRow(m, a, hasData, biz, now, dep)
			if hasData {
				done++
				summarized = append(summarized, m.MetricCode)
			} else {
				missing++
			}
			row.UpdatedAt = now
			if err := s.upsertDayRow(&row); err != nil {
				return done, missing, fmt.Errorf("day upsert %s: %w", m.MetricCode, err)
			}
		}
	}
	// 历史峰谷维护(best effort: 失败时读取路径惰性初始化会重算)
	if len(summarized) > 0 {
		_ = s.RefreshPeaks(ctx, summarized...)
	}
	// 汇总指标: 主统计模块跨子指标聚合 → 日值(水位/级别) + 阈值告警联动(须在普通指标日值落库后执行)
	if err := s.summarizeAggregates(ctx, anchor, dep); err != nil {
		return done, missing, err
	}
	return done, missing, nil
}

// SummarizeMetricBizDate 仅刷新一个指标的业务日日值。
// 手工补采会在落点后立即调用, 使目标业务日无需等待下一次全量日汇总。
func (s *Store) SummarizeMetricBizDate(ctx context.Context, metric *persistence.CapacityMetric,
	bizDate time.Time, dep *SummaryDeps) error {
	if metric == nil {
		return nil
	}
	biz := clock.DayStart(bizDate)
	aggs, err := s.aggregateDay(ctx, biz, biz.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	var agg dayAgg
	hasData := false
	for i := range aggs {
		if aggs[i].MetricCode == metric.MetricCode {
			agg, hasData = aggs[i], true
			break
		}
	}

	var existing persistence.MetricValueDay
	found := s.db.Where("metric_code = ? AND biz_date = ?", metric.MetricCode, biz).
		First(&existing).Error == nil
	if found && (existing.Corrected || (!hasData && hasStoredDayValue(existing))) {
		return nil
	}

	row := buildDayRow(metric, agg, hasData, biz, time.Now(), dep)
	if err := s.upsertDayRow(&row); err != nil {
		return fmt.Errorf("day upsert %s: %w", metric.MetricCode, err)
	}
	if hasData {
		_ = s.RefreshPeaks(ctx, metric.MetricCode)
	}
	return nil
}

func buildDayRow(metric *persistence.CapacityMetric, agg dayAgg, hasData bool,
	bizDate, now time.Time, dep *SummaryDeps) persistence.MetricValueDay {
	row := persistence.MetricValueDay{
		MetricCode: metric.MetricCode, BizDate: bizDate,
		Level: "normal", Source: "collect",
	}
	if !hasData {
		row.Level = "missing"
		row.PointCount = 0
		return row
	}
	row.DayMax, row.DayMin, row.DayAvg, row.DayLast = agg.MaxV, agg.MinV, agg.AvgV, agg.LastV
	row.PointCount = agg.Cnt
	switch metric.StatMode {
	case "max":
		row.StatValue = agg.MaxV
	case "min":
		row.StatValue = agg.MinV
	case "sum":
		row.StatValue = agg.SumV
	case "avg":
		row.StatValue = agg.AvgV
	default:
		row.StatValue = agg.LastV
	}
	row.Level = agg.MaxLevel
	if dep != nil && dep.Judge != nil {
		jr := dep.Judge.Evaluate(metric, row.StatValue)
		if jr.WaterLevel != nil {
			row.WaterLevel = jr.WaterLevel
		}
		if levelRank(jr.Level) > levelRank(row.Level) {
			row.Level = jr.Level
		}
	}
	return row
}

func hasStoredDayValue(row persistence.MetricValueDay) bool {
	return row.StatValue != nil || row.PointCount > 0 || (row.Level != "" && row.Level != "missing")
}

func (s *Store) upsertDayRow(row *persistence.MetricValueDay) error {
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "metric_code"}, {Name: "biz_date"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"day_max", "day_min", "day_avg", "day_last", "stat_value",
			"water_level", "level", "point_count", "updated_at",
		}),
	}).Create(row).Error
}

// aggregateLeaves 取汇总指标下全部叶子指标(统计模块指标): 直接子指标 + 子指标分组的子指标,
// 顺序与 metric.Modules 视图一致(先直接子指标后分组子指标, 组内按编码)
func (s *Store) aggregateLeaves(aggCode string) []persistence.CapacityMetric {
	var leaves []persistence.CapacityMetric
	_ = s.db.Where("parent_code = ? AND metric_kind = 'normal'", aggCode).Order("metric_code").Find(&leaves).Error
	var groupCodes []string
	_ = s.db.Model(&persistence.CapacityMetric{}).
		Where("parent_code = ? AND metric_kind = 'group'", aggCode).
		Pluck("metric_code", &groupCodes).Error
	if len(groupCodes) > 0 {
		var subs []persistence.CapacityMetric
		_ = s.db.Where("parent_code IN ? AND metric_kind = 'normal'", groupCodes).Order("metric_code").Find(&subs).Error
		leaves = append(leaves, subs...)
	}
	return leaves
}

// summarizeAggregates 汇总指标日值 + 阈值告警联动(幂等):
//
//	日值 = 主统计模块(MainModule, 空=首个模块)子指标业务日日值的跨指标聚合值,
//	聚合方式与 metric 模块视图一致(metricagg: sum/max/min/avg/weighted_avg);
//	日值行 source='aggregate', 水位/级别由 Judge 判定; 超线/恢复交 alert.Service 处理。
func (s *Store) summarizeAggregates(ctx context.Context, biz time.Time, dep *SummaryDeps) error {
	var aggs []persistence.CapacityMetric
	if err := s.db.Where("status = 'active' AND metric_kind = 'aggregate'").Order("metric_code").Find(&aggs).Error; err != nil {
		return err
	}
	now := time.Now()
	for i := range aggs {
		a := &aggs[i]
		leaves := s.aggregateLeaves(a.MetricCode)
		if len(leaves) == 0 {
			continue
		}
		// 主统计模块: 空=首个叶子的模块键(与模块视图"首个模块"口径一致)
		moduleKey := a.MainModule
		if moduleKey == "" {
			moduleKey = leaves[0].StatModule
			if moduleKey == "" {
				moduleKey = leaves[0].MetricCode
			}
		}
		modLeaves := make([]persistence.CapacityMetric, 0, len(leaves))
		for j := range leaves {
			k := leaves[j].StatModule
			if k == "" {
				k = leaves[j].MetricCode
			}
			if k == moduleKey {
				modLeaves = append(modLeaves, leaves[j])
			}
		}
		if len(modLeaves) == 0 {
			continue // 主模块无子指标(如模块标识已变更), 跳过不阻断
		}
		// 业务日日值: 叶子指标 + 其权重指标
		codes := make([]string, 0, len(modLeaves)*2)
		for j := range modLeaves {
			codes = append(codes, modLeaves[j].MetricCode)
			if modLeaves[j].WeightMetricCode != nil && *modLeaves[j].WeightMetricCode != "" {
				codes = append(codes, *modLeaves[j].WeightMetricCode)
			}
		}
		type valRow struct {
			MetricCode string   `gorm:"column:metric_code"`
			Value      *float64 `gorm:"column:value"`
		}
		var vrows []valRow
		if err := s.db.Raw(`SELECT metric_code, stat_value AS value FROM metric_value_day
			WHERE metric_code IN ? AND biz_date = ?`, codes, biz).Scan(&vrows).Error; err != nil {
			return err
		}
		vals := make(map[string]*float64, len(vrows))
		for j := range vrows {
			vals[vrows[j].MetricCode] = vrows[j].Value
		}
		statModes := make([]string, len(modLeaves))
		weights := make([]*string, len(modLeaves))
		pairs := make([]metricagg.Pair, len(modLeaves))
		n := 0
		for j, l := range modLeaves {
			statModes[j] = l.StatMode
			weights[j] = l.WeightMetricCode
			p := metricagg.Pair{Value: vals[l.MetricCode]}
			if l.WeightMetricCode != nil && *l.WeightMetricCode != "" {
				p.Weight = vals[*l.WeightMetricCode]
			}
			pairs[j] = p
			if p.Value != nil {
				n++
			}
		}
		v := metricagg.Aggregate(metricagg.Mode(statModes, weights), pairs)
		if v == nil {
			continue
		}
		jr := judge.Result{Level: "normal"}
		if dep.Judge != nil {
			jr = dep.Judge.Evaluate(a, v)
		}
		row := persistence.MetricValueDay{
			MetricCode: a.MetricCode, BizDate: biz,
			StatValue: v, WaterLevel: jr.WaterLevel, Level: jr.Level,
			PointCount: n, Source: "aggregate",
		}
		row.UpdatedAt = now
		if err := s.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "metric_code"}, {Name: "biz_date"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"stat_value", "water_level", "level", "point_count", "source", "updated_at",
			}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("aggregate day upsert %s: %w", a.MetricCode, err)
		}
		if dep.Alerts != nil {
			dep.Alerts.OnThreshold(ctx, a, jr, biz)
		}
	}
	return nil
}

// Correct 手工修正（FR-DAY-05: 原因必填/原值保留/审计）
func (s *Store) Correct(ctx context.Context, metricCode string, bizDate time.Time, field string, newValue *float64, reason, operator string) error {
	var row persistence.MetricValueDay
	if err := s.db.Where("metric_code = ? AND biz_date = ?", metricCode, bizDate).First(&row).Error; err != nil {
		return fmt.Errorf("日数据不存在: %w", err)
	}
	snapshot, _ := jsonMarshal(row)
	snap := string(snapshot)
	updates := map[string]any{
		"corrected": true, "correct_reason": reason,
		"corrected_by": operator, "corrected_at": time.Now(),
		"original_snapshot": &snap,
	}
	if field == "stat_value" {
		updates["stat_value"] = *newValue
	} else if field == "water_level" {
		updates["water_level"] = *newValue
	} else {
		return fmt.Errorf("不支持的修正字段: %s", field)
	}
	if err := s.db.Model(&row).Updates(updates).Error; err != nil {
		return err
	}
	if err := s.db.Create(&persistence.DataCorrection{
		MetricCode: metricCode, BizDate: bizDate, Field: field,
		OldValue: fieldOld(row, field), NewValue: newValue,
		Reason: reason, Operator: operator,
	}).Error; err != nil {
		return err
	}
	// 修正可能改变历史峰谷(stat_value) → 重算落地(best effort)
	_ = s.RefreshPeaks(ctx, metricCode)
	return nil
}

// RefreshPeaks 重算并落地指定指标的历史峰值/谷值(基于日数据 stat_value 序列, 与日粒度趋势同口径)。
// 极值取出现的最早日期; 日数据无 stat_value 的指标不产生记录
// (历史数据被删除且无剩余数据时, 保留已有记录不清除)。
func (s *Store) RefreshPeaks(ctx context.Context, codes ...string) error {
	if len(codes) == 0 {
		return nil
	}
	type extremeRow struct {
		V *float64  `gorm:"column:v"`
		D time.Time `gorm:"column:d"`
	}
	now := time.Now()
	for _, code := range codes {
		var maxRows, minRows []extremeRow
		if err := s.db.Raw(`SELECT stat_value AS v, biz_date AS d
			FROM metric_value_day
			WHERE metric_code = ? AND stat_value IS NOT NULL
			ORDER BY stat_value DESC, biz_date ASC LIMIT 1`, code).Scan(&maxRows).Error; err != nil {
			return err
		}
		if err := s.db.Raw(`SELECT stat_value AS v, biz_date AS d
			FROM metric_value_day
			WHERE metric_code = ? AND stat_value IS NOT NULL
			ORDER BY stat_value ASC, biz_date ASC LIMIT 1`, code).Scan(&minRows).Error; err != nil {
			return err
		}
		if len(maxRows) == 0 {
			continue // 无日数据 stat_value 则不产生/不清除记录
		}
		maxRow, minRow := maxRows[0], minRows[0]
		if err := s.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "metric_code"}},
			DoUpdates: clause.AssignmentColumns([]string{"max_value", "max_biz_date", "updated_at"}),
		}).Create(&persistence.MetricPeakStat{
			MetricCode: code, MaxValue: maxRow.V, MaxBizDate: &maxRow.D, UpdatedAt: now,
		}).Error; err != nil {
			return err
		}
		if err := s.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "metric_code"}},
			DoUpdates: clause.AssignmentColumns([]string{"min_value", "min_biz_date", "updated_at"}),
		}).Create(&persistence.MetricPeakStat{
			MetricCode: code, MinValue: minRow.V, MinBizDate: &minRow.D, UpdatedAt: now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func fieldOld(row persistence.MetricValueDay, field string) *float64 {
	if field == "stat_value" {
		return row.StatValue
	}
	return row.WaterLevel
}
