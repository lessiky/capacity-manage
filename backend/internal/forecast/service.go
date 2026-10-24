package forecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"capacity-manage/backend/internal/persistence"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ModelVersion = "stat-v1.0"

// Service loads daily observations, runs the deterministic engine and
// persists one reproducible result per active metric and horizon.
type Service struct {
	db      *gorm.DB
	now     func() time.Time
	workers int
	ai      AIClient
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, now: time.Now, workers: 4}
}

func (s *Service) SetWorkers(n int) {
	if n > 0 {
		s.workers = n
	}
}

// ErrNoMatchedMetrics 表示部分重算的筛选条件没有命中任何活动指标。
var ErrNoMatchedMetrics = errors.New("所选范围没有可预测的指标")

// RunFilter 限定一次预测批次覆盖的业务系统与指标。两个字段都为空时表示全量重算。
type RunFilter struct {
	SystemCodes []string
	MetricCodes []string
}

func (f RunFilter) Empty() bool {
	return len(f.SystemCodes) == 0 && len(f.MetricCodes) == 0
}

// RunSummary 同时返回批次记录与本次筛选范围内的产出计数。
type RunSummary struct {
	Run                 *persistence.ForecastRun
	ScopeMetricsTotal   int
	ScopeMetricsDone    int
	ScopeMetricsSkipped int
}

func (s *Service) RunDaily(ctx context.Context, runDate time.Time) error {
	_, err := s.RunDailyResult(ctx, runDate)
	return err
}

// RunDailyResult is the API-facing form of RunDaily. It is kept separate so
// callers following the WBS interface can still use RunDaily(ctx, date) error.
func (s *Service) RunDailyResult(ctx context.Context, runDate time.Time) (*persistence.ForecastRun, error) {
	out, err := s.RunScopedResult(ctx, runDate, RunFilter{})
	if err != nil {
		return nil, err
	}
	return out.Run, nil
}

// RunScopedResult 执行一次全量或按系统/指标过滤的统计预测。部分重算只覆盖选中
// 指标的结果行，批次级覆盖率仍按该 run_date 下已存在的全部结果重新统计。
func (s *Service) RunScopedResult(ctx context.Context, runDate time.Time, filter RunFilter) (*RunSummary, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("forecast service is not configured")
	}
	runDate = startDay(runDate)
	now := s.now()

	var metrics []persistence.CapacityMetric
	q := s.db.WithContext(ctx).Where("status = 'active' AND metric_kind <> 'group'")
	if len(filter.SystemCodes) > 0 {
		q = q.Where("system_code IN ?", filter.SystemCodes)
	}
	if len(filter.MetricCodes) > 0 {
		q = q.Where("metric_code IN ?", filter.MetricCodes)
	}
	if err := q.
		Order("system_code, metric_code").
		Find(&metrics).Error; err != nil {
		return nil, err
	}
	if !filter.Empty() && len(metrics) == 0 {
		return nil, ErrNoMatchedMetrics
	}
	allCodes := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		allCodes = append(allCodes, metric.MetricCode)
	}

	run, err := s.beginRun(ctx, runDate, now, allCodes, filter.Empty())
	if err != nil {
		return nil, err
	}
	run.DataEndDate = runDate
	if err := s.db.Model(run).Updates(map[string]any{
		"data_end_date": run.DataEndDate,
		"model_version": ModelVersion,
		"ai_enabled":    s.AIEnabled(),
	}).Error; err != nil {
		return &RunSummary{Run: run}, err
	}

	if err := expireDueEvents(ctx, s.db, runDate); err != nil {
		s.failRun(run, err)
		return &RunSummary{Run: run}, err
	}
	// 事件集合按最大预测窗口 90 天取，覆盖 30/90 两种 horizon。
	events, err := s.activeEvents(ctx, runDate, 90)
	if err != nil {
		s.failRun(run, err)
		return &RunSummary{Run: run}, err
	}
	eventVersion := eventSetVersion(events)
	scopeDone, scopeSkipped := 0, 0
	for start := 0; start < len(metrics); start += 1000 {
		end := min(len(metrics), start+1000)
		batch := metrics[start:end]
		codes := make([]string, 0, len(batch))
		for _, metric := range batch {
			codes = append(codes, metric.MetricCode)
		}
		values, err := s.loadValues(ctx, codes, runDate)
		if err != nil {
			s.failRun(run, err)
			return &RunSummary{Run: run}, err
		}
		qualities, err := s.loadQuality(ctx, codes, runDate)
		if err != nil {
			s.failRun(run, err)
			return &RunSummary{Run: run}, err
		}
		results, adjustments, done, skipped, err := s.buildBatch(ctx, batch, values, qualities, runDate, eventVersion, events)
		if err != nil {
			s.failRun(run, err)
			return &RunSummary{Run: run}, err
		}
		saved, err := s.saveBatch(ctx, run.ID, results)
		if err != nil {
			s.failRun(run, err)
			return &RunSummary{Run: run}, err
		}
		if err := s.saveAIAdjustments(ctx, run.ID, saved, adjustments); err != nil {
			s.failRun(run, err)
			return &RunSummary{Run: run}, err
		}
		scopeDone += done
		scopeSkipped += skipped
	}
	if err := s.refreshRunCounters(ctx, run); err != nil {
		s.failRun(run, err)
		return &RunSummary{Run: run, ScopeMetricsTotal: len(metrics), ScopeMetricsDone: scopeDone, ScopeMetricsSkipped: scopeSkipped}, err
	}
	finished := s.now()
	run.Status = "success"
	run.FinishedAt = &finished
	if err := s.db.WithContext(ctx).Model(run).Updates(map[string]any{
		"status": "success", "finished_at": finished, "error_msg": "",
	}).Error; err != nil {
		return &RunSummary{Run: run, ScopeMetricsTotal: len(metrics), ScopeMetricsDone: scopeDone, ScopeMetricsSkipped: scopeSkipped}, err
	}
	return &RunSummary{
		Run:                 run,
		ScopeMetricsTotal:   len(metrics),
		ScopeMetricsDone:    scopeDone,
		ScopeMetricsSkipped: scopeSkipped,
	}, nil
}

