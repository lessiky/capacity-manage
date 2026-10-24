package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"capacity-manage/backend/internal/alert"
	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pkg/scope"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/tradingday"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	store    *Store
	db       *gorm.DB
	az       *middleware.AuthZ
	params   *system.Params
	cal      *tradingday.Calendar
	log      *slog.Logger
	quality  *QualityService
	backfill *BackfillService
}

func NewHandler(store *Store, db *gorm.DB, az *middleware.AuthZ, params *system.Params, cal *tradingday.Calendar, log *slog.Logger) *Handler {
	return &Handler{
		store: store, db: db, az: az, params: params, cal: cal, log: log,
		quality: NewQualityService(db, params),
	}
}

func (h *Handler) SetBackfillService(service *BackfillService) {
	if h != nil {
		h.backfill = service
	}
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// scopedMetricCodes 数据权限过滤 metricCodes（FR-ADM-03）:
// 返回 (codes, ok); ok=false 表示已返回 403;
// codes=nil 不限制; 空切片表示无可见数据
func (h *Handler) scopedMetricCodes(c *gin.Context, reqCodes []string) ([]string, bool) {
	sys := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c))
	if sys == nil {
		return reqCodes, true
	}
	var visible []string
	q := scope.Apply(h.db.Model(&persistence.CapacityMetric{}), sys)
	if len(reqCodes) > 0 {
		q = q.Where("metric_code IN ?", reqCodes)
	}
	_ = q.Distinct().Pluck("metric_code", &visible).Error
	if len(reqCodes) > 0 {
		visSet := make(map[string]bool, len(visible))
		for _, v := range visible {
			visSet[v] = true
		}
		var exist []string
		_ = h.db.Model(&persistence.CapacityMetric{}).
			Where("metric_code IN ?", reqCodes).Pluck("metric_code", &exist).Error
		existSet := make(map[string]bool, len(exist))
		for _, e := range exist {
			existSet[e] = true
		}
		for _, cd := range reqCodes {
			if existSet[cd] && !visSet[cd] {
				scope.Forbidden(c, cd)
				return nil, false
			}
		}
	}
	return visible, true
}

// QueryRT 日内数据（数据权限: 仅限授权系统内指标）
// 兼容: 不带 page 参数返回数组(limit 上限, 旧客户端/脚本); 带 page 时返回 {items,total,page,pageSize}
func (h *Handler) QueryRT(c *gin.Context) {
	from, to := timeRange(c)
	codes, ok := h.scopedMetricCodes(c, splitQuery(c, "metricCodes"))
	if !ok {
		return
	}
	if codes != nil && len(codes) == 0 {
		resp.OK(c, []persistence.MetricValueRT{})
		return
	}
	page, perr := atoi2(c.Query("page"))
	if perr == nil && page > 0 {
		size := 100
		if v := c.Query("pageSize"); v != "" {
			if n, serr := atoi2(v); serr == nil && n > 0 && n <= 500 {
				size = n
			}
		}
		rows, total, err := h.store.QueryRTPage(c.Request.Context(), codes, from, to, page, size)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, resp.Page(rows, total, page, size))
		return
	}
	limit := 1000
	if v := c.Query("limit"); v != "" {
		if n, err := atoi2(v); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	rows, err := h.store.QueryRT(c.Request.Context(), codes, from, to, limit)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, rows)
}

// QueryDay 日数据（数据权限: 仅限授权系统内指标）
// 兼容: 不带 page 参数返回全量数组（旧客户端/脚本）; 带 page 时返回 {items,total,page,pageSize}
func (h *Handler) QueryDay(c *gin.Context) {
	from, to := timeRange(c)
	codes, ok := h.scopedMetricCodes(c, splitQuery(c, "metricCodes"))
	if !ok {
		return
	}
	if codes != nil && len(codes) == 0 {
		resp.OK(c, []persistence.MetricValueDay{})
		return
	}
	page, perr := atoi2(c.Query("page"))
	if perr == nil && page > 0 {
		size := 100
		if v := c.Query("pageSize"); v != "" {
			if n, serr := atoi2(v); serr == nil && n > 0 && n <= 500 {
				size = n
			}
		}
		rows, total, err := h.store.QueryDayPage(c.Request.Context(), codes, from, to, page, size)
		if err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, resp.Page(rows, total, page, size))
		return
	}
	rows, err := h.store.QueryDay(c.Request.Context(), codes, from, to)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, rows)
}

