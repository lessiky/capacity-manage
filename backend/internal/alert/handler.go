package alert

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/system"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	az     *middleware.AuthZ
	params *system.Params
	log    *slog.Logger
	cipher ConfigCipher
	now    func() time.Time
}

func NewHandler(db *gorm.DB, az *middleware.AuthZ, params *system.Params, log *slog.Logger, cipher ...ConfigCipher) *Handler {
	h := &Handler{db: db, az: az, params: params, log: log, now: time.Now}
	if len(cipher) > 0 {
		h.cipher = cipher[0]
	}
	return h
}

func (h *Handler) load(c *gin.Context) (*persistence.AlertRecord, bool) {
	var rec persistence.AlertRecord
	if err := h.db.First(&rec, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "告警不存在"))
		return nil, false
	}
	return &rec, true
}

// scopeSystems 数据权限: 返回用户可见系统编码; nil=不限制(平台管理员)
func (h *Handler) scopeSystems(c *gin.Context) []string {
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	h.az.EnsureFresh()
	if h.az.IsPlatformAdmin(un) {
		return nil
	}
	codes := []string{}
	_ = h.db.Table("sys_data_scope s").
		Joins("JOIN sys_user u ON u.id = s.user_id").
		Joins("LEFT JOIN sys_user_role ur ON ur.user_id = u.id").
		Joins("LEFT JOIN sys_role r ON r.id = ur.role_id").
		Where("u.username = ?", un).
		Where("s.user_id IS NOT NULL OR r.status = 'active'").
		Distinct().Pluck("s.system_code", &codes).Error
	return codes
}

func applyScope(q *gorm.DB, codes []string) *gorm.DB {
	if codes == nil {
		return q
	}
	if len(codes) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where("system_code IN ?", codes)
}

