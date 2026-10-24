package report

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

type definitionRequest struct {
	Name            string   `json:"name"`
	ReportType      string   `json:"reportType"`
	SystemScope     []string `json:"systemScope"`
	MetricScope     []string `json:"metricScope"`
	GroupDimension  string   `json:"groupDimension"`
	SortBy          string   `json:"sortBy"`
	SortDesc        bool     `json:"sortDesc"`
	IncludeForecast bool     `json:"includeForecast"`
	PeriodWindow    int      `json:"periodWindow"`
	ChartOptions    any      `json:"chartOptions"`
	Enabled         bool     `json:"enabled"`
}

type subscriptionRequest struct {
	Name         string   `json:"name"`
	DefinitionID int64    `json:"definitionId"`
	Recipients   []string `json:"recipients"`
	ChannelIDs   []int64  `json:"channelIds"`
	ScheduleType string   `json:"scheduleType"`
	ScheduleTime string   `json:"scheduleTime"`
	Weekday      int      `json:"weekday"`
	DayOfMonth   int      `json:"dayOfMonth"`
	Timezone     string   `json:"timezone"`
	Enabled      bool     `json:"enabled"`
}

func (h *Handler) ListDefinitions(c *gin.Context) {
	var rows []persistence.ReportDefinition
	if err := h.db.Order("report_type, id").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows})
}

func (h *Handler) CreateDefinition(c *gin.Context) {
	var req definitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "请求体格式错误"))
		return
	}
	row, err := definitionFromRequest(req, nil)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	row.CreatedBy = currentUsername(c)
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.definition.create", strconv.FormatInt(row.ID, 10), row.Name)
	resp.OK(c, row)
}

func (h *Handler) UpdateDefinition(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var current persistence.ReportDefinition
	if err := h.db.First(&current, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表模板不存在"))
		return
	}
	var req definitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "请求体格式错误"))
		return
	}
	row, err := definitionFromRequest(req, &current)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	row.ID = current.ID
	row.CreatedBy = current.CreatedBy
	row.CreatedAt = current.CreatedAt
	if err := h.db.Save(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.definition.update", strconv.FormatInt(row.ID, 10), row.Name)
	resp.OK(c, row)
}

func (h *Handler) DeleteDefinition(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&persistence.ReportDefinition{}, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.definition.delete", strconv.FormatInt(id, 10), "")
	resp.OK(c, gin.H{"deleted": id})
}

func (h *Handler) PreviewCenter(c *gin.Context) {
	def, period, ok := h.centerRequest(c)
	if !ok {
		return
	}
	systems := h.effectiveSystems(c, def)
	data, err := h.svc.Generate(c.Request.Context(), def, period, systems)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, data)
}

func (h *Handler) RunCenter(c *gin.Context) {
	def, period, ok := h.centerRequest(c)
	if !ok {
		return
	}
	systems := h.effectiveSystems(c, def)
	run, err := h.svc.RunCenter(c.Request.Context(), def, period, currentUsername(c), systems)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.center.run", strconv.FormatInt(run.ID, 10), def.Name)
	resp.OK(c, run)
}

func (h *Handler) ListRuns(c *gin.Context) {
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	q := h.db.Model(&persistence.ReportRun{})
	if definitionID, err := strconv.ParseInt(c.Query("definitionId"), 10, 64); err == nil && definitionID > 0 {
		q = q.Where("definition_id = ?", definitionID)
	}
	var rows []persistence.ReportRun
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows})
}

func (h *Handler) GetRun(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var run persistence.ReportRun
	if err := h.db.First(&run, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表运行不存在"))
		return
	}
	var def persistence.ReportDefinition
	_ = h.db.First(&def, run.DefinitionID).Error
	files := map[string]string{}
	_ = json.Unmarshal([]byte(run.Files), &files)
	resp.OK(c, gin.H{"run": run, "definition": def, "files": files})
}