func (s *Service) beginRun(ctx context.Context, runDate, now time.Time, metricCodes []string, full bool) (*persistence.ForecastRun, error) {
	var run persistence.ForecastRun
	err := s.db.WithContext(ctx).Where("run_date = ?", runDate).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		run = persistence.ForecastRun{
			RunDate: runDate, ModelVersion: ModelVersion, DataEndDate: runDate,
			Status: "running", StartedAt: &now, CreatedAt: now,
			AIEnabled: s.AIEnabled(),
		}
		if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
			return nil, err
		}
		return &run, nil
	}
	if err != nil {
		return nil, err
	}
	if full {
		if err := s.db.WithContext(ctx).Where("run_id = ?", run.ID).Delete(&persistence.ForecastResult{}).Error; err != nil {
			return nil, err
		}
	} else if len(metricCodes) > 0 {
		if err := s.db.WithContext(ctx).
			Where("run_id = ? AND metric_code IN ?", run.ID, metricCodes).
			Delete(&persistence.ForecastResult{}).Error; err != nil {
			return nil, err
		}
	}
	run.ModelVersion = ModelVersion
	run.DataEndDate = runDate
	run.Status = "running"
	run.StartedAt = &now
	run.FinishedAt = nil
	if full {
		run.MetricsTotal = 0
		run.MetricsDone = 0
		run.MetricsSkipped = 0
	}
	run.ErrorMsg = ""
	run.AIEnabled = s.AIEnabled()
	updates := map[string]any{
		"model_version": ModelVersion, "data_end_date": runDate, "status": "running",
		"started_at": now, "finished_at": nil, "error_msg": "", "ai_enabled": s.AIEnabled(),
	}
	if full {
		updates["metrics_total"] = 0
		updates["metrics_done"] = 0
		updates["metrics_skipped"] = 0
	}
	if err := s.db.WithContext(ctx).Model(&run).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Service) loadValues(ctx context.Context, codes []string, endDate time.Time) (map[string][]persistence.MetricValueDay, error) {
	var rows []persistence.MetricValueDay
	from := endDate.AddDate(0, 0, -181)
	if err := s.db.WithContext(ctx).
		Where("metric_code IN ? AND biz_date >= ? AND biz_date <= ?", codes, from, endDate).
		Order("metric_code, biz_date").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string][]persistence.MetricValueDay, len(codes))
	for _, row := range rows {
		out[row.MetricCode] = append(out[row.MetricCode], row)
	}
	return out, nil
}

func (s *Service) loadQuality(ctx context.Context, codes []string, endDate time.Time) (map[string][]persistence.MetricQualityDay, error) {
	var rows []persistence.MetricQualityDay
	from := endDate.AddDate(0, 0, -181)
	if err := s.db.WithContext(ctx).
		Where("metric_code IN ? AND biz_date >= ? AND biz_date <= ?", codes, from, endDate).
		Order("metric_code, biz_date").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string][]persistence.MetricQualityDay, len(codes))
	for _, row := range rows {
		out[row.MetricCode] = append(out[row.MetricCode], row)
	}
	return out, nil
}

