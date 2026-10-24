package tradingday

import (
	"fmt"
	"log/slog"
	"net/http"
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
	cal *Calendar
	db  *gorm.DB
	log *slog.Logger
}

func NewHandler(cal *Calendar, db *gorm.DB, log *slog.Logger) *Handler {
	return &Handler{cal: cal, db: db, log: log}
}

// List 日历查询 {market, from, to}
// 兼容: 不带 page 参数返回全量数组(旧客户端/导出场景); 带 page 时返回 {items,total,page,pageSize}
func (h *Handler) List(c *gin.Context) {
	market := c.DefaultQuery("market", "SH")
	from, to := dateRange(c, 30)
	q := h.db.Model(&persistence.TradingDayCalendar{}).
		Where("market = ? AND cal_date >= ? AND cal_date <= ?", market, from, to)
	page, perr := atoi2(c.Query("page"))
	if perr == nil && page > 0 {
		size := 30
		if v := c.Query("pageSize"); v != "" {
			if n, serr := atoi2(v); serr == nil && n > 0 && n <= 400 {
				size = n
			}
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.TradingDayCalendar
		if err := q.Order("cal_date").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, resp.Page(calItems(rows), total, page, size))
		return
	}
	var rows []persistence.TradingDayCalendar
	_ = q.Order("cal_date").Find(&rows).Error
	resp.OK(c, calItems(rows))
}

type calItem struct {
	Date      string `json:"date"`
	IsTrading bool   `json:"isTrading"`
	DayType   string `json:"dayType"`
	Source    string `json:"source"`
}

func calItems(rows []persistence.TradingDayCalendar) []calItem {
	out := make([]calItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, calItem{Date: r.CalDate.Format("2006-01-02"), IsTrading: r.IsTrading, DayType: r.DayType, Source: r.Source})
	}
	return out
}