// Trend 指标趋势（日内/日粒度 + 参考线 + 缺数标记）
func (h *Handler) Trend(c *gin.Context) {
	code := c.Param("code")
	gran := c.DefaultQuery("granularity", "day") // rt/day
	var from, to time.Time
	if c.Query("from") != "" || c.Query("to") != "" {
		f, perr1 := time.Parse("2006-01-02", c.Query("from"))
		t, perr2 := time.Parse("2006-01-02", c.Query("to"))
		if perr1 != nil || perr2 != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "from/to 格式 YYYY-MM-DD"))
			return
		}
		if t.Before(f) {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "to 不得早于 from"))
			return
		}
		from = clock.DayStart(f)
		to = clock.DayStart(t).AddDate(0, 0, 1)
		if to.Sub(from) > 400*24*time.Hour {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "区间跨度须 ≤ 400 天"))
			return
		}
	} else {
		d, dterr := atoi2(c.DefaultQuery("days", "30"))
		if dterr != nil || d <= 0 || d > 400 {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "days 须为 1~400"))
			return
		}
		from = clock.DayStart(time.Now().AddDate(0, 0, -d))
		to = clock.DayStart(time.Now()).AddDate(0, 0, 1)
	}

	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&metric).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !scope.Contains(scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)), metric.SystemCode) {
		scope.Forbidden(c, metric.SystemCode)
		return
	}
	var series []struct {
		Ts            time.Time `json:"ts" gorm:"column:ts"`
		Value         *float64  `json:"value" gorm:"column:value"`
		IsAnomaly     bool      `json:"isAnomaly" gorm:"column:is_anomaly"`
		JudgeLevel    string    `json:"judgeLevel" gorm:"column:judge_level"`
		AnomalyReason string    `json:"anomalyReason" gorm:"column:anomaly_reason"`
	}
	var err error
	if gran == "rt" {
		err = h.db.Raw(`SELECT ts, clean_value AS value, is_anomaly, judge_level, anomaly_reason
			FROM metric_value_rt WHERE metric_code = ? AND ts >= ? AND ts < ?
			ORDER BY ts LIMIT 5000`, code, from, to).Scan(&series).Error
	} else {
		err = h.db.Raw(`SELECT biz_date AS ts, stat_value AS value, false AS is_anomaly, level AS judge_level
			FROM metric_value_day WHERE metric_code = ? AND biz_date >= ? AND biz_date < ?
			ORDER BY biz_date`, code, from, to).Scan(&series).Error
	}
	if err != nil {
		resp.Fail(c, err)
		return
	}
	missingDates := []string{}
	if gran != "rt" {
		var ds []time.Time
		_ = h.db.Model(&persistence.MetricValueDay{}).
			Where("metric_code = ? AND biz_date >= ? AND biz_date < ? AND level = 'missing'", code, from, to).
			Order("biz_date").Pluck("biz_date", &ds).Error
		for i := range ds {
			missingDates = append(missingDates, ds[i].Format("2006-01-02"))
		}
	}
	// 区间统计(按非空值; 极值记录最早出现时刻)
	type rangeStats struct {
		Count int        `json:"count"`
		Max   *float64   `json:"max"`
		MaxTs *time.Time `json:"maxTs"`
		Min   *float64   `json:"min"`
		MinTs *time.Time `json:"minTs"`
		Avg   *float64   `json:"avg"`
	}
	stats := rangeStats{}
	sum := 0.0
	for i := range series {
		v := series[i].Value
		if v == nil {
			continue
		}
		stats.Count++
		sum += *v
		if stats.Max == nil || *v > *stats.Max {
			stats.Max = v
			stats.MaxTs = &series[i].Ts
		}
		if stats.Min == nil || *v < *stats.Min {
			stats.Min = v
			stats.MinTs = &series[i].Ts
		}
	}
	if stats.Count > 0 {
		avg := sum / float64(stats.Count)
		stats.Avg = &avg
	}

	// 历史峰值(已落地; 无记录时懒性重算初始化)
	var peak persistence.MetricPeakStat
	var peaks *persistence.MetricPeakStat
	if err := h.db.Where("metric_code = ?", code).First(&peak).Error; err == nil {
		peaks = &peak
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		if rerr := h.store.RefreshPeaks(c.Request.Context(), code); rerr == nil {
			var p2 persistence.MetricPeakStat
			if err2 := h.db.Where("metric_code = ?", code).First(&p2).Error; err2 == nil {
				peaks = &p2
			}
		}
	} else {
		resp.Fail(c, err)
		return
	}

	resp.OK(c, gin.H{
		"metricCode": code, "unit": metric.Unit, "granularity": gran,
		"baseline": metric.Baseline, "warnLine": metric.WarnLine, "dangerLine": metric.DangerLine,
		"thresholdMode": metric.ThresholdMode, "judgeDirection": metric.JudgeDirection,
		"series": series, "points": len(series), "missingDates": missingDates,
		"stats": stats, "peaks": peaks,
	})
}

