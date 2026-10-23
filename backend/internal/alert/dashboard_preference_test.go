package alert

import (
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
)

func TestDefaultDashboardPreference(t *testing.T) {
	got := defaultDashboardPreference(42)
	if got.UserID != 42 || got.DateMode != "today" || got.RecentDays != 7 {
		t.Fatalf("defaults = %+v", got)
	}
	if len(got.Levels) != 4 || len(got.Statuses) != 4 || len(got.AlertTypes) != 5 {
		t.Fatalf("default sets = %+v", got)
	}
	if len(got.SortRules) != 1 || got.SortRules[0].Field != "triggerAt" || got.SortRules[0].Direction != "desc" {
		t.Fatalf("default sort = %+v", got.SortRules)
	}
}

func TestValidateDashboardPreferenceDeduplicatesAndDefaultsSort(t *testing.T) {
	got, err := validateDashboardPreference(dashboardPreferenceInput{
		Levels:     []string{"p2", "p1", "p2"},
		Statuses:   []string{"active"},
		AlertTypes: []string{"threshold"},
		DateMode:   "recent_days",
		RecentDays: 14,
	}, map[string]struct{}{"SYS_A": {}})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(got.Levels) != 2 || got.Levels[0] != "p2" || got.Levels[1] != "p1" {
		t.Fatalf("levels = %+v", got.Levels)
	}
	if len(got.SortRules) != 1 || got.SortRules[0].Field != "triggerAt" {
		t.Fatalf("sort = %+v", got.SortRules)
	}
}

func TestValidateDashboardPreferenceRejectsInvalidFields(t *testing.T) {
	allowed := map[string]struct{}{"SYS_A": {}}
	cases := []struct {
		name string
		in   dashboardPreferenceInput
		want string
	}{
		{"level", dashboardPreferenceInput{Levels: []string{"p9"}}, "levels"},
		{"status", dashboardPreferenceInput{Statuses: []string{"open"}}, "statuses"},
		{"type", dashboardPreferenceInput{AlertTypes: []string{"unknown"}}, "alertTypes"},
		{"system", dashboardPreferenceInput{SystemCodes: []string{"SYS_B"}}, "systemCodes"},
		{"date mode", dashboardPreferenceInput{DateMode: "month"}, "dateMode"},
		{"recent days low", dashboardPreferenceInput{DateMode: "recent_days", RecentDays: 0}, "recentDays"},
		{"recent days high", dashboardPreferenceInput{DateMode: "recent_days", RecentDays: 366}, "recentDays"},
		{"sort field", dashboardPreferenceInput{SortRules: []dashboardSortRuleInput{{Field: "alertNo", Direction: "asc"}}}, "sortRules.field"},
		{"sort direction", dashboardPreferenceInput{SortRules: []dashboardSortRuleInput{{Field: "level", Direction: "up"}}}, "sortRules.direction"},
		{"sort duplicate", dashboardPreferenceInput{SortRules: []dashboardSortRuleInput{
			{Field: "level", Direction: "asc"},
			{Field: "level", Direction: "desc"},
		}}, "sortRules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateDashboardPreference(tc.in, allowed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestSanitizeDashboardPreferenceFallsBackInvalidValues(t *testing.T) {
	got := sanitizeDashboardPreference(persistence.AlertDashboardPreference{
		UserID:      7,
		Levels:      persistence.StringList{"bad"},
		Statuses:    persistence.StringList{"active"},
		AlertTypes:  persistence.StringList{"threshold"},
		SystemCodes: persistence.StringList{"SYS_REVOKED"},
		DateMode:    "month",
		RecentDays:  0,
		SortRules:   persistence.AlertDashboardSortRules{{Field: "level", Direction: "up"}},
	}, 7)
	if got.UserID != 7 || got.DateMode != "today" || got.RecentDays != 7 {
		t.Fatalf("sanitized defaults = %+v", got)
	}
	if len(got.Levels) != 4 {
		t.Fatalf("levels should fall back to all levels: %+v", got.Levels)
	}
	if len(got.SortRules) != 1 || got.SortRules[0].Field != "triggerAt" {
		t.Fatalf("sort should fall back: %+v", got.SortRules)
	}
	if len(got.SystemCodes) != 1 || got.SystemCodes[0] != "SYS_REVOKED" {
		t.Fatalf("saved system codes must be preserved: %+v", got.SystemCodes)
	}
}
