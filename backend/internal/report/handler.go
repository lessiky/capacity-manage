package report

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/pkg/store"
	"capacity-manage/backend/internal/system"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	files  store.Store
	params *system.Params
	az     *middleware.AuthZ
	log    *slog.Logger
	svc    *Service
}

func NewHandler(db *gorm.DB, files store.Store, params *system.Params, az *middleware.AuthZ, log *slog.Logger) *Handler {
	return &Handler{db: db, files: files, params: params, az: az, log: log, svc: NewService(db, files)}
}

// scopeSystems 报表数据权限（FR-RPT-14）: 平台管理员返回 nil(全部), 否则返回授权系统编码
func (h *Handler) scopeSystems(c *gin.Context) []string {
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	h.az.EnsureFresh()
	if h.az.IsPlatformAdmin(un) {
		return nil
	}
	var codes []string
	_ = h.db.Table("sys_data_scope s").
		Joins("JOIN sys_user u ON u.id = s.user_id").
		Joins("LEFT JOIN sys_user_role ur ON ur.user_id = u.id").
		Joins("LEFT JOIN sys_role r ON r.id = ur.role_id").
		Where("u.username = ?", un).
		Where("s.user_id IS NOT NULL OR r.status = 'active'").
		Distinct().Pluck("s.system_code", &codes).Error
	return codes
}

func parseDate(c *gin.Context, def time.Time) (time.Time, bool) {
	dateStr := c.DefaultQuery("date", def.Format("2006-01-02"))
	bd, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日期格式错误"))
		return time.Time{}, false
	}
	return bd, true
}

// Daily 日报数据（FR-RPT-01: 当日值/水位/状态 + 超线清单 + 缺数清单 + 采集异常清单）
// metricsLimit/missingLimit（0=不限制, 兼容旧客户端）: 大指标量场景裁剪响应; metricsTotal 恒为全量数
func (h *Handler) Daily(c *gin.Context) {
	bd, ok := parseDate(c, time.Now().AddDate(0, 0, -1))
	if !ok {
		return
	}
	data, err := CollectDaily(h.db, h.scopeSystems(c), bd)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	metrics := data.Rows
	if ml := optLimit(c, "metricsLimit"); ml > 0 && len(metrics) > ml {
		sorted := make([]DailyRow, len(metrics))
		copy(sorted, metrics)
		sort.SliceStable(sorted, func(i, j int) bool {
			wi, wj := 0.0, 0.0
			if sorted[i].WaterLevel != nil {
				wi = *sorted[i].WaterLevel
			}
			if sorted[j].WaterLevel != nil {
				wj = *sorted[j].WaterLevel
			}
			return wi > wj
		})
		metrics = sorted[:ml]
	}
	missing := data.MissingRows()
	if ml := optLimit(c, "missingLimit"); ml > 0 && len(missing) > ml {
		missing = missing[:ml]
	}
	resp.OK(c, gin.H{
		"bizDate":             data.BizDate,
		"metrics":             metrics,
		"metricsTotal":        len(data.Rows),
		"overCount":           len(data.OverRows()),
		"overList":            data.OverRows(),
		"missingCount":        len(data.MissingRows()),
		"missingList":         missing,
		"statusCounts":        data.Status(),
		"collectFailCount":    len(data.Fails),
		"collectFailures":     data.Fails,
		"forecastCount":       len(data.ForecastRows()),
		"levelChanges":        data.LevelChanges,
		"legacyLevelMappings": data.LegacyLevelMappings,
	})
}

// optLimit 可选上限参数（缺省/0=不限制, 最大 10000）
func optLimit(c *gin.Context, name string) int {
	v := c.Query(name)
	if v == "" {
		return 0
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	if n > 10000 {
		return 10000
	}
	return n
}

// Export 日报导出（FR-RPT-12: Excel/PDF）, 数据权限与 Daily 一致
func (h *Handler) Export(c *gin.Context) {
	bd, err := time.ParseInLocation("2006-01-02", c.Param("date"), time.Local)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日期格式错误"))
		return
	}
	format := c.DefaultQuery("format", "xlsx")
	data, err := CollectDaily(h.db, h.scopeSystems(c), bd)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	var (
		content []byte
		mime    string
		ext     string
	)
	switch format {
	case "pdf":
		content, err = BuildPDF(data)
		mime, ext = "application/pdf", "pdf"
	default:
		content, err = BuildExcel(data)
		mime, ext = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"
	}
	if err != nil {
		h.log.Error("report export failed", "date", bd.Format("2006-01-02"), "format", format, "err", err.Error())
		resp.Fail(c, err)
		return
	}
	filename := fmt.Sprintf("daily-report-%s.%s", bd.Format("2006-01-02"), ext)
	middleware.Log(c, "report", "report.export", filename, "rows="+fmt.Sprint(len(data.Rows)))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Writer.Header().Set("Content-Type", mime)
	c.Writer.Header().Set("Content-Length", fmt.Sprint(len(content)))
	_, _ = c.Writer.Write(content)
}

// Files 已自动生成的日报列表
func (h *Handler) Files(c *gin.Context) {
	var items []persistence.ReportResult
	_ = h.db.Where("report_type = 'daily'").Order("biz_date DESC").Limit(90).Find(&items).Error
	resp.OK(c, items)
}

// Download 下载已生成的日报文件（format=xlsx|pdf）
func (h *Handler) Download(c *gin.Context) {
	bd, err := time.ParseInLocation("2006-01-02", c.Param("date"), time.Local)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日期格式错误"))
		return
	}
	format := c.DefaultQuery("format", "xlsx")
	xlsxKey, pdfKey := ReportKeys(bd)
	key := xlsxKey
	if format == "pdf" {
		key = pdfKey
	}
	if !h.files.Exists(key) {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "该日期日报未生成, 请先加载/生成"))
		return
	}
	rc, err := h.files.Open(key)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	defer rc.Close()
	ext := "xlsx"
	if format == "pdf" {
		ext = "pdf"
	}
	mime := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	if ext == "pdf" {
		mime = "application/pdf"
	}
	filename := fmt.Sprintf("daily-report-%s.%s", bd.Format("2006-01-02"), ext)
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Writer.Header().Set("Content-Type", mime)
	_, _ = io.Copy(c.Writer, rc)
}

// Generate 手工触发（重新）生成指定业务日的日报
func (h *Handler) Generate(c *gin.Context) {
	bd, err := time.ParseInLocation("2006-01-02", c.Param("date"), time.Local)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日期格式错误"))
		return
	}
	res, err := GenerateDaily(c.Request.Context(), h.db, h.files, bd)
	if err != nil {
		h.log.Error("report generate failed", "date", bd.Format("2006-01-02"), "err", err.Error())
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "report", "report.generate", bd.Format("2006-01-02"), "status="+res.Status)
	resp.OK(c, res)
}