func atoi2(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid int: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// Save 批量保存/修正
func (h *Handler) Save(c *gin.Context) {
	var req struct {
		Market string `json:"market" binding:"required"`
		Dates  []struct {
			Date      string `json:"date" binding:"required"`
			IsTrading bool   `json:"isTrading"`
			DayType   string `json:"dayType"`
		} `json:"dates" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	tx := h.db.Begin()
	minD, maxD := time.Time{}, time.Time{}
	for _, d := range req.Dates {
		t, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			tx.Rollback()
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", "日期格式错误: "+d.Date))
			return
		}
		if minD.IsZero() || t.Before(minD) {
			minD = t
		}
		if t.After(maxD) {
			maxD = t
		}
		dayType := d.DayType
		switch {
		case d.IsTrading && (dayType == "" || dayType == "trading" || dayType == "makeup"):
			dayType = "trading"
		case !d.IsTrading && dayType == "":
			if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
				dayType = "weekend"
			} else {
				dayType = "holiday"
			}
		}
		var n int64
		_ = tx.Model(&persistence.TradingDayCalendar{}).Where("market = ? AND cal_date = ?", req.Market, t).Count(&n).Error
		if n == 0 {
			_ = tx.Create(&persistence.TradingDayCalendar{
				Market: req.Market, CalDate: t, IsTrading: d.IsTrading, DayType: dayType, Source: "manual",
			}).Error
		} else {
			_ = tx.Model(&persistence.TradingDayCalendar{}).
				Where("market = ? AND cal_date = ?", req.Market, t).
				Updates(map[string]any{"is_trading": d.IsTrading, "day_type": dayType, "source": "manual"}).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	h.cal.InvalidateCache(c.Request.Context(), req.Market, minD, maxD)
	middleware.Log(c, "tradingday", "calendar.save", req.Market, fmt.Sprintf("dates=%d", len(req.Dates)))
	resp.OK(c, gin.H{"saved": len(req.Dates)})
}

// Import Excel 导入（模板: 日期,是否交易日,日类型）
// normalizeDayType 归一化导入的日类型（支持中/英文取值）
func normalizeDayType(s string) (string, bool) {
	switch strings.TrimSpace(s) {
	case "trading", "交易日":
		return "trading", true
	case "weekend", "周末":
		return "weekend", true
	case "holiday", "节假日":
		return "holiday", true
	case "makeup", "makeup_workday", "调休交易日", "调休":
		return "makeup", true
	}
	return "", false
}

func (h *Handler) Import(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", "缺少文件: "+err.Error()))
		return
	}
	fio, err := file.Open()
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", "文件打开失败: "+err.Error()))
		return
	}
	defer fio.Close()
	f, err := excelize.OpenReader(fio)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", "Excel 打开失败: "+err.Error()))
		return
	}
	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%s", "Excel 读取失败: "+err.Error()))
		return
	}
	type errItem struct {
		Row int    `json:"row"`
		Msg string `json:"msg"`
	}
	var errs []errItem
	var toSave []persistence.TradingDayCalendar
	for i, row := range rows {
		if i == 0 && len(row) > 0 {
			hdr := row[0]
			if hdr == "日期" || hdr == "date" {
				continue
			}
		}
		if len(row) < 2 {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02", row[0], time.Local)
		if err != nil {
			errs = append(errs, errItem{Row: i + 1, Msg: "日期解析失败: " + row[0]})
			continue
		}
		isTrading := false
		switch row[1] {
		case "1", "是", "true", "Y", "y", "交易日":
			isTrading = true
		case "0", "否", "false", "N", "n":
			isTrading = false
		default:
			errs = append(errs, errItem{Row: i + 1, Msg: "是否交易日取值无法识别: " + row[1]})
			continue
		}
		var dayType string
		if len(row) > 2 && row[2] != "" {
			dt, ok := normalizeDayType(row[2])
			if !ok {
				errs = append(errs, errItem{Row: i + 1, Msg: "日类型取值无法识别: " + row[2]})
				continue
			}
			dayType = dt
		}
		if dayType == "" {
			if isTrading {
				dayType = "trading"
			} else if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
				dayType = "weekend"
			} else {
				dayType = "holiday"
			}
		}
		// 一致性: 交易日/调休须为交易日, 周末/节假日须非交易日
		if (dayType == "trading" || dayType == "makeup") != isTrading {
			errs = append(errs, errItem{Row: i + 1, Msg: "是否交易日与日类型不一致: " + row[1] + "/" + row[2]})
			continue
		}
		toSave = append(toSave, persistence.TradingDayCalendar{
			Market: "SH", CalDate: t, IsTrading: isTrading, DayType: dayType, Source: "excel",
		})
	}
	if len(errs) > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "导入校验失败 %d 行, 已回滚", len(errs)))
		c.JSON(http.StatusUnprocessableEntity, resp.Body{Code: resp.CodeUnprocessable, Message: "导入校验失败", Data: errs})
		return
	}
	tx := h.db.Begin()
	for _, r := range toSave {
		var n int64
		_ = tx.Model(&persistence.TradingDayCalendar{}).Where("market = ? AND cal_date = ?", r.Market, r.CalDate).Count(&n).Error
		if n == 0 {
			_ = tx.Create(&r).Error
		} else {
			_ = tx.Model(&persistence.TradingDayCalendar{}).
				Where("market = ? AND cal_date = ?", r.Market, r.CalDate).
				Updates(map[string]any{"is_trading": r.IsTrading, "day_type": r.DayType, "source": "excel"}).Error
		}
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	if len(toSave) > 0 {
		h.cal.InvalidateCache(c.Request.Context(), "SH", toSave[0].CalDate, toSave[len(toSave)-1].CalDate)
	}
	middleware.Log(c, "tradingday", "calendar.import", "SH", fmt.Sprintf("rows=%d", len(toSave)))
	resp.OK(c, gin.H{"saved": len(toSave), "errors": errs})
}

// Export 导出 Excel
func (h *Handler) Export(c *gin.Context) {
	market := c.DefaultQuery("market", "SH")
	from, to := dateRange(c, 365)
	var rows []persistence.TradingDayCalendar
	_ = h.db.Where("market = ? AND cal_date >= ? AND cal_date <= ?", market, from, to).Order("cal_date").Find(&rows).Error
	f := excelize.NewFile()
	sheet := "交易日日历"
	f.SetSheetName(f.GetSheetName(0), sheet)
	f.SetCellValue(sheet, "A1", "日期")
	f.SetCellValue(sheet, "B1", "是否交易日")
	f.SetCellValue(sheet, "C1", "日类型")
	type nameMap map[string]string
	dayTypeName := nameMap{"trading": "交易日", "weekend": "周末", "holiday": "节假日", "makeup": "调休交易日"}
	for i, r := range rows {
		row := i + 2
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), r.CalDate.Format("2006-01-02"))
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), map[bool]string{true: "是", false: "否"}[r.IsTrading])
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), dayTypeName[r.DayType])
	}
	middleware.Log(c, "tradingday", "calendar.export", market, "")
	c.Header("Content-Disposition", `attachment; filename="trading_days.xlsx"`)
	c.Writer.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_ = f.Write(c.Writer)
}

// Template 导入模板
func (h *Handler) Template(c *gin.Context) {
	f := excelize.NewFile()
	sheet := "交易日日历"
	f.SetSheetName(f.GetSheetName(0), sheet)
	f.SetCellValue(sheet, "A1", "日期")
	f.SetCellValue(sheet, "B1", "是否交易日")
	f.SetCellValue(sheet, "C1", "日类型")
	f.SetCellValue(sheet, "A2", "2026-11-02")
	f.SetCellValue(sheet, "B2", "是")
	f.SetCellValue(sheet, "C2", "交易日")
	c.Header("Content-Disposition", `attachment; filename="trading_day_template.xlsx"`)
	c.Writer.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_ = f.Write(c.Writer)
}

// Derive 推导 {date, market, action: prev|next|offset, n}
func (h *Handler) Derive(c *gin.Context) {
	var req struct {
		Date   string `json:"date" binding:"required"`
		Market string `json:"market"`
		Action string `json:"action" binding:"required,oneof=prev next offset"`
		N      int    `json:"n"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if req.Market == "" {
		req.Market = "SH"
	}
	d, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "日期格式错误"))
		return
	}
	ctx := c.Request.Context()
	var out time.Time
	switch req.Action {
	case "prev":
		out, err = h.cal.PrevTradingDay(ctx, req.Market, d)
	case "next":
		out, err = h.cal.NextTradingDay(ctx, req.Market, d)
	case "offset":
		if req.N == 0 {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "offset 模式 n 不能为 0"))
			return
		}
		out, err = h.cal.Offset(ctx, req.Market, d, req.N)
	}
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeUnprocessable, "推导失败: %v", err))
		return
	}
	resp.OK(c, gin.H{"date": out.Format("2006-01-02")})
}

func dateRange(c *gin.Context, defaultDays int) (time.Time, time.Time) {
	var from, to time.Time
	var err error
	if v := c.Query("from"); v != "" {
		from, err = time.Parse("2006-01-02", v)
	}
	if v := c.Query("to"); v != "" {
		if parsed, parseErr := time.Parse("2006-01-02", v); parseErr == nil {
			to = parsed.AddDate(0, 0, 1)
		}
	}
	now := clock.DayStart(time.Now())
	if from.IsZero() {
		from = now.AddDate(0, 0, -defaultDays)
	}
	if to.IsZero() {
		to = now.AddDate(0, 0, defaultDays+1)
	}
	_ = err
	return from, to
}

var _ = system.NewParams