// statPlan 是某个指标在某个 horizon 上的统计预测中间结果，供主循环串行施加
// 事件因子与 AI 修正（AI 涉及网络调用，不能放进并行 worker）。
type statPlan struct {
	Metric   persistence.CapacityMetric
	Series   Series
	Horizon  int
	Linear   ForecastResult
	Growth   ForecastResult
	Backtest map[string]float64
	Quality  float64
}

// buildBatch 并行完成统计预测（纯计算），AI/事件修正由 buildBatch 的主循环
// 串行施加，保证确定性并避免并发写共享状态。
func (s *Service) buildBatch(ctx context.Context, metrics []persistence.CapacityMetric,
	values map[string][]persistence.MetricValueDay,
	qualities map[string][]persistence.MetricQualityDay,
	dataEnd time.Time, eventVersion string,
	events []persistence.ForecastEvent) ([]persistence.ForecastResult, []AIAdjustmentRecord, int, int, error) {
	type planResult struct {
		idx   int
		plans []statPlan
	}
	jobs := make(chan int)
	planCh := make(chan planResult, len(metrics))
	workers := s.workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(metrics) && len(metrics) > 0 {
		workers = len(metrics)
	}
	for i := 0; i < workers; i++ {
		go func() {
			for idx := range jobs {
				planCh <- planResult{
					idx:   idx,
					plans: buildStatPlans(metrics[idx], values[metrics[idx].MetricCode], qualities[metrics[idx].MetricCode], dataEnd),
				}
			}
		}()
	}
	go func() {
		for i := range metrics {
			jobs <- i
		}
		close(jobs)
	}()

	plans := make([][]statPlan, len(metrics))
	for i := 0; i < len(metrics); i++ {
		res := <-planCh
		plans[res.idx] = res.plans
	}

	out := make([]persistence.ForecastResult, 0, len(metrics)*2)
	adjustments := make([]AIAdjustmentRecord, 0, len(metrics))
	done, skipCount := 0, 0
	for idx := range metrics {
		anySuccess, anySkipped := false, false
		for _, plan := range plans[idx] {
			best := SelectBestModel([]ForecastResult{plan.Linear, plan.Growth}, plan.Backtest)
			if !plan.Series.QualityKnown {
				best = skipped(best.ModelType, StatusInsufficientQuality, plan.Horizon, "缺少数据质量分，不输出确定性 ETA")
			}
			result, record := s.applyAdjustments(ctx, plan.Metric, plan.Series, best, dataEnd, events, plan.Quality)
			if result.Status == StatusSuccess {
				anySuccess = true
			} else {
				anySkipped = true
			}
			// 事件版本只记到事件实际覆盖的指标: 未覆盖时留空, 便于前端与报表
			// 区分「本指标受事件影响」与「批次里存在事件」两种情况。
			// ApplyEventImpact 仅在事件真正命中该指标时才写入 EventFactorVersion。
			version := ""
			if result.EventFactorVersion != "" {
				version = eventVersion
			}
			row := toPersistenceResult(plan.Metric, plan.Horizon, result, plan.Series, dataEnd, version)
			out = append(out, row)
			if record != nil {
				record.MetricCode = plan.Metric.MetricCode
				record.HorizonDays = plan.Horizon
				adjustments = append(adjustments, *record)
			}
		}
		if anySuccess {
			done++
		}
		if anySkipped {
			skipCount++
		}
	}
	return out, adjustments, done, skipCount, nil
}

// buildStatPlans 产出指标的 30/90 天统计预测中间结果（纯计算，可并行）。
func buildStatPlans(metric persistence.CapacityMetric, valueRows []persistence.MetricValueDay,
	qualityRows []persistence.MetricQualityDay, dataEnd time.Time) []statPlan {
	points := make([]Point, 0, len(valueRows))
	for _, row := range valueRows {
		if row.StatValue == nil || row.Level == "missing" || row.Level == "anomaly" {
			continue
		}
		points = append(points, Point{BizDate: startDay(row.BizDate), Value: *row.StatValue})
	}
	qualityByDay := make(map[string]float64, len(qualityRows))
	for _, row := range qualityRows {
		qualityByDay[startDay(row.BizDate).Format("2006-01-02")] = row.Score
	}
	plans := make([]statPlan, 0, 2)
	for _, horizon := range []int{30, 90} {
		windowStart := dataEnd.AddDate(0, 0, -(horizon - 1))
		windowPoints := pointsInWindow(points, windowStart, dataEnd)
		quality, qualityKnown := qualityInWindow(qualityByDay, windowStart, dataEnd)
		series := Series{
			MetricCode: metric.MetricCode, JudgeDirection: metric.JudgeDirection,
			WarnLine: metric.WarnLine, DangerLine: metric.DangerLine,
			Points: windowPoints, QualityScore: quality, QualityKnown: qualityKnown,
		}
		plans = append(plans, statPlan{
			Metric: metric, Series: series, Horizon: horizon,
			Linear: ForecastLinear(series, horizon), Growth: ForecastGrowth(series, horizon),
			Backtest: EvaluateBacktest(series, 7), Quality: quality,
		})
	}
	return plans
}

