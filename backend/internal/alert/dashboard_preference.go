package alert

import (
	"fmt"
	"strings"

	"capacity-manage/backend/internal/persistence"
)

var (
	dashboardLevels     = []string{"p1", "p2", "p3", "p4"}
	dashboardStatuses   = []string{"active", "processing", "recovered", "closed"}
	dashboardTypes      = []string{"threshold", "missing", "collect_fail", "anomaly", "ds_error"}
	dashboardSortFields = map[string]string{
		"level":      "level",
		"status":     "status",
		"alertType":  "alert_type",
		"systemCode": "system_code",
		"triggerAt":  "trigger_at",
	}
)

type dashboardSortRuleInput struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type dashboardPreferenceInput struct {
	Levels      []string                 `json:"levels"`
	Statuses    []string                 `json:"statuses"`
	AlertTypes  []string                 `json:"alertTypes"`
	SystemCodes []string                 `json:"systemCodes"`
	DateMode    string                   `json:"dateMode"`
	RecentDays  int                      `json:"recentDays"`
	SortRules   []dashboardSortRuleInput `json:"sortRules"`
}

func defaultDashboardPreference(userID int64) persistence.AlertDashboardPreference {
	return persistence.AlertDashboardPreference{
		UserID:      userID,
		Levels:      persistence.StringList(dashboardLevels),
		Statuses:    persistence.StringList(dashboardStatuses),
		AlertTypes:  persistence.StringList(dashboardTypes),
		SystemCodes: persistence.StringList{},
		DateMode:    "today",
		RecentDays:  7,
		SortRules: persistence.AlertDashboardSortRules{
			{Field: "triggerAt", Direction: "desc"},
		},
	}
}

func validateDashboardPreference(in dashboardPreferenceInput, allowedSystems map[string]struct{}) (persistence.AlertDashboardPreference, error) {
	pref := defaultDashboardPreference(0)
	pref.Levels = nil
	pref.Statuses = nil
	pref.AlertTypes = nil
	pref.SystemCodes = nil
	pref.SortRules = nil

	var err error
	if pref.Levels, err = validateSet("levels", in.Levels, dashboardLevels); err != nil {
		return persistence.AlertDashboardPreference{}, err
	}
	if pref.Statuses, err = validateSet("statuses", in.Statuses, dashboardStatuses); err != nil {
		return persistence.AlertDashboardPreference{}, err
	}
	if pref.AlertTypes, err = validateSet("alertTypes", in.AlertTypes, dashboardTypes); err != nil {
		return persistence.AlertDashboardPreference{}, err
	}
	if pref.SystemCodes, err = validateSet("systemCodes", in.SystemCodes, nil); err != nil {
		return persistence.AlertDashboardPreference{}, err
	}
	for _, code := range pref.SystemCodes {
		if _, ok := allowedSystems[code]; !ok {
			return persistence.AlertDashboardPreference{}, fmt.Errorf("systemCodes 包含不可见或已删除系统: %s", code)
		}
	}

	pref.DateMode = strings.TrimSpace(in.DateMode)
	if pref.DateMode == "" {
		pref.DateMode = "today"
	}
	if pref.DateMode != "today" && pref.DateMode != "recent_days" {
		return persistence.AlertDashboardPreference{}, fmt.Errorf("dateMode 只允许 today 或 recent_days")
	}
	pref.RecentDays = in.RecentDays
	if pref.DateMode == "today" && pref.RecentDays == 0 {
		pref.RecentDays = 7
	}
	if pref.RecentDays < 1 || pref.RecentDays > 365 {
		return persistence.AlertDashboardPreference{}, fmt.Errorf("recentDays 必须在 1 至 365 之间")
	}

	if len(in.SortRules) == 0 {
		pref.SortRules = persistence.AlertDashboardSortRules{{Field: "triggerAt", Direction: "desc"}}
		return pref, nil
	}
	if len(in.SortRules) > len(dashboardSortFields) {
		return persistence.AlertDashboardPreference{}, fmt.Errorf("sortRules 最多允许 %d 条", len(dashboardSortFields))
	}
	seen := map[string]struct{}{}
	for _, rule := range in.SortRules {
		if _, ok := dashboardSortFields[rule.Field]; !ok {
			return persistence.AlertDashboardPreference{}, fmt.Errorf("sortRules.field 非法: %s", rule.Field)
		}
		if rule.Direction != "asc" && rule.Direction != "desc" {
			return persistence.AlertDashboardPreference{}, fmt.Errorf("sortRules.direction 只允许 asc 或 desc")
		}
		if _, ok := seen[rule.Field]; ok {
			return persistence.AlertDashboardPreference{}, fmt.Errorf("sortRules 不允许重复字段: %s", rule.Field)
		}
		seen[rule.Field] = struct{}{}
		pref.SortRules = append(pref.SortRules, persistence.AlertDashboardSortRule{Field: rule.Field, Direction: rule.Direction})
	}
	return pref, nil
}

func validateSet(field string, values, allowed []string) (persistence.StringList, error) {
	allowedSet := map[string]struct{}{}
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	seen := map[string]struct{}{}
	out := persistence.StringList{}
	for _, value := range values {
		if allowed != nil {
			if _, ok := allowedSet[value]; !ok {
				return nil, fmt.Errorf("%s 包含非法值: %s", field, value)
			}
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func sanitizeDashboardPreference(pref persistence.AlertDashboardPreference, userID int64) persistence.AlertDashboardPreference {
	fallback := defaultDashboardPreference(userID)
	pref.UserID = userID
	if got, err := validateSet("levels", pref.Levels, dashboardLevels); err != nil || len(pref.Levels) == 0 {
		pref.Levels = fallback.Levels
	} else {
		pref.Levels = got
	}
	if got, err := validateSet("statuses", pref.Statuses, dashboardStatuses); err != nil || len(pref.Statuses) == 0 {
		pref.Statuses = fallback.Statuses
	} else {
		pref.Statuses = got
	}
	if got, err := validateSet("alertTypes", pref.AlertTypes, dashboardTypes); err != nil || len(pref.AlertTypes) == 0 {
		pref.AlertTypes = fallback.AlertTypes
	} else {
		pref.AlertTypes = got
	}
	if pref.DateMode != "today" && pref.DateMode != "recent_days" {
		pref.DateMode = fallback.DateMode
	}
	if pref.RecentDays < 1 || pref.RecentDays > 365 {
		pref.RecentDays = fallback.RecentDays
	}
	if len(pref.SortRules) == 0 || len(pref.SortRules) > len(dashboardSortFields) {
		pref.SortRules = fallback.SortRules
	} else {
		seen := map[string]struct{}{}
		ok := true
		for _, rule := range pref.SortRules {
			if _, known := dashboardSortFields[rule.Field]; !known || (rule.Direction != "asc" && rule.Direction != "desc") {
				ok = false
				break
			}
			if _, duplicate := seen[rule.Field]; duplicate {
				ok = false
				break
			}
			seen[rule.Field] = struct{}{}
		}
		if !ok {
			pref.SortRules = fallback.SortRules
		}
	}
	if pref.SystemCodes == nil {
		pref.SystemCodes = persistence.StringList{}
	}
	return pref
}
