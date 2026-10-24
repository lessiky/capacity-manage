package openapi

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------- FR-MET-04 衍生指标 ----------

type derivedMetricInput struct {
	MetricCode    string   `json:"metricCode"`
	Name          string   `json:"name"`
	SystemCode    string   `json:"systemCode"`
	Expression    string   `json:"expression"`
	SourceMetrics []string `json:"sourceMetrics"`
	Unit          string   `json:"unit"`
}

// ListDerivedMetrics 衍生指标列表。
func (h *Handler) ListDerivedMetrics(c *gin.Context) {
	if page, size, ok := resp.ParsePage(c, 20, 200); ok {
		q := h.db.Model(&persistence.DerivedMetric{})
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.DerivedMetric
		if err := q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, gin.H{"items": rows, "count": len(rows), "total": total, "page": page, "pageSize": size})
		return
	}
	var rows []persistence.DerivedMetric
	if err := h.db.Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows)})
}

// CreateDerivedMetric 新建衍生指标定义，并做表达式与源指标存在性校验。
func (h *Handler) CreateDerivedMetric(c *gin.Context) {
	var in derivedMetricInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if err := h.validateDerived(c, in); err != nil {
		resp.Fail(c, err)
		return
	}
	sources, _ := json.Marshal(in.SourceMetrics)
	row := persistence.DerivedMetric{
		MetricCode: in.MetricCode, Name: in.Name, SystemCode: in.SystemCode,
		Expression: in.Expression, SourceMetrics: string(sources), Unit: in.Unit,
		Status: "active", CreatedBy: currentUser(c),
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "衍生指标创建失败: %v", err))
		return
	}
	middleware.Log(c, "metric", "derived_create", row.MetricCode, row.Expression)
	resp.OK(c, row)
}

// DisableDerivedMetric 停用衍生指标。
func (h *Handler) DisableDerivedMetric(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.DerivedMetric
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "衍生指标不存在"))
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"status": "disabled", "updated_at": time.Now(),
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "derived_disable", row.MetricCode, "")
	resp.OK(c, gin.H{"id": id, "status": "disabled"})
}

// EnableDerivedMetric 重新启用衍生指标。
func (h *Handler) EnableDerivedMetric(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.DerivedMetric
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "衍生指标不存在"))
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"status": "active", "updated_at": time.Now(),
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "derived_enable", row.MetricCode, "")
	resp.OK(c, gin.H{"id": id, "status": "active"})
}

// DeleteDerivedMetric 永久删除已停用的衍生指标。
func (h *Handler) DeleteDerivedMetric(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.DerivedMetric
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "衍生指标不存在"))
		return
	}
	if row.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "请先停用衍生指标后再删除"))
		return
	}
	if err := h.db.Delete(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "derived_delete", row.MetricCode, "")
	resp.OK(c, gin.H{"id": id, "deleted": true})
}

// EvaluateDerived 试算衍生指标：以源指标最新日值代入表达式。
func (h *Handler) EvaluateDerived(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.DerivedMetric
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "衍生指标不存在"))
		return
	}
	if row.Status != "active" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "衍生指标已停用，不能试算"))
		return
	}
	values, err := h.latestValues(c, jsonList(row.SourceMetrics))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	value, err := evaluateExpression(row.Expression, values)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "表达式无法试算: %v", err))
		return
	}
	resp.OK(c, gin.H{"metricCode": row.MetricCode, "expression": row.Expression, "values": values, "result": value})
}

func (h *Handler) validateDerived(c *gin.Context, in derivedMetricInput) *resp.AppError {
	if strings.TrimSpace(in.MetricCode) == "" || strings.TrimSpace(in.Name) == "" {
		return resp.NewErr(resp.CodeBadRequest, "指标编码与名称不能为空")
	}
	if strings.TrimSpace(in.Expression) == "" {
		return resp.NewErr(resp.CodeBadRequest, "表达式不能为空")
	}
	if len(in.SourceMetrics) == 0 {
		return resp.NewErr(resp.CodeBadRequest, "至少需要一个源指标")
	}
	for _, code := range in.SourceMetrics {
		var n int64
		if err := h.db.Model(&persistence.CapacityMetric{}).Where("metric_code = ?", code).Count(&n).Error; err != nil {
			return resp.NewErr(resp.CodeInternal, "源指标校验失败")
		}
		if n == 0 {
			return resp.NewErr(resp.CodeBadRequest, "源指标不存在: %s", code)
		}
	}
	var existing int64
	if err := h.db.Model(&persistence.DerivedMetric{}).Where("metric_code = ?", in.MetricCode).Count(&existing).Error; err != nil {
		return resp.NewErr(resp.CodeInternal, "编码查重失败")
	}
	if existing > 0 {
		return resp.NewErr(resp.CodeConflict, "衍生指标编码已存在: %s", in.MetricCode)
	}
	return nil
}