// saveBatch 写入批次结果并返回带自增 id 的行，供 AI 调节记录关联。
func (s *Service) saveBatch(ctx context.Context, runID int64, rows []persistence.ForecastResult) ([]persistence.ForecastResult, error) {
	if len(rows) > 0 {
		for i := range rows {
			rows[i].RunID = runID
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "run_id"}, {Name: "metric_code"}, {Name: "horizon_days"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"model_type", "status", "eta_warn_date", "eta_danger_date", "risk_score",
				"confidence", "slope", "intercept", "residual_stddev", "data_quality",
				"event_factor_version", "explanation", "curve", "input_digest", "created_at",
			}),
		}).Create(&rows).Error; err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// refreshRunCounters 按批次内实际存在的结果行重算覆盖率。done 表示至少一个窗口
// 预测成功，skipped 表示至少一个窗口未产出确定性结果，二者可以同时命中同一指标。
func (s *Service) refreshRunCounters(ctx context.Context, run *persistence.ForecastRun) error {
	if run == nil {
		return nil
	}
	var total, done, skipped int64
	baseQuery := func() *gorm.DB {
		return s.db.WithContext(ctx).Table("forecast_result").Where("run_id = ?", run.ID)
	}
	if err := baseQuery().Select("COUNT(DISTINCT metric_code)").Scan(&total).Error; err != nil {
		return err
	}
	if err := baseQuery().Where("status = ?", StatusSuccess).Select("COUNT(DISTINCT metric_code)").Scan(&done).Error; err != nil {
		return err
	}
	if err := baseQuery().Where("status <> ?", StatusSuccess).Select("COUNT(DISTINCT metric_code)").Scan(&skipped).Error; err != nil {
		return err
	}
	run.MetricsTotal = int(total)
	run.MetricsDone = int(done)
	run.MetricsSkipped = int(skipped)
	return s.db.WithContext(ctx).Model(&persistence.ForecastRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"metrics_total":   run.MetricsTotal,
		"metrics_done":    run.MetricsDone,
		"metrics_skipped": run.MetricsSkipped,
	}).Error
}

// saveAIAdjustments 把每个结果对应的 AI 修正前后快照写入 forecast_ai_adjustment。
// 未发生 AI 调用（未启用或统计降级）的结果不写记录，便于统计真实 AI 命中率；
// 但会先清掉本批次结果的旧记录，避免 AI 关闭后重算仍展示上一轮的 AI 元数据。
func (s *Service) saveAIAdjustments(ctx context.Context, runID int64, saved []persistence.ForecastResult,
	records []AIAdjustmentRecord) error {
	ids := make([]int64, 0, len(saved))
	for _, row := range saved {
		if row.ID != 0 {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) > 0 {
		if err := s.db.WithContext(ctx).
			Where("result_id IN ?", ids).
			Delete(&persistence.ForecastAIAdjustment{}).Error; err != nil {
			return err
		}
	}
	if len(records) == 0 {
		return nil
	}
	byKey := make(map[string]int64, len(saved))
	for _, row := range saved {
		byKey[row.MetricCode+"|"+fmt.Sprintf("%d", row.HorizonDays)] = row.ID
	}
	rows := make([]persistence.ForecastAIAdjustment, 0, len(records))
	for _, rec := range records {
		resultID, ok := byKey[rec.MetricCode+"|"+fmt.Sprintf("%d", rec.HorizonDays)]
		if !ok || resultID == 0 {
			continue
		}
		etaShift, slope := rec.ETAShiftDays, rec.SlopeMultiplier
		intercept, confidence := rec.InterceptAdjPct, rec.ConfidenceAdj
		latency := rec.LatencyMS
		rows = append(rows, persistence.ForecastAIAdjustment{
			ResultID: resultID, RunID: runID, MetricCode: rec.MetricCode, HorizonDays: rec.HorizonDays,
			AIEnabled: true, Status: rec.Status, Model: rec.Model,
			SlopeMultiplier: &slope, InterceptAdjPct: &intercept, ConfidenceAdj: &confidence,
			Rationale: rec.Rationale, BaseSnapshot: rec.BaseSnapshot, AdjustedSnapshot: rec.AdjustedSnapshot,
			ETAShiftDays: &etaShift, AdvisoryOnly: rec.AdvisoryOnly, LatencyMS: &latency, ErrMsg: rec.ErrMsg,
			CreatedAt: time.Now(),
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "result_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"run_id", "metric_code", "horizon_days", "ai_enabled", "status", "model",
			"slope_multiplier", "intercept_adj_pct", "confidence_adj", "rationale",
			"base_snapshot", "adjusted_snapshot", "eta_shift_days", "advisory_only",
			"latency_ms", "error_msg", "created_at",
		}),
	}).Create(&rows).Error
}