func (h *Handler) DownloadCenter(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	format := strings.ToLower(c.DefaultQuery("format", "xlsx"))
	if format != "xlsx" && format != "pdf" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "format 仅支持 xlsx 或 pdf"))
		return
	}
	var run persistence.ReportRun
	if err := h.db.First(&run, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表运行不存在"))
		return
	}
	files := map[string]string{}
	if err := json.Unmarshal([]byte(run.Files), &files); err != nil {
		resp.Fail(c, err)
		return
	}
	key := files[format]
	if key == "" || h.files == nil || !h.files.Exists(key) {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表文件不存在"))
		return
	}
	rc, err := h.files.Open(key)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	defer rc.Close()
	mime := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	if format == "pdf" {
		mime = "application/pdf"
	}
	filename := "capacity-report-" + strconv.FormatInt(run.ID, 10) + "." + format
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", mime)
	_, _ = io.Copy(c.Writer, rc)
}

func (h *Handler) ListSubscriptions(c *gin.Context) {
	var rows []persistence.ReportSubscription
	if err := h.db.Order("id").Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows})
}

func (h *Handler) CreateSubscription(c *gin.Context) {
	var req subscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "请求体格式错误"))
		return
	}
	row, err := subscriptionFromRequest(req, nil)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	row.CreatedBy = currentUsername(c)
	next := ComputeNextRun(row, time.Now())
	row.NextRunAt = next
	if err := h.db.Create(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.subscription.create", strconv.FormatInt(row.ID, 10), row.Name)
	resp.OK(c, row)
}

func (h *Handler) UpdateSubscription(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var current persistence.ReportSubscription
	if err := h.db.First(&current, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表订阅不存在"))
		return
	}
	var req subscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "请求体格式错误"))
		return
	}
	row, err := subscriptionFromRequest(req, &current)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	row.ID = current.ID
	row.CreatedBy = current.CreatedBy
	row.CreatedAt = current.CreatedAt
	row.LastRunAt = current.LastRunAt
	row.NextRunAt = ComputeNextRun(row, time.Now())
	if err := h.db.Save(&row).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.subscription.update", strconv.FormatInt(row.ID, 10), row.Name)
	resp.OK(c, row)
}

func (h *Handler) DeleteSubscription(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&persistence.ReportSubscription{}, id).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.subscription.delete", strconv.FormatInt(id, 10), "")
	resp.OK(c, gin.H{"deleted": id})
}

func (h *Handler) RunSubscription(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var sub persistence.ReportSubscription
	if err := h.db.First(&sub, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表订阅不存在"))
		return
	}
	var def persistence.ReportDefinition
	if err := h.db.First(&def, sub.DefinitionID).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	run, err := h.svc.RunCenter(c.Request.Context(), def, subscriptionPeriod(def, time.Now()), currentUsername(c), h.effectiveSystems(c, def))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, run)
}

func (h *Handler) centerRequest(c *gin.Context) (persistence.ReportDefinition, ReportPeriod, bool) {
	id, ok := parseIDParam(c)
	if !ok {
		return persistence.ReportDefinition{}, ReportPeriod{}, false
	}
	var def persistence.ReportDefinition
	if err := h.db.First(&def, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "报表模板不存在"))
		return def, ReportPeriod{}, false
	}
	end := clock.DayStart(time.Now()).AddDate(0, 0, -1)
	if raw := c.Query("date"); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "date 格式应为 YYYY-MM-DD"))
			return def, ReportPeriod{}, false
		}
		end = parsed
	}
	period := ReportPeriod{RunDate: end, End: end}
	if raw := c.Query("start"); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "start 格式应为 YYYY-MM-DD"))
			return def, ReportPeriod{}, false
		}
		period.Start = parsed
	}
	return def, normalizeReportPeriod(def, period), true
}

func (h *Handler) effectiveSystems(c *gin.Context, def persistence.ReportDefinition) []string {
	userScope := h.scopeSystems(c)
	var defScope []string
	_ = json.Unmarshal([]byte(def.SystemScope), &defScope)
	if userScope == nil {
		if len(defScope) == 0 {
			return nil
		}
		return defScope
	}
	if len(defScope) == 0 {
		return userScope
	}
	allowed := make(map[string]bool, len(userScope))
	for _, code := range userScope {
		allowed[code] = true
	}
	out := make([]string, 0, len(defScope))
	for _, code := range defScope {
		if allowed[code] {
			out = append(out, code)
		}
	}
	return out
}

