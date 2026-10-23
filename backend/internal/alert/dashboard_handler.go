package alert

import (
	"errors"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (h *Handler) DashboardPreference(c *gin.Context) {
	uid, ok := dashboardUserID(c)
	if !ok {
		return
	}
	pref, err := h.loadDashboardPreference(uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, pref)
}

func (h *Handler) SaveDashboardPreference(c *gin.Context) {
	uid, ok := dashboardUserID(c)
	if !ok {
		return
	}
	var in dashboardPreferenceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	allowed, _, err := h.allowedDashboardSystems(c)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	pref, err := validateDashboardPreference(in, allowed)
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "%v", err))
		return
	}
	pref.UserID = uid
	now := h.currentTime()
	err = h.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"levels": pref.Levels, "statuses": pref.Statuses, "alert_types": pref.AlertTypes,
			"system_codes": pref.SystemCodes, "date_mode": pref.DateMode, "recent_days": pref.RecentDays,
			"sort_rules": pref.SortRules, "updated_at": now,
		}),
	}).Create(&pref).Error
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "alert", "alert.dashboard.preference.save", "", "")
	resp.OK(c, pref)
}

func (h *Handler) Dashboard(c *gin.Context) {
	uid, ok := dashboardUserID(c)
	if !ok {
		return
	}
	pref, err := h.loadDashboardPreference(uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	visible := h.scopeSystems(c)
	systemCodes, none := dashboardSystemScope(pref.SystemCodes, visible)

	page, size := 1, 20
	if p, s, ok := resp.ParsePage(c, 20, 200); ok {
		page, size = p, s
	}
	if none {
		resp.OK(c, resp.Page([]persistence.AlertRecord{}, 0, page, size))
		return
	}

	q := h.db.Model(&persistence.AlertRecord{})
	q = applyDashboardQuery(q, pref, h.currentTime())
	if systemCodes != nil {
		q = applyScope(q, systemCodes)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	var items []persistence.AlertRecord
	q = applyDashboardSort(q, pref.SortRules)
	if err := q.Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, resp.Page(items, total, page, size))
}

func (h *Handler) DashboardOptions(c *gin.Context) {
	_, systems, err := h.allowedDashboardSystems(c)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"systems": systems})
}

func dashboardUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get(middleware.CtxUserID)
	id, typeOK := v.(int64)
	if !ok || !typeOK || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeUnauthorized, "未认证"))
		return 0, false
	}
	return id, true
}

func (h *Handler) loadDashboardPreference(userID int64) (persistence.AlertDashboardPreference, error) {
	var pref persistence.AlertDashboardPreference
	err := h.db.Where("user_id = ?", userID).First(&pref).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultDashboardPreference(userID), nil
	}
	if err != nil {
		return persistence.AlertDashboardPreference{}, err
	}
	return sanitizeDashboardPreference(pref, userID), nil
}

type dashboardSystemOption struct {
	SystemCode string `json:"systemCode"`
	Name       string `json:"name"`
}

func (h *Handler) allowedDashboardSystems(c *gin.Context) (map[string]struct{}, []dashboardSystemOption, error) {
	visible := h.scopeSystems(c)
	q := h.db.Model(&persistence.BusinessSystem{})
	if visible != nil {
		if len(visible) == 0 {
			return map[string]struct{}{}, []dashboardSystemOption{}, nil
		}
		q = q.Where("system_code IN ?", visible)
	}
	var systems []dashboardSystemOption
	if err := q.Select("system_code, name").Order("system_code").Scan(&systems).Error; err != nil {
		return nil, nil, err
	}
	allowed := make(map[string]struct{}, len(systems))
	for _, system := range systems {
		allowed[system.SystemCode] = struct{}{}
	}
	return allowed, systems, nil
}

// nil visible means platform administrator with no data-scope restriction.
func dashboardSystemScope(saved, visible []string) ([]string, bool) {
	if len(saved) == 0 {
		if visible == nil {
			return nil, false
		}
		if len(visible) == 0 {
			return []string{}, true
		}
		return visible, false
	}
	if visible == nil {
		return saved, false
	}
	allowed := map[string]struct{}{}
	for _, code := range visible {
		allowed[code] = struct{}{}
	}
	out := make([]string, 0, len(saved))
	for _, code := range saved {
		if _, ok := allowed[code]; ok {
			out = append(out, code)
		}
	}
	if len(out) == 0 {
		return []string{}, true
	}
	return out, false
}

func applyDashboardQuery(q *gorm.DB, pref persistence.AlertDashboardPreference, now time.Time) *gorm.DB {
	if len(pref.Levels) > 0 {
		q = q.Where("level IN ?", []string(pref.Levels))
	}
	if len(pref.Statuses) > 0 {
		q = q.Where("status IN ?", []string(pref.Statuses))
	}
	if len(pref.AlertTypes) > 0 {
		q = q.Where("alert_type IN ?", []string(pref.AlertTypes))
	}
	start := clock.DayStart(now)
	if pref.DateMode == "recent_days" {
		start = start.AddDate(0, 0, -(pref.RecentDays - 1))
	}
	return q.Where("trigger_at >= ? AND trigger_at < ?", start, clock.DayStart(now).AddDate(0, 0, 1))
}

func applyDashboardSort(q *gorm.DB, rules persistence.AlertDashboardSortRules) *gorm.DB {
	if len(rules) == 0 {
		rules = persistence.AlertDashboardSortRules{{Field: "triggerAt", Direction: "desc"}}
	}
	for _, rule := range rules {
		switch rule.Field {
		case "level":
			q = q.Order("CASE level WHEN 'p1' THEN 1 WHEN 'p2' THEN 2 WHEN 'p3' THEN 3 WHEN 'p4' THEN 4 ELSE 99 END " + rule.Direction)
		case "status":
			q = q.Order("CASE status WHEN 'active' THEN 1 WHEN 'processing' THEN 2 WHEN 'recovered' THEN 3 WHEN 'closed' THEN 4 ELSE 99 END " + rule.Direction)
		default:
			column := dashboardSortFields[rule.Field]
			if column != "" {
				q = q.Order(column + " " + rule.Direction)
			}
		}
	}
	return q.Order("id DESC")
}

func (h *Handler) currentTime() time.Time {
	if h.now == nil {
		return time.Now()
	}
	return h.now()
}