func toPersistenceResult(metric persistence.CapacityMetric, horizon int, result ForecastResult,
	series Series, dataEnd time.Time, eventVersion string) persistence.ForecastResult {
	curve, _ := json.Marshal(result.Curve)
	digest := inputDigest(metric, series, horizon, dataEnd)
	return persistence.ForecastResult{
		RunID:              0, // assigned by saveBatch caller before insert
		MetricCode:         metric.MetricCode,
		HorizonDays:        horizon,
		ModelType:          result.ModelType,
		Status:             result.Status,
		ETAWarnDate:        result.ETAWarnDate,
		ETADangerDate:      result.ETADangerDate,
		RiskScore:          floatPtr(result.RiskScore),
		Confidence:         floatPtr(result.Confidence),
		Slope:              floatPtr(result.Slope),
		Intercept:          floatPtr(result.Intercept),
		ResidualStd:        floatPtr(result.ResidualStd),
		DataQuality:        floatPtr(series.QualityScore),
		EventFactorVersion: eventVersion,
		Explanation:        result.Explanation,
		Curve:              string(curve),
		InputDigest:        digest,
		CreatedAt:          time.Now(),
	}
}

func (s *Service) failRun(run *persistence.ForecastRun, err error) {
	if run == nil || s.db == nil {
		return
	}
	now := s.now()
	_ = s.db.Model(run).Updates(map[string]any{
		"status": "failed", "error_msg": err.Error(), "finished_at": now,
	}).Error
}

func (s *Service) eventVersion(ctx context.Context, dataEnd time.Time) string {
	var n int64
	err := s.db.WithContext(ctx).Model(&persistence.ForecastEvent{}).
		Where("status = 'confirmed' AND start_date <= ? AND (end_date IS NULL OR end_date >= ?)", dataEnd, dataEnd).
		Count(&n).Error
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("events-%d", n)
}

func pointsInWindow(points []Point, from, to time.Time) []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		if !p.BizDate.Before(from) && !p.BizDate.After(to) {
			out = append(out, p)
		}
	}
	return out
}

func qualityInWindow(values map[string]float64, from, to time.Time) (float64, bool) {
	var sum float64
	count := 0
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if value, ok := values[day.Format("2006-01-02")]; ok {
			sum += value
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return round4(sum / float64(count)), true
}

func inputDigest(metric persistence.CapacityMetric, series Series, horizon int, dataEnd time.Time) string {
	payload := map[string]any{
		"metric": metric.MetricCode, "direction": metric.JudgeDirection,
		"warn": metric.WarnLine, "danger": metric.DangerLine,
		"horizon": horizon, "dataEnd": dataEnd.Format("2006-01-02"),
		"quality": series.QualityScore, "points": series.Points,
		"modelVersion": ModelVersion,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func floatPtr(v float64) *float64 { return &v }

func (s *Service) latestRuns(ctx context.Context, limit int) ([]persistence.ForecastRun, error) {
	var runs []persistence.ForecastRun
	if limit <= 0 {
		limit = 30
	}
	err := s.db.WithContext(ctx).Order("run_date DESC, id DESC").Limit(limit).Find(&runs).Error
	return runs, err
}

// SortForecastResults is used by handlers and tests to keep risk output
// stable across databases with different NULL ordering.
func SortForecastResults(rows []persistence.ForecastResult) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := 0.0, 0.0
		if rows[i].RiskScore != nil {
			ri = *rows[i].RiskScore
		}
		if rows[j].RiskScore != nil {
			rj = *rows[j].RiskScore
		}
		if ri != rj {
			return ri > rj
		}
		return rows[i].MetricCode < rows[j].MetricCode
	})
}