func definitionFromRequest(req definitionRequest, current *persistence.ReportDefinition) (persistence.ReportDefinition, error) {
	if strings.TrimSpace(req.Name) == "" {
		return persistence.ReportDefinition{}, resp.NewErr(resp.CodeBadRequest, "报表名称不能为空")
	}
	switch strings.ToLower(req.ReportType) {
	case "daily", "weekly", "monthly", "capacity_analysis", "alert_summary", "forecast":
	default:
		return persistence.ReportDefinition{}, resp.NewErr(resp.CodeBadRequest, "报表类型非法")
	}
	if req.PeriodWindow <= 0 {
		req.PeriodWindow = 30
	}
	if current == nil {
		req.Enabled = req.Enabled || !req.Enabled
	}
	systemScope, _ := json.Marshal(req.SystemScope)
	metricScope, _ := json.Marshal(req.MetricScope)
	chartOptions, _ := json.Marshal(req.ChartOptions)
	if len(chartOptions) == 0 || string(chartOptions) == "null" {
		chartOptions = []byte("{}")
	}
	return persistence.ReportDefinition{
		Name: strings.TrimSpace(req.Name), ReportType: strings.ToLower(req.ReportType),
		SystemScope: string(systemScope), MetricScope: string(metricScope),
		GroupDimension: req.GroupDimension, SortBy: req.SortBy, SortDesc: req.SortDesc,
		IncludeForecast: req.IncludeForecast, PeriodWindow: req.PeriodWindow,
		ChartOptions: string(chartOptions), Enabled: req.Enabled,
	}, nil
}

func subscriptionFromRequest(req subscriptionRequest, current *persistence.ReportSubscription) (persistence.ReportSubscription, error) {
	if strings.TrimSpace(req.Name) == "" || req.DefinitionID <= 0 {
		return persistence.ReportSubscription{}, resp.NewErr(resp.CodeBadRequest, "订阅名称和报表模板不能为空")
	}
	req.ScheduleType = strings.ToLower(req.ScheduleType)
	switch req.ScheduleType {
	case "daily", "weekly", "monthly":
	default:
		return persistence.ReportSubscription{}, resp.NewErr(resp.CodeBadRequest, "调度类型非法")
	}
	if _, _, ok := parseHHMMStrict(req.ScheduleTime); !ok {
		return persistence.ReportSubscription{}, resp.NewErr(resp.CodeBadRequest, "调度时间格式应为 HH:MM")
	}
	if req.Timezone == "" {
		req.Timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return persistence.ReportSubscription{}, resp.NewErr(resp.CodeBadRequest, "时区非法")
	}
	if req.ScheduleType == "weekly" && (req.Weekday < 1 || req.Weekday > 7) {
		return persistence.ReportSubscription{}, resp.NewErr(resp.CodeBadRequest, "每周调度需指定 1~7")
	}
	if req.ScheduleType == "monthly" && (req.DayOfMonth < 1 || req.DayOfMonth > 28) {
		return persistence.ReportSubscription{}, resp.NewErr(resp.CodeBadRequest, "每月调度日需为 1~28")
	}
	recipients, _ := json.Marshal(req.Recipients)
	channels, _ := json.Marshal(req.ChannelIDs)
	return persistence.ReportSubscription{
		Name: strings.TrimSpace(req.Name), DefinitionID: req.DefinitionID,
		Recipients: string(recipients), ChannelIDs: string(channels),
		ScheduleType: req.ScheduleType, ScheduleTime: req.ScheduleTime,
		Weekday: req.Weekday, DayOfMonth: req.DayOfMonth, Timezone: req.Timezone,
		Enabled: req.Enabled,
	}, nil
}

func parseHHMMStrict(raw string) (int, int, bool) {
	parsed, err := time.Parse("15:04", raw)
	if err != nil {
		return 0, 0, false
	}
	return parsed.Hour(), parsed.Minute(), true
}

func parseIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "ID 非法"))
		return 0, false
	}
	return id, true
}

func currentUsername(c *gin.Context) string {
	value, _ := c.Get(middleware.CtxUsername)
	name, _ := value.(string)
	return name
}