// RecalcPeaks 手工重算并落地历史峰值/谷值(运维/数据修正后使用)
func (h *Handler) RecalcPeaks(c *gin.Context) {
	code := c.Param("code")
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", code).First(&metric).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !scope.Contains(scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)), metric.SystemCode) {
		scope.Forbidden(c, metric.SystemCode)
		return
	}
	if err := h.store.RefreshPeaks(c.Request.Context(), code); err != nil {
		resp.Fail(c, err)
		return
	}
	var peak persistence.MetricPeakStat
	var peaks *persistence.MetricPeakStat
	if err := h.db.Where("metric_code = ?", code).First(&peak).Error; err == nil {
		peaks = &peak
	}
	middleware.Log(c, "data", "data.peaks.recalc", code, "")
	resp.OK(c, gin.H{"metricCode": code, "peaks": peaks})
}

// RunSummary 手工触发日汇总（幂等补数, 运维入口; 参数 daily_summary_time 的定时任务同逻辑）
func (h *Handler) RunSummary(c *gin.Context) {
	var req struct {
		BizDate string `json:"bizDate"`
	}
	_ = c.ShouldBindJSON(&req)
	anchor := clock.DayStart(time.Now()).AddDate(0, 0, -1)
	if req.BizDate != "" {
		t, perr := time.Parse("2006-01-02", req.BizDate)
		if perr != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "bizDate 格式 YYYY-MM-DD"))
			return
		}
		anchor = t
	}
	go RunDailySummary(context.Background(), h.db, h.log, h.params, h.cal, anchor)
	middleware.Log(c, "data", "data.summary.run", "", "bizDate="+anchor.Format("2006-01-02"))
	resp.OK(c, gin.H{"bizDate": anchor.Format("2006-01-02"), "message": "日汇总任务已启动(幂等), 稍后可查询日数据"})
}

// Correct 手工修正
func (h *Handler) Correct(c *gin.Context) {
	var req struct {
		MetricCode string   `json:"metricCode" binding:"required"`
		BizDate    string   `json:"bizDate" binding:"required"`
		Field      string   `json:"field" binding:"required,oneof=stat_value water_level"`
		NewValue   *float64 `json:"newValue" binding:"required"`
		Reason     string   `json:"reason" binding:"required,min=5"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	bd, err := time.Parse("2006-01-02", req.BizDate)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日期格式错误"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	if err := h.store.Correct(c.Request.Context(), req.MetricCode, bd, req.Field, req.NewValue, req.Reason, un); err != nil {
		resp.Fail(c, err)
		return
	}
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", req.MetricCode).First(&metric).Error; err == nil {
		_ = alert.New(h.db, h.params, nil).EvaluateAndEscalate(c.Request.Context(), &metric, bd)
	}
	middleware.Log(c, "data", "data.correct", req.MetricCode, "date="+req.BizDate+" field="+req.Field)
	resp.OK(c, gin.H{"refreshHint": "该日相关告警/报表数据已刷新, 请刷新页面查看"})
}

// ListCorrections 修正单检索
// 兼容: 不带 page 参数返回数组(limit 200, 旧客户端/脚本); 带 page 时返回 {items,total,page,pageSize}
func (h *Handler) ListCorrections(c *gin.Context) {
	q := h.db.Model(&persistence.DataCorrection{})
	if v := c.Query("metricCode"); v != "" {
		q = q.Where("metric_code = ?", v)
	}
	if sys := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)); sys != nil {
		q = q.Joins("JOIN capacity_metric m ON m.metric_code = data_correction.metric_code")
		q = scope.Apply(q, sys)
	}
	page, perr := atoi2(c.Query("page"))
	if perr == nil && page > 0 {
		size := 20
		if v := c.Query("pageSize"); v != "" {
			if n, serr := atoi2(v); serr == nil && n > 0 && n <= 200 {
				size = n
			}
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var items []persistence.DataCorrection
		if err := q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, resp.Page(items, total, page, size))
		return
	}
	var items []persistence.DataCorrection
	_ = q.Order("created_at DESC, id DESC").Limit(200).Find(&items).Error
	resp.OK(c, items)
}

// ListQuality returns persisted daily quality scores in the requested range.
func (h *Handler) ListQuality(c *gin.Context) {
	from, to := timeRange(c)
	metricCode := c.Query("metricCode")
	if metricCode != "" {
		codes, ok := h.scopedMetricCodes(c, []string{metricCode})
		if !ok {
			return
		}
		if len(codes) == 0 {
			resp.OK(c, []persistence.MetricQualityDay{})
			return
		}
	}
	q := h.db.Model(&persistence.MetricQualityDay{}).Where("biz_date >= ? AND biz_date < ?", from, to)
	if metricCode != "" {
		q = q.Where("metric_code = ?", metricCode)
	}
	if sys := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)); sys != nil {
		q = q.Where("system_code IN ?", sys)
	}
	if page, size, ok := resp.ParsePage(c, 100, 500); ok {
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.MetricQualityDay
		if err := q.Order("biz_date DESC, metric_code").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, resp.Page(rows, total, page, size))
		return
	}
	var rows []persistence.MetricQualityDay
	if err := q.Order("biz_date DESC, metric_code").Limit(5000).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, rows)
}

// EvaluateQuality recalculates and persists quality for one metric/range.
func (h *Handler) EvaluateQuality(c *gin.Context) {
	var req struct {
		MetricCode string `json:"metricCode" binding:"required"`
		From       string `json:"from" binding:"required"`
		To         string `json:"to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	codes, ok := h.scopedMetricCodes(c, []string{req.MetricCode})
	if !ok {
		return
	}
	if len(codes) == 0 {
		scope.Forbidden(c, req.MetricCode)
		return
	}
	from, err := time.ParseInLocation("2006-01-02", req.From, time.Local)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "from 格式须为 YYYY-MM-DD"))
		return
	}
	to, err := time.ParseInLocation("2006-01-02", req.To, time.Local)
	if err != nil || to.Before(from) {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "to 格式错误或早于 from"))
		return
	}
	if h.quality == nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "质量服务不可用"))
		return
	}
	result, err := h.quality.Evaluate(c.Request.Context(), req.MetricCode, from, to)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "data", "data.quality.evaluate", req.MetricCode,
		"from="+req.From+" to="+req.To)
	resp.OK(c, result)
}