func (h *Handler) List(c *gin.Context) {
	q := h.db.Model(&persistence.AlertRecord{})
	if v := c.Query("level"); v != "" {
		q = q.Where("level = ?", v)
	}
	if v := c.Query("systemCode"); v != "" {
		q = q.Where("system_code = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("alertType"); v != "" {
		q = q.Where("alert_type = ?", v)
	}
	if v := c.Query("metricCode"); v != "" {
		q = q.Where("metric_code = ?", v)
	}
	if v := c.Query("keyword"); v != "" {
		q = q.Where("alert_no LIKE ? OR metric_code LIKE ? OR system_code LIKE ?",
			"%"+v+"%", "%"+v+"%", "%"+v+"%")
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			q = q.Where("trigger_at >= ?", t)
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			q = q.Where("trigger_at < ?", t.AddDate(0, 0, 1))
		}
	}
	q = applyScope(q, h.scopeSystems(c))
	var total int64
	_ = q.Count(&total).Error
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := atoi3(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := atoi3(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	var items []persistence.AlertRecord
	_ = q.Order("trigger_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error
	resp.OK(c, resp.Page(items, total, page, size))
}

// Get 告警详情（FR-MON-13 + 契约 §11: 含指标趋势片段与处理轨迹）
func (h *Handler) Get(c *gin.Context) {
	rec, ok := h.load(c)
	if !ok {
		return
	}
	var levelHistory []persistence.AlertLevelHistory
	_ = h.db.Where("alert_id = ?", rec.ID).Order("changed_at, id").Find(&levelHistory).Error
	resp.OK(c, gin.H{
		"alert":        rec,
		"timeline":     BuildTimelineWithHistory(rec, levelHistory),
		"trendSnippet": h.trendSnippet(rec),
	})
}

// trendSnippet 触发日前 7 日(日粒度) + 触发日日内数据点
func (h *Handler) trendSnippet(rec *persistence.AlertRecord) gin.H {
	dayStart := clock.DayStart(rec.TriggerAt)
	from := dayStart.AddDate(0, 0, -7)
	type dayRow struct {
		BizDate   time.Time `json:"bizDate" gorm:"column:biz_date"`
		StatValue *float64  `json:"statValue" gorm:"column:stat_value"`
		Level     string    `json:"level" gorm:"column:level"`
	}
	var days []dayRow
	_ = h.db.Raw(`SELECT biz_date, stat_value, level FROM metric_value_day
		WHERE metric_code = ? AND biz_date >= ? AND biz_date < ? ORDER BY biz_date`,
		rec.MetricCode, from, dayStart.AddDate(0, 0, 1)).Scan(&days).Error
	type rtRow struct {
		Ts         time.Time `json:"ts" gorm:"column:ts"`
		CleanValue *float64  `json:"cleanValue" gorm:"column:clean_value"`
		JudgeLevel string    `json:"judgeLevel" gorm:"column:judge_level"`
	}
	var rt []rtRow
	_ = h.db.Raw(`SELECT ts, clean_value, judge_level FROM metric_value_rt
		WHERE metric_code = ? AND ts >= ? AND ts < ? ORDER BY ts LIMIT 500`,
		rec.MetricCode, dayStart, dayStart.AddDate(0, 0, 1)).Scan(&rt).Error
	return gin.H{"day": days, "rt": rt}
}

// TimelineItem 处理轨迹节点（触发/认领/备注/恢复/关闭）
type TimelineItem struct {
	Time     string         `json:"time"`
	Event    string         `json:"event"`
	User     string         `json:"user,omitempty"`
	Detail   string         `json:"detail,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// BuildTimeline 由告警记录构造处理轨迹（纯函数, 便于单测）
func BuildTimeline(rec *persistence.AlertRecord) []TimelineItem {
	return BuildTimelineWithHistory(rec, nil)
}

// BuildTimelineWithHistory adds immutable level-change evidence to the alert
// handling timeline. Escalation and downgrade suggestions are first-class
// events so operators can explain why the current level was reached.
func BuildTimelineWithHistory(rec *persistence.AlertRecord, history []persistence.AlertLevelHistory) []TimelineItem {
	items := []TimelineItem{}
	lv := "P" + rec.Level[1:]
	if fv := rec.FirstValue; fv != nil {
		items = append(items, TimelineItem{
			Time: fmtRFC(rec.TriggerAt), Event: "trigger",
			Detail: fmt.Sprintf("触发%s告警: 当前值 %.6g, 阈值 %.6g, 水位 %s",
				lv, *fv, deref6(rec.Threshold), pct(rec.WaterLevel)),
		})
	} else {
		items = append(items, TimelineItem{
			Time: fmtRFC(rec.TriggerAt), Event: "trigger",
			Detail: "触发" + lv + "告警（数据异常类）",
		})
	}
	for _, h := range history {
		event, detail := timelineLevelEvent(h)
		var evidence map[string]any
		if strings.TrimSpace(h.Evidence) != "" {
			_ = json.Unmarshal([]byte(h.Evidence), &evidence)
		}
		items = append(items, TimelineItem{
			Time: fmtRFC(h.ChangedAt), Event: event, User: h.Operator,
			Detail: detail, Evidence: evidence,
		})
	}
	if rec.ClaimedAt != nil {
		items = append(items, TimelineItem{
			Time: fmtRFC(*rec.ClaimedAt), Event: "claim",
			User: rec.ClaimedBy, Detail: "认领告警",
		})
	}
	notes, _ := parseNotes(rec.HandleNote)
	for _, n := range notes {
		items = append(items, TimelineItem{
			Time:   asString(n["time"]),
			Event:  "note",
			User:   asString(n["user"]),
			Detail: asString(n["note"]),
		})
	}
	if rec.RecoveredAt != nil {
		items = append(items, TimelineItem{
			Time: fmtRFC(*rec.RecoveredAt), Event: "recover",
			Detail: "指标回落至阈值以下, 告警恢复",
		})
	}
	if rec.ClosedAt != nil {
		items = append(items, TimelineItem{
			Time: fmtRFC(*rec.ClosedAt), Event: "close",
			User: rec.ClosedBy, Detail: "关闭: " + rec.CloseResult,
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Time < items[j].Time })
	return items
}

func timelineLevelEvent(h persistence.AlertLevelHistory) (string, string) {
	switch h.Action {
	case "downgrade_suggestion":
		return "downgrade_suggestion", fmt.Sprintf("降级建议 %s → %s（需管理员确认）: %s",
			strings.ToUpper(h.FromLevel), strings.ToUpper(h.ToLevel), h.Reason)
	case "manual_downgrade":
		return "escalate", fmt.Sprintf("管理员确认降级 %s → %s: %s",
			strings.ToUpper(h.FromLevel), strings.ToUpper(h.ToLevel), h.Reason)
	default:
		return "escalate", fmt.Sprintf("级别升级 %s → %s: %s",
			strings.ToUpper(h.FromLevel), strings.ToUpper(h.ToLevel), h.Reason)
	}
}

func fmtRFC(t time.Time) string {
	return t.Format(time.RFC3339)
}

func deref6(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func pct(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", *v*100)
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func (h *Handler) Claim(c *gin.Context) {
	rec, ok := h.load(c)
	if !ok {
		return
	}
	if rec.Status == "closed" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "告警已关闭, 不可认领"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	if err := New(h.db, h.params, nil).Claim(rec, un); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.claim", rec.AlertNo, "")
	resp.OK(c, nil)
}

func (h *Handler) Note(c *gin.Context) {
	var req struct {
		Note string `json:"note" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	rec, ok := h.load(c)
	if !ok {
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	if err := New(h.db, h.params, nil).Note(rec, un, req.Note); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.note", rec.AlertNo, "")
	resp.OK(c, nil)
}

func (h *Handler) Close(c *gin.Context) {
	var req struct {
		Result string `json:"result" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "关闭必须填写处理结果"))
		return
	}
	rec, ok := h.load(c)
	if !ok {
		return
	}
	if rec.Status == "closed" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "告警已关闭, 不可重复操作"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	if err := New(h.db, h.params, nil).Close(rec, un, req.Result); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.close", rec.AlertNo, "")
	resp.OK(c, nil)
}

// Stats 统计（FR-MON-24: 趋势/高频TOP/MTTA/MTTR/SLA达成率; 契约 §11）
func (h *Handler) Stats(c *gin.Context) {
	from := time.Now().AddDate(0, 0, -30)
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			from = t
		}
	}
	to := time.Now()
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			to = t.AddDate(0, 0, 1)
		}
	}
	base := func() *gorm.DB {
		q := h.db.Model(&persistence.AlertRecord{}).
			Where("trigger_at >= ? AND trigger_at < ?", from, to)
		return applyScope(q, h.scopeSystems(c))
	}

	// 按级别
	type levelCount struct {
		Level string `gorm:"column:level" json:"level"`
		N     int64  `gorm:"column:n" json:"n"`
	}
	var byLevel []levelCount
	_ = base().Select("level, count(*) as n").Group("level").Scan(&byLevel).Error

	// 未关闭（活动/处理中）按级别 —— 统计条用
	var activeByLevel []levelCount
	_ = h.db.Model(&persistence.AlertRecord{}).
		Where("status IN ('active','processing')").
		Select("level, count(*) as n").Group("level").Scan(&activeByLevel).Error

	// 高频 TOP 指标
	type topMetric struct {
		MetricCode string `gorm:"column:metric_code" json:"metricCode"`
		N          int64  `gorm:"column:n" json:"n"`
	}
	var top []topMetric
	_ = base().
		Select("metric_code, count(*) as n").Group("metric_code").Order("n DESC").Limit(10).Scan(&top).Error

	// 告警趋势（按日 × 级别）
	type trendRow struct {
		D     string `gorm:"column:d"`
		Level string `gorm:"column:level"`
		N     int64  `gorm:"column:n"`
	}
	var trends []trendRow
	_ = base().
		Select("to_char(trigger_at, 'YYYY-MM-DD') as d, level, count(*) as n").
		Group("1, 2").Order("1").Scan(&trends).Error
	type trendPoint struct {
		Date string `json:"date"`
		P1   int64  `json:"p1"`
		P2   int64  `json:"p2"`
		P3   int64  `json:"p3"`
		P4   int64  `json:"p4"`
	}
	byDate := map[string]*trendPoint{}
	var dates []string
	for _, t := range trends {
		p := byDate[t.D]
		if p == nil {
			p = &trendPoint{Date: t.D}
			byDate[t.D] = p
			dates = append(dates, t.D)
		}
		switch t.Level {
		case "p1":
			p.P1 += t.N
		case "p2":
			p.P2 += t.N
		case "p3":
			p.P3 += t.N
		default:
			p.P4 += t.N
		}
	}
	sort.Strings(dates)
	trend := make([]trendPoint, 0, len(dates))
	for _, d := range dates {
		trend = append(trend, *byDate[d])
	}

	// MTTA/MTTR + SLA 达成率（Go 内计算, 未认领不计入 MTTA）
	var rows []slaRow
	_ = base().
		Select("level, trigger_at, claimed_at, COALESCE(recovered_at, closed_at) AS end_at").
		Scan(&rows).Error
	sla := ComputeSLA(rows, SlaTargets(h.params))
	var mttSum, mttrSum, mttN, mttrN float64
	for _, r := range rows {
		if r.ClaimedAt != nil {
			mttSum += r.ClaimedAt.Sub(r.TriggerAt).Seconds()
			mttN++
			if r.EndAt != nil {
				mttrSum += r.EndAt.Sub(*r.ClaimedAt).Seconds()
				mttrN++
			}
		}
	}
	var mtt, mttr *float64
	if mttN > 0 {
		v := round1(mttSum / mttN / 60)
		mtt = &v
	}
	if mttrN > 0 {
		v := round1(mttrSum / mttrN / 60)
		mttr = &v
	}

	resp.OK(c, gin.H{
		"range":         gin.H{"from": from.Format("2006-01-02"), "to": to.AddDate(0, 0, -1).Format("2006-01-02")},
		"byLevel":       byLevel,
		"activeByLevel": activeByLevel,
		"topMetrics":    top,
		"trend":         trend,
		"sla":           sla,
		"mttaMinutes":   mtt,
		"mttrMinutes":   mttr,
	})
}

// Export 告警导出（受数据权限约束）
func (h *Handler) Export(c *gin.Context) {
	q := h.db.Model(&persistence.AlertRecord{}).Where("trigger_at >= ?", time.Now().AddDate(0, 0, -30))
	q = applyScope(q, h.scopeSystems(c))
	var items []persistence.AlertRecord
	_ = q.Order("trigger_at DESC").Limit(5000).Find(&items).Error
	f := excelize.NewFile()
	sheet := "告警"
	f.SetSheetName(f.GetSheetName(0), sheet)
	headers := []string{"告警号", "指标", "系统", "级别", "类型", "状态", "触发时间", "最近值", "阈值", "处理结果"}
	for i, ht := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, ht)
	}
	for r, a := range items {
		row := r + 2
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), a.AlertNo)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), a.MetricCode)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), a.SystemCode)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), a.Level)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", row), a.AlertType)
		f.SetCellValue(sheet, fmt.Sprintf("F%d", row), a.Status)
		f.SetCellValue(sheet, fmt.Sprintf("G%d", row), a.TriggerAt.Format("2006-01-02 15:04:05"))
		if a.LastValue != nil {
			f.SetCellValue(sheet, fmt.Sprintf("H%d", row), *a.LastValue)
		}
		if a.Threshold != nil {
			f.SetCellValue(sheet, fmt.Sprintf("I%d", row), *a.Threshold)
		}
		f.SetCellValue(sheet, fmt.Sprintf("J%d", row), a.CloseResult)
	}
	middleware.Log(c, "alert", "alert.export", "", "rows="+fmt.Sprint(len(items)))
	c.Header("Content-Disposition", `attachment; filename="alerts.xlsx"`)
	c.Writer.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_ = f.Write(c.Writer)
}

func atoi3(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("bad")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

var _ = http.StatusOK