func (h *Handler) latestValues(c *gin.Context, codes []string) (map[string]float64, error) {
	out := make(map[string]float64, len(codes))
	for _, code := range codes {
		var row persistence.MetricValueDay
		if err := h.db.Where("metric_code = ?", code).Order("biz_date DESC").First(&row).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			return nil, err
		}
		if row.StatValue != nil {
			out[code] = *row.StatValue
		}
	}
	return out, nil
}

// ---------- FR-MET-13 阈值模板 ----------

type thresholdTemplateInput struct {
	Name           string   `json:"name"`
	JudgeDirection string   `json:"judgeDirection"`
	WarnLine       *float64 `json:"warnLine"`
	DangerLine     *float64 `json:"dangerLine"`
	WarnRatio      *float64 `json:"warnRatio"`
	DangerRatio    *float64 `json:"dangerRatio"`
	Unit           string   `json:"unit"`
	Remark         string   `json:"remark"`
}

// ListThresholdTemplates 阈值模板列表。
func (h *Handler) ListThresholdTemplates(c *gin.Context) {
	var rows []persistence.ThresholdTemplate
	if err := h.db.Order("created_at DESC").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows)})
}

// CreateThresholdTemplate 新建阈值模板。
func (h *Handler) CreateThresholdTemplate(c *gin.Context) {
	var in thresholdTemplateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "模板名称不能为空"))
		return
	}
	if in.JudgeDirection != "higher_worse" && in.JudgeDirection != "lower_worse" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "判定方向不合法"))
		return
	}
	row := persistence.ThresholdTemplate{
		Name: in.Name, JudgeDirection: in.JudgeDirection,
		WarnLine: in.WarnLine, DangerLine: in.DangerLine,
		WarnRatio: in.WarnRatio, DangerRatio: in.DangerRatio,
		Unit: in.Unit, Remark: in.Remark, Status: "active", CreatedBy: currentUser(c),
	}
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "threshold_template_create", row.Name, row.JudgeDirection)
	resp.OK(c, row)
}

// DisableThresholdTemplate 停用阈值模板。
func (h *Handler) DisableThresholdTemplate(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.ThresholdTemplate
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "阈值模板不存在"))
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"status": "disabled", "updated_at": time.Now(),
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "threshold_template_disable", row.Name, "")
	resp.OK(c, gin.H{"id": id, "status": "disabled"})
}

// EnableThresholdTemplate 重新启用阈值模板。
func (h *Handler) EnableThresholdTemplate(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.ThresholdTemplate
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "阈值模板不存在"))
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{
		"status": "active", "updated_at": time.Now(),
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "threshold_template_enable", row.Name, "")
	resp.OK(c, gin.H{"id": id, "status": "active"})
}

// DeleteThresholdTemplate 永久删除已停用的阈值模板。
func (h *Handler) DeleteThresholdTemplate(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var row persistence.ThresholdTemplate
	if err := h.db.First(&row, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "阈值模板不存在"))
		return
	}
	if row.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "请先停用阈值模板后再删除"))
		return
	}
	if err := h.db.Delete(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "threshold_template_delete", row.Name, "")
	resp.OK(c, gin.H{"id": id, "deleted": true})
}