// ListBackfills returns historical backfill runs for the visible metrics.
func (h *Handler) ListBackfills(c *gin.Context) {
	q := h.db.Model(&persistence.DataBackfillRun{})
	if v := c.Query("metricCode"); v != "" {
		q = q.Where("metric_code = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if sys := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)); sys != nil {
		q = q.Joins("JOIN capacity_metric m ON m.metric_code = data_backfill_run.metric_code").
			Where("m.system_code IN ?", sys)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := atoi2(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := atoi2(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	var rows []persistence.DataBackfillRun
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, resp.Page(rows, total, page, size))
}

func (h *Handler) GetBackfill(c *gin.Context) {
	id, err := atoi2(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "id 非法"))
		return
	}
	var run persistence.DataBackfillRun
	if err := h.db.First(&run, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "补采任务不存在"))
		return
	}
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", run.MetricCode).First(&metric).Error; err != nil ||
		!scope.Contains(scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)), metric.SystemCode) {
		scope.Forbidden(c, metric.SystemCode)
		return
	}
	var items []persistence.DataBackfillItem
	if err := h.db.Where("run_id = ?", run.ID).Order("biz_date").Find(&items).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"run": run, "items": items})
}

// RunBackfill starts one synchronous historical replay request. The service
// records per-day state so repeated requests stay idempotent at the data layer.
func (h *Handler) RunBackfill(c *gin.Context) {
	if h.backfill == nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "补采服务不可用"))
		return
	}
	var req BackfillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	var flow persistence.CollectFlow
	if err := h.db.First(&flow, req.FlowID).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	var metric persistence.CapacityMetric
	if err := h.db.Where("metric_code = ?", flow.MetricCode).First(&metric).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "指标不存在"))
		return
	}
	if !scope.Contains(scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)), metric.SystemCode) {
		scope.Forbidden(c, metric.SystemCode)
		return
	}
	if req.Operator == "" {
		username, _ := c.Get(middleware.CtxUsername)
		req.Operator, _ = username.(string)
	}
	result, err := h.backfill.Run(c.Request.Context(), req)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "data", "data.backfill.run", metric.MetricCode,
		"flow="+fmt.Sprint(flow.ID)+" from="+req.From+" to="+req.To)
	resp.OK(c, result)
}

func timeRange(c *gin.Context) (time.Time, time.Time) {
	now := clock.DayStart(time.Now())
	var from, to time.Time
	var err error
	if v := c.Query("from"); v != "" {
		from, err = time.Parse("2006-01-02", v)
		if err != nil {
			from = now.AddDate(0, 0, -1)
		}
	} else {
		from = now.AddDate(0, 0, -1)
	}
	if v := c.Query("to"); v != "" {
		if t, perr := time.Parse("2006-01-02", v); perr == nil {
			to = t.AddDate(0, 0, 1) // to 日期为含边界(次日 0 点)
		} else {
			to = now
		}
	} else {
		to = now
	}
	if to.IsZero() || to.Before(from) {
		to = from.AddDate(0, 0, 1)
	}
	return from, to
}

func splitQuery(c *gin.Context, name string) []string {
	v := c.Query(name)
	if v == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, s := range splitComma(v) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitComma(v string) []string {
	out := make([]string, 0, 4)
	cur := ""
	for _, r := range v {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func atoi2(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errAtoi
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

var errAtoi = resp.NewErr(resp.CodeBadRequest, "invalid int")