// ApplyThresholdTemplate 把模板套用到指定指标（同时写阈值历史，便于追溯）。
func (h *Handler) ApplyThresholdTemplate(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var tpl persistence.ThresholdTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "阈值模板不存在"))
		return
	}
	if tpl.Status != "active" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "阈值模板已停用，不能套用"))
		return
	}
	var in struct {
		MetricCodes []string `json:"metricCodes"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	if len(in.MetricCodes) == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "至少选择一个指标"))
		return
	}
	applied := 0
	for _, code := range in.MetricCodes {
		var metric persistence.CapacityMetric
		if err := h.db.Where("metric_code = ?", code).First(&metric).Error; err != nil {
			continue
		}
		updates := map[string]any{"judge_direction": tpl.JudgeDirection, "updated_at": time.Now()}
		if tpl.WarnLine != nil {
			updates["warn_line"] = *tpl.WarnLine
		}
		if tpl.DangerLine != nil {
			updates["danger_line"] = *tpl.DangerLine
		}
		if tpl.WarnRatio != nil {
			updates["warn_ratio"] = *tpl.WarnRatio
		}
		if tpl.DangerRatio != nil {
			updates["danger_ratio"] = *tpl.DangerRatio
		}
		if err := h.db.Model(&metric).Updates(updates).Error; err != nil {
			continue
		}
		applied++
	}
	middleware.Log(c, "metric", "threshold_template_apply", tpl.Name,
		fmt.Sprintf("applied=%d", applied))
	resp.OK(c, gin.H{"templateId": id, "applied": applied})
}

// ---------- FR-COL-16 批量导入 ----------

// metricImportRow 是批量导入的一行指标定义。
type metricImportRow struct {
	MetricCode     string   `json:"metricCode"`
	Name           string   `json:"name"`
	SystemCode     string   `json:"systemCode"`
	Unit           string   `json:"unit"`
	JudgeDirection string   `json:"judgeDirection"`
	WarnLine       *float64 `json:"warnLine"`
	DangerLine     *float64 `json:"dangerLine"`
}

// ImportMetrics 批量导入指标（CSV 上传或 JSON 数组）。CSV 表头:
// metricCode,name,systemCode,unit,judgeDirection,warnLine,dangerLine
func (h *Handler) ImportMetrics(c *gin.Context) {
	var rows []metricImportRow
	filename := "inline"
	file, header, err := c.Request.FormFile("file")
	if err == nil {
		defer file.Close()
		filename = header.Filename
		parsed, err := parseMetricCSV(file)
		if err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "CSV 解析失败: %v", err))
			return
		}
		rows = parsed
	} else {
		if err := c.ShouldBindJSON(&rows); err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "需要 CSV 文件或 JSON 数组"))
			return
		}
	}
	if len(rows) == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "导入内容为空"))
		return
	}
	run := persistence.MetricImportRun{
		Filename: filename, TotalRows: len(rows), Status: "running", CreatedBy: currentUser(c),
	}
	if err := h.db.Create(&run).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	type failure struct {
		Row    int    `json:"row"`
		Reason string `json:"reason"`
	}
	failures := make([]failure, 0)
	for i, r := range rows {
		reason := h.upsertImportedMetric(r.MetricCode, r.Name, r.SystemCode, r.Unit, r.JudgeDirection, r.WarnLine, r.DangerLine)
		if reason != "" {
			failures = append(failures, failure{Row: i + 1, Reason: reason})
			continue
		}
		run.SuccessRows++
	}
	run.FailedRows = len(failures)
	run.Status = "success"
	if run.FailedRows > 0 && run.SuccessRows == 0 {
		run.Status = "failed"
	} else if run.FailedRows > 0 {
		run.Status = "partial"
	}
	raw, _ := json.Marshal(failures)
	now := time.Now()
	if err := h.db.Model(&run).Updates(map[string]any{
		"success_rows": run.SuccessRows, "failed_rows": run.FailedRows,
		"status": run.Status, "error_report": string(raw), "finished_at": now,
	}).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "metric", "batch_import", filename,
		fmt.Sprintf("total=%d success=%d failed=%d", run.TotalRows, run.SuccessRows, run.FailedRows))
	resp.OK(c, gin.H{
		"runId": run.ID, "total": run.TotalRows, "success": run.SuccessRows,
		"failed": run.FailedRows, "status": run.Status, "errors": failures,
	})
}

func (h *Handler) upsertImportedMetric(code, name, system, unit, direction string, warn, danger *float64) string {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(system) == "" {
		return "指标编码/名称/系统编码不能为空"
	}
	if direction == "" {
		direction = "higher_worse"
	}
	if direction != "higher_worse" && direction != "lower_worse" {
		return "判定方向不合法"
	}
	var n int64
	if err := h.db.Model(&persistence.BusinessSystem{}).Where("system_code = ?", system).Count(&n).Error; err != nil {
		return "系统校验失败"
	}
	if n == 0 {
		return "业务系统不存在: " + system
	}
	var existing persistence.CapacityMetric
	err := h.db.Where("metric_code = ?", code).First(&existing).Error
	if err != nil {
		row := persistence.CapacityMetric{
			MetricCode: code, Name: name, SystemCode: system, Unit: unit,
			JudgeDirection: direction, WarnLine: warn, DangerLine: danger,
			Status: "active", MetricKind: "single",
		}
		if err := h.db.Create(&row).Error; err != nil {
			return "创建失败: " + err.Error()
		}
		return ""
	}
	updates := map[string]any{
		"name": name, "unit": unit, "judge_direction": direction, "updated_at": time.Now(),
	}
	if warn != nil {
		updates["warn_line"] = *warn
	}
	if danger != nil {
		updates["danger_line"] = *danger
	}
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		return "更新失败: " + err.Error()
	}
	return ""
}

func parseMetricCSV(r io.Reader) ([]metricImportRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("至少需要表头与一行数据")
	}
	index := map[string]int{}
	for i, h := range records[0] {
		index[strings.TrimSpace(strings.ToLower(h))] = i
	}
	required := []string{"metriccode", "name", "systemcode"}
	for _, need := range required {
		if _, ok := index[need]; !ok {
			return nil, fmt.Errorf("缺少表头 %s", need)
		}
	}
	out := make([]metricImportRow, 0, len(records)-1)
	get := func(rec []string, key string) string {
		idx, ok := index[key]
		if !ok || idx >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[idx])
	}
	for _, rec := range records[1:] {
		item := metricImportRow{
			MetricCode: get(rec, "metriccode"), Name: get(rec, "name"),
			SystemCode: get(rec, "systemcode"), Unit: get(rec, "unit"),
			JudgeDirection: get(rec, "judgedirection"),
		}
		if v := get(rec, "warnline"); v != "" {
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				item.WarnLine = &parsed
			}
		}
		if v := get(rec, "dangerline"); v != "" {
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				item.DangerLine = &parsed
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// ListImportRuns 导入历史。
// 兼容: 不带 page 参数返回最近 200 条; 带 page 时返回 {items,total,page,pageSize}。
func (h *Handler) ListImportRuns(c *gin.Context) {
	if page, size, ok := resp.ParsePage(c, 20, 200); ok {
		q := h.db.Model(&persistence.MetricImportRun{})
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.MetricImportRun
		if err := q.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, gin.H{"items": rows, "count": len(rows), "total": total, "page": page, "pageSize": size})
		return
	}
	var rows []persistence.MetricImportRun
	if err := h.db.Order("created_at DESC, id DESC").Limit(200).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows)})
}

// ---------- FR-TD-05 日历完整性检查 ----------

// RunCalendarCheck 检查指定年份交易日历是否覆盖全年日期且无重复。
func (h *Handler) RunCalendarCheck(c *gin.Context) {
	year := time.Now().Year()
	if raw := strings.TrimSpace(c.Query("year")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 2000 && n <= 2100 {
			year = n
		}
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	to := time.Date(year, 12, 31, 0, 0, 0, 0, time.Local)
	market := strings.TrimSpace(c.Query("market"))
	type dayRow struct {
		CalDate   time.Time `gorm:"column:cal_date"`
		IsTrading bool      `gorm:"column:is_trading"`
	}
	var rows []dayRow
	q := h.db.Table("trading_day_calendar").
		Select("cal_date, is_trading").
		Where("cal_date >= ? AND cal_date <= ?", from, to)
	if market != "" {
		q = q.Where("market = ?", market)
	}
	if err := q.Order("cal_date").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	seen := make(map[string]bool, len(rows))
	missing := make([]string, 0)
	for _, r := range rows {
		seen[r.CalDate.Format("2006-01-02")] = true
	}
	expected := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		expected++
		if !seen[d.Format("2006-01-02")] {
			missing = append(missing, d.Format("2006-01-02"))
		}
	}
	status := "success"
	message := "日历完整"
	if len(missing) > 0 {
		status = "warning"
		message = fmt.Sprintf("缺失 %d 天", len(missing))
	}
	raw, _ := json.Marshal(missing)
	run := persistence.CalendarCheckRun{
		Year: year, ExpectedDays: expected, ActualDays: len(rows),
		MissingDates: string(raw), Status: status, Message: message,
	}
	if err := h.db.Create(&run).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, run)
}
