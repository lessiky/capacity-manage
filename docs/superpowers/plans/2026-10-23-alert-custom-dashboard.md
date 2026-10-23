# Alert Custom Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-user custom alert dashboard with multi-value filters, configurable date ranges, multi-field sorting, backend persistence, and automatic restoration after login.

**Architecture:** Add one user-keyed preference table and a set of `/api/v1/alerts/dashboard*` endpoints. The backend validates and stores preferences, then executes alert queries from the saved configuration while applying the existing alert data scope. The frontend adds a focused `AlertDashboard.vue` component inside the existing alert center, leaving login routing and the original alert tabs unchanged.

**Tech Stack:** Go 1.26, Gin, GORM, SQLite tests, PostgreSQL/JSONB production, Vue 3, TypeScript, Element Plus, Playwright, pytest.

**Spec:** `docs/superpowers/specs/2026-10-23-alert-custom-dashboard-design.md`

## Global Constraints

- Login continues redirecting to `/dashboard`; only the alert center defaults to the custom dashboard tab.
- Existing alert list, statistics, policy, claim, note, close, export, and notification behavior must remain unchanged.
- All new endpoints require `alert:view`; user identity comes from `middleware.CtxUserID`, never from a request field.
- Empty multi-value arrays mean no filter for that field.
- Default date mode is `today`; default recent-day count is `7`.
- Default sort is `triggerAt desc`.
- Allowed sort fields are `level`, `status`, `alertType`, `systemCode`, and `triggerAt`; directions are `asc` and `desc`.
- Level order is P1, P2, P3, P4. Status order is active, processing, recovered, closed.
- Query sorting always appends `id DESC`.
- Saved system codes are intersected with current visible systems; a non-empty saved set that intersects to empty returns no rows.
- No new frontend or backend dependency.
- Use `persistence.StringList` for JSON string arrays.
- Migration number is `0031`.

---

### Task 1: Persistence Model and Migration

**Files:**
- Create: `backend/internal/persistence/alert_dashboard.go`
- Create: `db/migrations/0031_alert_dashboard_preference.sql`
- Test: `backend/internal/persistence/migrate_alert_dashboard_test.go`

**Interfaces:**
- Consumes: `persistence.StringList` from `backend/internal/persistence/types.go`.
- Produces:
  - `type AlertDashboardSortRule struct { Field string; Direction string }`
  - `type AlertDashboardSortRules []AlertDashboardSortRule`
  - `type AlertDashboardPreference struct`
  - `func (r AlertDashboardSortRules) Value() (driver.Value, error)`
  - `func (r *AlertDashboardSortRules) Scan(src any) error`
  - `func (r AlertDashboardSortRules) MarshalJSON() ([]byte, error)`
  - `func (r *AlertDashboardSortRules) UnmarshalJSON(data []byte) error`

- [ ] **Step 1: Write the failing migration and JSON round-trip tests**

Create `backend/internal/persistence/migrate_alert_dashboard_test.go`:

```go
package persistence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func migrateAlertDashboard(t *testing.T, db *gorm.DB) {
	t.Helper()
	migration := filepath.Join("..", "..", "..", "db", "migrations", "0031_alert_dashboard_preference.sql")
	raw, err := os.ReadFile(migration)
	if err != nil {
		t.Fatalf("read alert dashboard migration: %v", err)
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		one := strings.TrimSpace(statement)
		if one == "" || isCommentOnly(one) || strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
			continue
		}
		if err := db.Exec(one).Error; err != nil {
			t.Fatalf("apply alert dashboard migration statement: %v", err)
		}
	}
}

func TestAlertDashboardPreferenceMigrationDefaults(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:alert-dashboard-migration?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysUser{}); err != nil {
		t.Fatalf("migrate sys_user: %v", err)
	}
	user := SysUser{Username: "dashboard-user", PasswordHash: "x", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	migrateAlertDashboard(t, db)

	if err := db.Exec("INSERT INTO alert_dashboard_preference (user_id) VALUES (?)", user.ID).Error; err != nil {
		t.Fatalf("insert default preference: %v", err)
	}
	var got struct {
		Levels     string `gorm:"column:levels"`
		Statuses   string `gorm:"column:statuses"`
		AlertTypes string `gorm:"column:alert_types"`
		SystemCode string `gorm:"column:system_codes"`
		DateMode   string `gorm:"column:date_mode"`
		RecentDays int    `gorm:"column:recent_days"`
		SortRules  string `gorm:"column:sort_rules"`
	}
	err = db.Raw(`SELECT levels, statuses, alert_types, system_codes, date_mode, recent_days, sort_rules
		FROM alert_dashboard_preference WHERE user_id = ?`, user.ID).Scan(&got).Error
	if err != nil {
		t.Fatalf("read default preference: %v", err)
	}
	if got.Levels != `["p1","p2","p3","p4"]` {
		t.Fatalf("levels = %s", got.Levels)
	}
	if got.Statuses != `["active","processing","recovered","closed"]` {
		t.Fatalf("statuses = %s", got.Statuses)
	}
	if got.AlertTypes != `["threshold","missing","collect_fail","anomaly","ds_error"]` {
		t.Fatalf("alert types = %s", got.AlertTypes)
	}
	if got.SystemCode != `[]` {
		t.Fatalf("system codes = %s", got.SystemCode)
	}
	if got.DateMode != "today" || got.RecentDays != 7 {
		t.Fatalf("date defaults = %s/%d", got.DateMode, got.RecentDays)
	}
	if got.SortRules != `[{"field":"triggerAt","direction":"desc"}]` {
		t.Fatalf("sort rules = %s", got.SortRules)
	}
}

func TestAlertDashboardPreferenceMigrationChecksDateBounds(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:alert-dashboard-check?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&SysUser{}); err != nil {
		t.Fatalf("migrate sys_user: %v", err)
	}
	user := SysUser{Username: "check-user", PasswordHash: "x", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	migrateAlertDashboard(t, db)

	if err := db.Exec(`INSERT INTO alert_dashboard_preference (user_id, recent_days) VALUES (?, 0)`, user.ID).Error; err == nil {
		t.Fatal("recent_days=0 must violate the migration check constraint")
	}
	if err := db.Exec(`INSERT INTO alert_dashboard_preference (user_id, date_mode) VALUES (?, 'yesterday')`, user.ID).Error; err == nil {
		t.Fatal("date_mode=yesterday must violate the migration check constraint")
	}
}

func TestAlertDashboardSortRulesJSONRoundTrip(t *testing.T) {
	want := AlertDashboardSortRules{
		{Field: "level", Direction: "asc"},
		{Field: "triggerAt", Direction: "desc"},
	}
	value, err := want.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	var got AlertDashboardSortRules
	if err := got.Scan(value); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 2 || got[0].Field != "level" || got[1].Direction != "desc" {
		t.Fatalf("round trip = %+v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```powershell
go test ./backend/internal/persistence/... -run 'TestAlertDashboard' -count=1
```

Expected: FAIL because `AlertDashboardSortRules` and `0031_alert_dashboard_preference.sql` do not exist.

- [ ] **Step 3: Add the persistence model**

Create `backend/internal/persistence/alert_dashboard.go`:

```go
package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AlertDashboardSortRule is persisted in sort order. Index order is priority.
type AlertDashboardSortRule struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type AlertDashboardSortRules []AlertDashboardSortRule

func (r AlertDashboardSortRules) Value() (driver.Value, error) {
	if len(r) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal([]AlertDashboardSortRule(r))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (r *AlertDashboardSortRules) Scan(src any) error {
	if src == nil {
		*r = AlertDashboardSortRules{}
		return nil
	}
	var raw string
	switch v := src.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return fmt.Errorf("AlertDashboardSortRules: unsupported scan type %T", src)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		*r = AlertDashboardSortRules{}
		return nil
	}
	var out []AlertDashboardSortRule
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return fmt.Errorf("AlertDashboardSortRules: parse %q: %w", raw, err)
	}
	*r = out
	return nil
}

func (r AlertDashboardSortRules) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]AlertDashboardSortRule(r))
}

func (r *AlertDashboardSortRules) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*r = AlertDashboardSortRules{}
		return nil
	}
	var out []AlertDashboardSortRule
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*r = out
	return nil
}

type AlertDashboardPreference struct {
	UserID      int64                   `gorm:"primaryKey" json:"userId"`
	Levels      StringList              `gorm:"type:jsonb" json:"levels"`
	Statuses    StringList              `gorm:"type:jsonb" json:"statuses"`
	AlertTypes  StringList              `gorm:"type:jsonb" json:"alertTypes"`
	SystemCodes StringList              `gorm:"type:jsonb" json:"systemCodes"`
	DateMode    string                  `gorm:"size:16;default:today" json:"dateMode"`
	RecentDays  int                     `gorm:"default:7" json:"recentDays"`
	SortRules   AlertDashboardSortRules `gorm:"type:jsonb" json:"sortRules"`
	CreatedAt   time.Time               `json:"createdAt"`
	UpdatedAt   time.Time               `json:"updatedAt"`
}
```

- [ ] **Step 4: Add the migration**

Create `db/migrations/0031_alert_dashboard_preference.sql`:

```sql
-- Alert center custom dashboard preferences, one row per user.
CREATE TABLE IF NOT EXISTS alert_dashboard_preference (
  user_id      bigint PRIMARY KEY REFERENCES sys_user(id) ON DELETE CASCADE,
  levels       jsonb NOT NULL DEFAULT '["p1","p2","p3","p4"]',
  statuses     jsonb NOT NULL DEFAULT '["active","processing","recovered","closed"]',
  alert_types  jsonb NOT NULL DEFAULT '["threshold","missing","collect_fail","anomaly","ds_error"]',
  system_codes jsonb NOT NULL DEFAULT '[]',
  date_mode    varchar(16) NOT NULL DEFAULT 'today',
  recent_days  int NOT NULL DEFAULT 7,
  sort_rules   jsonb NOT NULL DEFAULT '[{"field":"triggerAt","direction":"desc"}]',
  created_at   timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at   timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT chk_alert_dashboard_date_mode CHECK (date_mode IN ('today', 'recent_days')),
  CONSTRAINT chk_alert_dashboard_recent_days CHECK (recent_days BETWEEN 1 AND 365)
);

CREATE INDEX IF NOT EXISTS idx_alert_dashboard_updated_at
  ON alert_dashboard_preference (updated_at DESC);
```

- [ ] **Step 5: Run the focused tests**

Run:

```powershell
go test ./backend/internal/persistence/... -run 'TestAlertDashboard' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/persistence/alert_dashboard.go backend/internal/persistence/migrate_alert_dashboard_test.go db/migrations/0031_alert_dashboard_preference.sql
git commit -m "feat: add alert dashboard preference storage"
```

---

### Task 2: Preference Validation and Normalization

**Files:**
- Create: `backend/internal/alert/dashboard_preference.go`
- Test: `backend/internal/alert/dashboard_preference_test.go`

**Interfaces:**
- Consumes: `persistence.AlertDashboardPreference`, `persistence.AlertDashboardSortRules`, and `persistence.StringList` from Task 1.
- Produces:
  - `type dashboardSortRuleInput struct { Field string; Direction string }`
  - `type dashboardPreferenceInput struct`
  - `func defaultDashboardPreference(userID int64) persistence.AlertDashboardPreference`
  - `func validateDashboardPreference(in dashboardPreferenceInput, allowedSystems map[string]struct{}) (persistence.AlertDashboardPreference, error)`
  - `func sanitizeDashboardPreference(pref persistence.AlertDashboardPreference, userID int64) persistence.AlertDashboardPreference`

- [ ] **Step 1: Write the failing validation tests**

Create `backend/internal/alert/dashboard_preference_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```powershell
go test ./backend/internal/alert/... -run 'TestDefaultDashboardPreference|TestValidateDashboardPreference|TestSanitizeDashboardPreference' -count=1
```

Expected: FAIL because the dashboard preference types and functions do not exist.

- [ ] **Step 3: Implement the validation layer**

Create `backend/internal/alert/dashboard_preference.go`:

```go
package alert

import (
	"fmt"
	"strings"

	"capacity-manage/backend/internal/persistence"
)

var (
	dashboardLevels   = []string{"p1", "p2", "p3", "p4"}
	dashboardStatuses = []string{"active", "processing", "recovered", "closed"}
	dashboardTypes    = []string{"threshold", "missing", "collect_fail", "anomaly", "ds_error"}
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
	if pref.RecentDays == 0 {
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
```

- [ ] **Step 4: Run the focused tests**

Run:

```powershell
go test ./backend/internal/alert/... -run 'TestDefaultDashboardPreference|TestValidateDashboardPreference|TestSanitizeDashboardPreference' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/alert/dashboard_preference.go backend/internal/alert/dashboard_preference_test.go
git commit -m "feat: validate alert dashboard preferences"
```

---

### Task 3: Dashboard Handlers and Query Execution

**Files:**
- Modify: `backend/internal/alert/handler.go`
- Create: `backend/internal/alert/dashboard_handler.go`
- Test: `backend/internal/alert/dashboard_handler_test.go`

**Interfaces:**
- Consumes: `defaultDashboardPreference`, `validateDashboardPreference`, `sanitizeDashboardPreference`, `dashboardPreferenceInput`, and `dashboardSortFields`.
- Produces:
  - `func (h *Handler) DashboardPreference(c *gin.Context)`
  - `func (h *Handler) SaveDashboardPreference(c *gin.Context)`
  - `func (h *Handler) Dashboard(c *gin.Context)`
  - `func (h *Handler) DashboardOptions(c *gin.Context)`
  - `func dashboardSystemScope(saved, visible []string) ([]string, bool)`
  - `func applyDashboardQuery(q *gorm.DB, pref persistence.AlertDashboardPreference, now time.Time) *gorm.DB`
  - `func applyDashboardSort(q *gorm.DB, rules persistence.AlertDashboardSortRules) *gorm.DB`

- [ ] **Step 1: Add the deterministic clock seam**

Modify `backend/internal/alert/handler.go`:

```go
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
```

Keep the existing `time` import.

Also change the start of `scopeSystems` from `var codes []string` to:

```go
codes := []string{}
```

This preserves the intended security rule: a non-platform-admin with no data
scopes has zero visible systems instead of being treated as unrestricted.

- [ ] **Step 2: Write the failing handler tests**

Create `backend/internal/alert/dashboard_handler_test.go`:

```go
package alert

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func newDashboardHarness(t *testing.T, username string, admin bool) (*Handler, *testutil.DBHarness) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(
		&persistence.SysUser{}, &persistence.SysRole{}, &persistence.SysUserRole{},
		&persistence.SysRolePermission{}, &persistence.SysPermission{}, &persistence.SysDataScope{},
		&persistence.BusinessSystem{}, &persistence.AlertRecord{}, &persistence.AlertDashboardPreference{},
	)
	user := persistence.SysUser{Username: username, PasswordHash: "x", RealName: username, Status: "active"}
	if err := h.DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if admin {
		role := persistence.SysRole{Code: "platform_admin", Name: "平台管理员", Builtin: true, Status: "active"}
		if err := h.DB.Create(&role).Error; err != nil {
			t.Fatalf("seed role: %v", err)
		}
		if err := h.DB.Create(&persistence.SysUserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
			t.Fatalf("seed user role: %v", err)
		}
	}
	az, err := middleware.NewAuthZ(h.DB)
	if err != nil {
		t.Fatalf("new authz: %v", err)
	}
	handler := NewHandler(h.DB, az, system.NewParams(h.DB), nil)
	handler.now = func() time.Time {
		return time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	}
	return handler, h
}

func dashboardContext(t *testing.T, method, target, username string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	c.Request = httptest.NewRequest(method, target, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(middleware.CtxUserID, int64(1))
	c.Set(middleware.CtxUsername, username)
	return c, w
}

func dashboardBody(t *testing.T, w *httptest.ResponseRecorder) resp.Body {
	t.Helper()
	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v %s", err, w.Body.String())
	}
	return body
}

func TestDashboardPreferenceDefaultsAndRoundTrip(t *testing.T) {
	h, db := newDashboardHarness(t, "admin", true)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard-preference", "admin", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.DashboardPreference(c)
	first := dashboardBody(t, w)
	if first.Code != resp.CodeOK {
		t.Fatalf("get default: %+v", first)
	}

	c, w = dashboardContext(t, http.MethodPut, "/api/v1/alerts/dashboard-preference", "admin", map[string]any{
		"levels":      []string{"p1", "p2"},
		"statuses":    []string{"active"},
		"alertTypes":  []string{"threshold"},
		"systemCodes": []string{},
		"dateMode":    "recent_days",
		"recentDays":  14,
		"sortRules": []map[string]string{
			{"field": "level", "direction": "asc"},
			{"field": "triggerAt", "direction": "desc"},
		},
	})
	c.Set(middleware.CtxUserID, user.ID)
	h.SaveDashboardPreference(c)
	if got := dashboardBody(t, w); got.Code != resp.CodeOK {
		t.Fatalf("put preference: %+v", got)
	}

	c, w = dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard-preference", "admin", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.DashboardPreference(c)
	again := dashboardBody(t, w)
	if again.Code != resp.CodeOK {
		t.Fatalf("get saved: %+v", again)
	}
	raw, _ := json.Marshal(again.Data)
	if !bytes.Contains(raw, []byte(`"recentDays":14`)) || !bytes.Contains(raw, []byte(`"level"`)) {
		t.Fatalf("saved preference not returned: %s", raw)
	}
}

func TestSaveDashboardPreferenceRejectsSystemOutsidePermission(t *testing.T) {
	h, db := newDashboardHarness(t, "ops", false)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "ops").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_A", Name: "A", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_B", Name: "B", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.SysDataScope{UserID: &user.ID, SystemCode: "SYS_A", ScopeType: "view"}).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodPut, "/api/v1/alerts/dashboard-preference", "ops", map[string]any{
		"systemCodes": []string{"SYS_B"},
		"dateMode":    "today",
		"recentDays":  7,
	})
	c.Set(middleware.CtxUserID, user.ID)
	h.SaveDashboardPreference(c)
	if got := dashboardBody(t, w); got.Code != resp.CodeBadRequest {
		t.Fatalf("save outside scope = %+v", got)
	}
}

func TestDashboardAppliesMultiFilterAndMultiSort(t *testing.T) {
	h, db := newDashboardHarness(t, "admin", true)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	rows := []persistence.AlertRecord{
		{AlertNo: "A1", MetricCode: "M1", SystemCode: "SYS_A", Level: "p2", AlertType: "threshold", Status: "active", TriggerAt: base.Add(-2 * time.Hour)},
		{AlertNo: "A2", MetricCode: "M1", SystemCode: "SYS_A", Level: "p1", AlertType: "threshold", Status: "active", TriggerAt: base.Add(-1 * time.Hour)},
		{AlertNo: "A3", MetricCode: "M1", SystemCode: "SYS_A", Level: "p1", AlertType: "threshold", Status: "active", TriggerAt: base.Add(-3 * time.Hour)},
		{AlertNo: "A4", MetricCode: "M1", SystemCode: "SYS_A", Level: "p1", AlertType: "missing", Status: "active", TriggerAt: base},
		{AlertNo: "A5", MetricCode: "M1", SystemCode: "SYS_B", Level: "p1", AlertType: "threshold", Status: "active", TriggerAt: base},
	}
	for i := range rows {
		rows[i].DurationMin = intPtr(0)
		if err := db.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed alert: %v", err)
		}
	}
	if err := db.DB.Save(&persistence.AlertDashboardPreference{
		UserID: user.ID, Levels: persistence.StringList{"p1", "p2"},
		Statuses: persistence.StringList{"active"}, AlertTypes: persistence.StringList{"threshold"},
		SystemCodes: persistence.StringList{"SYS_A"}, DateMode: "today", RecentDays: 7,
		SortRules: persistence.AlertDashboardSortRules{
			{Field: "level", Direction: "asc"},
			{Field: "triggerAt", Direction: "desc"},
		},
	}).Error; err != nil {
		t.Fatalf("save preference: %v", err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard?page=1&pageSize=20", "admin", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.Dashboard(c)
	body := dashboardBody(t, w)
	if body.Code != resp.CodeOK {
		t.Fatalf("dashboard: %+v", body)
	}
	raw, _ := json.Marshal(body.Data)
	var page struct {
		Items []persistence.AlertRecord `json:"items"`
		Total int64                     `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("total = %d, want 3", page.Total)
	}
	if page.Items[0].AlertNo != "A2" || page.Items[1].AlertNo != "A3" || page.Items[2].AlertNo != "A1" {
		t.Fatalf("order = [%s %s %s]", page.Items[0].AlertNo, page.Items[1].AlertNo, page.Items[2].AlertNo)
	}
}

func TestDashboardReturnsEmptyWhenSavedSystemsAreAllRevoked(t *testing.T) {
	h, db := newDashboardHarness(t, "ops", false)
	user := persistence.SysUser{}
	if err := db.DB.Where("username = ?", "ops").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_A", Name: "A", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.BusinessSystem{SystemCode: "SYS_B", Name: "B", Owner: "ops", Status: "normal"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&persistence.SysDataScope{UserID: &user.ID, SystemCode: "SYS_B", ScopeType: "view"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Save(&persistence.AlertDashboardPreference{
		UserID: user.ID, Levels: persistence.StringList{"p1"},
		Statuses: persistence.StringList{"active"}, AlertTypes: persistence.StringList{"threshold"},
		SystemCodes: persistence.StringList{"SYS_A"}, DateMode: "today", RecentDays: 7,
		SortRules: persistence.AlertDashboardSortRules{{Field: "triggerAt", Direction: "desc"}},
	}).Error; err != nil {
		t.Fatal(err)
	}

	c, w := dashboardContext(t, http.MethodGet, "/api/v1/alerts/dashboard?page=1&pageSize=20", "ops", nil)
	c.Set(middleware.CtxUserID, user.ID)
	h.Dashboard(c)
	body := dashboardBody(t, w)
	raw, _ := json.Marshal(body.Data)
	var page struct {
		Items []persistence.AlertRecord `json:"items"`
		Total int64                     `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("revoked scope returned rows: %+v", page)
	}
}
```

- [ ] **Step 3: Run the handler tests to verify they fail**

Run:

```powershell
go test ./backend/internal/alert/... -run 'TestDashboard|TestSaveDashboard' -count=1
```

Expected: FAIL because the dashboard handlers do not exist.

- [ ] **Step 4: Implement dashboard handlers**

Create `backend/internal/alert/dashboard_handler.go`:

```go
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
	now := h.now()
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
	q = applyDashboardQuery(q, pref, h.now())
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
		q = q.Where("level IN ?", pref.Levels)
	}
	if len(pref.Statuses) > 0 {
		q = q.Where("status IN ?", pref.Statuses)
	}
	if len(pref.AlertTypes) > 0 {
		q = q.Where("alert_type IN ?", pref.AlertTypes)
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
```

- [ ] **Step 5: Run the handler tests**

Run:

```powershell
go test ./backend/internal/alert/... -run 'TestDashboard|TestSaveDashboard' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all alert tests**

Run:

```powershell
go test ./backend/internal/alert/... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/alert/handler.go backend/internal/alert/dashboard_handler.go backend/internal/alert/dashboard_handler_test.go
git commit -m "feat: add alert dashboard APIs"
```

---

### Task 4: Route Registration

**Files:**
- Modify: `backend/internal/app/routes.go`
- Test: `backend/internal/app/routes_revision_test.go`

**Interfaces:**
- Consumes: `alert.Handler.DashboardPreference`, `SaveDashboardPreference`, `Dashboard`, and `DashboardOptions`.
- Produces: protected routes under `/api/v1/alerts`.

- [ ] **Step 1: Write the failing route test**

Append this test to `backend/internal/app/routes_revision_test.go`:

```go
func TestRegisterRoutesIncludesAlertDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	registerRoutes(
		nil, v1, nil, "test-secret", time.Minute, &middleware.AuthZ{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	want := map[string]bool{
		http.MethodGet + " /api/v1/alerts/dashboard":            false,
		http.MethodGet + " /api/v1/alerts/dashboard-preference": false,
		http.MethodPut + " /api/v1/alerts/dashboard-preference": false,
		http.MethodGet + " /api/v1/alerts/dashboard-options":    false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("%s is not registered", route)
		}
	}
}
```

- [ ] **Step 2: Run the route test to verify it fails**

Run:

```powershell
go test ./backend/internal/app/... -run TestRegisterRoutesIncludesAlertDashboard -count=1
```

Expected: FAIL because the dashboard routes are not registered.

- [ ] **Step 3: Register static routes before `/:id`**

Modify the `al := v1.Group("/alerts")` block in `backend/internal/app/routes.go`:

```go
al := v1.Group("/alerts")
{
	al.GET("", az.RequirePerm("alert:view"), alertH.List)
	al.GET("/stats", az.RequirePerm("alert:view"), alertH.Stats)
	al.GET("/export", az.RequirePerm("alert:export"), alertH.Export)
	al.GET("/dashboard", az.RequirePerm("alert:view"), alertH.Dashboard)
	al.GET("/dashboard-preference", az.RequirePerm("alert:view"), alertH.DashboardPreference)
	al.PUT("/dashboard-preference", az.RequirePerm("alert:view"), alertH.SaveDashboardPreference)
	al.GET("/dashboard-options", az.RequirePerm("alert:view"), alertH.DashboardOptions)
	al.GET("/:id", az.RequirePerm("alert:view"), alertH.Get)
	al.POST("/:id/claim", az.RequirePerm("alert:handle"), alertH.Claim)
	al.POST("/:id/note", az.RequirePerm("alert:handle"), alertH.Note)
	al.POST("/:id/close", az.RequirePerm("alert:handle"), alertH.Close)
}
```

- [ ] **Step 4: Run route and app tests**

Run:

```powershell
go test ./backend/internal/app/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/app/routes.go backend/internal/app/routes_revision_test.go
git commit -m "feat: route alert dashboard APIs"
```

---

### Task 5: Frontend Custom Dashboard

**Files:**
- Create: `frontend/src/components/AlertDashboard.vue`
- Create: `frontend/src/utils/alert.ts`
- Modify: `frontend/src/views/Alerts.vue`
- Modify: `testing/test_ui_menu_merge.py`

**Interfaces:**
- Consumes:
  - `GET /api/v1/alerts/dashboard-preference`
  - `PUT /api/v1/alerts/dashboard-preference`
  - `GET /api/v1/alerts/dashboard?page=&pageSize=`
  - `GET /api/v1/alerts/dashboard-options`
- Produces:
  - `frontend/src/utils/alert.ts` exports `levelTag`, `statusTag`, `statusLabel`, and `typeLabel`.
  - `AlertDashboard.vue` exposes `reload(): Promise<void>`.
  - Alerts page includes tab name `dashboard` and defaults to it.

- [ ] **Step 1: Update the browser test before implementation**

In `testing/test_ui_menu_merge.py`, extend `_mock_api_data` before the final return:

```python
    if method == "GET" and path == "/api/v1/alerts/dashboard-preference":
        return {
            "levels": ["p1", "p2", "p3", "p4"],
            "statuses": ["active", "processing", "recovered", "closed"],
            "alertTypes": ["threshold", "missing", "collect_fail", "anomaly", "ds_error"],
            "systemCodes": [],
            "dateMode": "today",
            "recentDays": 7,
            "sortRules": [{"field": "triggerAt", "direction": "desc"}],
        }
    if method == "PUT" and path == "/api/v1/alerts/dashboard-preference":
        return {
            "levels": ["p1", "p2", "p3", "p4"],
            "statuses": ["active", "processing", "recovered", "closed"],
            "alertTypes": ["threshold", "missing", "collect_fail", "anomaly", "ds_error"],
            "systemCodes": [],
            "dateMode": "today",
            "recentDays": 7,
            "sortRules": [{"field": "triggerAt", "direction": "desc"}],
        }
    if method == "GET" and path == "/api/v1/alerts/dashboard-options":
        return {"systems": [{"systemCode": "SYS_A", "name": "系统 A"}]}
    if method == "GET" and path == "/api/v1/alerts/dashboard":
        return {"items": [], "total": 0, "page": 1, "pageSize": 20}
    if method == "GET" and path == "/api/v1/alerts":
        return {"items": [], "total": 0, "page": 1, "pageSize": 20}
```

Replace `test_alert_center_contains_policy_tab` with:

```python
def test_alert_center_contains_custom_dashboard_tab(browser, app_url):
    page = _open_page(browser, app_url, "/alerts")
    tabs = page.locator(".module-tabs .el-tabs__item")

    assert [text.strip() for text in tabs.all_inner_texts()] == [
        "自定义看板",
        "告警列表",
        "告警统计",
        "告警策略",
    ]
    expect(tabs.filter(has_text="自定义看板")).to_have_class(re.compile(r"\bis-active\b"))
    expect(page).to_have_url(re.compile(r"/alerts$"))

    page.locator('[data-test="dashboard-save"]').click()
    expect(page.get_by_text("配置已保存")).to_be_visible()

    tabs.filter(has_text="告警策略").click()
    expect(page).to_have_url(re.compile(r"/alerts\?tab=policies$"))
    expect(tabs.filter(has_text="告警策略")).to_have_class(re.compile(r"\bis-active\b"))
    expect(page.locator(".page-card .page-card")).to_have_count(0)
```

- [ ] **Step 2: Run the browser test to verify it fails**

Run:

```powershell
pytest testing/test_ui_menu_merge.py::test_alert_center_contains_custom_dashboard_tab -q
```

Expected: FAIL because the tab and save control do not exist.

- [ ] **Step 3: Extract shared alert display helpers**

Create `frontend/src/utils/alert.ts`:

```ts
export function statusTag(s: string): string {
  return ({ active: 'danger', processing: 'primary', recovered: 'success', closed: 'info' } as Record<string, string>)[s] || 'info'
}

export function statusLabel(s: string): string {
  return ({ active: '活动', processing: '处理中', recovered: '已恢复', closed: '已关闭' } as Record<string, string>)[s] || s
}

export function typeLabel(t: string): string {
  return ({ threshold: '阈值', missing: '缺数', collect_fail: '采集失败', anomaly: '异常值', ds_error: '数据源异常' } as Record<string, string>)[t] || t
}

export function levelTag(level: string): string {
  return ({ p1: 'danger', p2: 'warning', p3: 'warning', p4: 'info' } as Record<string, string>)[level] || 'info'
}
```

- [ ] **Step 4: Create `AlertDashboard.vue`**

Create `frontend/src/components/AlertDashboard.vue`:

```vue
<template>
  <div class="dashboard-panel">
    <div class="dashboard-filters">
      <el-select v-model="pref.levels" multiple collapse-tags clearable placeholder="级别" style="width: 180px">
        <el-option v-for="o in levelOptions" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-select v-model="pref.statuses" multiple collapse-tags clearable placeholder="状态" style="width: 210px">
        <el-option v-for="o in statusOptions" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-select v-model="pref.alertTypes" multiple collapse-tags clearable placeholder="类型" style="width: 230px">
        <el-option v-for="o in typeOptions" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-select v-model="pref.systemCodes" multiple collapse-tags clearable placeholder="系统" style="width: 220px">
        <el-option v-for="s in systems" :key="s.systemCode" :label="`${s.name} (${s.systemCode})`" :value="s.systemCode" />
      </el-select>
      <el-radio-group v-model="pref.dateMode">
        <el-radio-button value="today">当日</el-radio-button>
        <el-radio-button value="recent_days">近 N 日</el-radio-button>
      </el-radio-group>
      <el-input-number v-if="pref.dateMode === 'recent_days'" v-model="pref.recentDays" :min="1" :max="365" data-test="dashboard-recent-days" />
      <el-button type="primary" :loading="saving" data-test="dashboard-save" @click="save">保存配置</el-button>
      <el-button @click="restoreDefaults">恢复默认</el-button>
      <el-button :icon="Refresh" circle title="刷新" @click="reload" />
    </div>

    <div class="sort-builder">
      <div class="sort-title">排序优先级</div>
      <div v-for="(rule, index) in pref.sortRules" :key="`${rule.field}-${index}`" class="sort-row">
        <span class="sort-index">{{ index + 1 }}</span>
        <el-select v-model="rule.field" style="width: 160px">
          <el-option
            v-for="o in sortFieldOptions"
            :key="o.value"
            :label="o.label"
            :value="o.value"
            :disabled="pref.sortRules.some((r, i) => i !== index && r.field === o.value)"
          />
        </el-select>
        <el-select v-model="rule.direction" style="width: 100px">
          <el-option label="升序" value="asc" />
          <el-option label="降序" value="desc" />
        </el-select>
        <el-button :icon="ArrowUp" circle title="上移" :disabled="index === 0" @click="moveRule(index, -1)" />
        <el-button :icon="ArrowDown" circle title="下移" :disabled="index === pref.sortRules.length - 1" @click="moveRule(index, 1)" />
        <el-button :icon="Delete" circle title="删除" @click="removeRule(index)" />
      </div>
      <el-button :icon="Plus" :disabled="pref.sortRules.length >= 5" @click="addRule">添加排序</el-button>
    </div>

    <el-table :data="items" v-loading="loading" border stripe highlight-current-row @row-click="emit('open', $event)">
      <el-table-column prop="alertNo" label="告警编号" width="170" />
      <el-table-column prop="level" label="级别" width="70">
        <template #default="{ row }"><el-tag :type="levelTag(row.level)">{{ row.level.toUpperCase() }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="alertType" label="类型" width="100">
        <template #default="{ row }">{{ typeLabel(row.alertType) }}</template>
      </el-table-column>
      <el-table-column prop="systemCode" label="系统" width="140" show-overflow-tooltip />
      <el-table-column prop="metricCode" label="指标" width="160" show-overflow-tooltip />
      <el-table-column prop="status" label="状态" width="90">
        <template #default="{ row }"><el-tag :type="statusTag(row.status)">{{ statusLabel(row.status) }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="triggerAt" label="触发时间" width="170">
        <template #default="{ row }">{{ fmtTs(row.triggerAt) }}</template>
      </el-table-column>
      <el-table-column prop="lastValue" label="最新值" width="90" align="right" />
      <el-table-column prop="threshold" label="阈值" width="80" align="right" />
      <el-table-column label="水位" width="80" align="right">
        <template #default="{ row }">{{ row.waterLevel != null ? (row.waterLevel * 100).toFixed(1) + '%' : '-' }}</template>
      </el-table-column>
      <el-table-column prop="durationMin" label="持续(min)" width="90" align="right" />
      <el-table-column prop="claimedBy" label="认领人" width="100">
        <template #default="{ row }">{{ row.claimedBy || '-' }}</template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无告警" />
    <el-pagination v-model:current-page="page" :page-size="pageSize" :total="total"
      layout="total, prev, pager, next" class="pager" @current-change="loadData" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ArrowDown, ArrowUp, Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'
import { levelTag, statusLabel, statusTag, typeLabel } from '@/utils/alert'

interface DashboardSortRule { field: string; direction: 'asc' | 'desc' }
interface DashboardPreference {
  levels: string[]
  statuses: string[]
  alertTypes: string[]
  systemCodes: string[]
  dateMode: 'today' | 'recent_days'
  recentDays: number
  sortRules: DashboardSortRule[]
}
interface AlertItem {
  id: number; alertNo: string; level: string; alertType: string
  systemCode: string; metricCode: string; status: string; triggerAt: string
  lastValue: number | null; threshold: number | null; waterLevel: number | null
  durationMin: number | null; claimedBy: string
}
interface SystemOption { systemCode: string; name: string }

const emit = defineEmits<{ open: [row: AlertItem] }>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const items = ref<AlertItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const systems = ref<SystemOption[]>([])
const pref = reactive<DashboardPreference>({
  levels: [],
  statuses: [],
  alertTypes: [],
  systemCodes: [],
  dateMode: 'today',
  recentDays: 7,
  sortRules: [{ field: 'triggerAt', direction: 'desc' }],
})

const levelOptions = [
  { label: 'P1', value: 'p1' }, { label: 'P2', value: 'p2' },
  { label: 'P3', value: 'p3' }, { label: 'P4', value: 'p4' },
]
const statusOptions = [
  { label: '活动', value: 'active' }, { label: '处理中', value: 'processing' },
  { label: '已恢复', value: 'recovered' }, { label: '已关闭', value: 'closed' },
]
const typeOptions = [
  { label: '阈值', value: 'threshold' }, { label: '缺数', value: 'missing' },
  { label: '采集失败', value: 'collect_fail' }, { label: '异常值', value: 'anomaly' },
  { label: '数据源异常', value: 'ds_error' },
]
const sortFieldOptions = [
  { label: '级别', value: 'level' }, { label: '状态', value: 'status' },
  { label: '类型', value: 'alertType' }, { label: '系统', value: 'systemCode' },
  { label: '触发时间', value: 'triggerAt' },
]

function applyPreference(next: DashboardPreference) {
  pref.levels = [...(next.levels || [])]
  pref.statuses = [...(next.statuses || [])]
  pref.alertTypes = [...(next.alertTypes || [])]
  pref.systemCodes = [...(next.systemCodes || [])]
  pref.dateMode = next.dateMode || 'today'
  pref.recentDays = next.recentDays || 7
  pref.sortRules = (next.sortRules?.length ? next.sortRules : [{ field: 'triggerAt', direction: 'desc' as const }])
    .map(rule => ({ field: rule.field, direction: rule.direction }))
}

function restoreDefaults() {
  applyPreference({
    levels: levelOptions.map(o => o.value),
    statuses: statusOptions.map(o => o.value),
    alertTypes: typeOptions.map(o => o.value),
    systemCodes: [],
    dateMode: 'today',
    recentDays: 7,
    sortRules: [{ field: 'triggerAt', direction: 'desc' }],
  })
}

function addRule() {
  const used = new Set(pref.sortRules.map(r => r.field))
  const next = sortFieldOptions.find(o => !used.has(o.value))
  if (next) pref.sortRules.push({ field: next.value, direction: 'asc' })
}

function removeRule(index: number) {
  pref.sortRules.splice(index, 1)
  if (pref.sortRules.length === 0) pref.sortRules.push({ field: 'triggerAt', direction: 'desc' })
}

function moveRule(index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= pref.sortRules.length) return
  const [rule] = pref.sortRules.splice(index, 1)
  pref.sortRules.splice(target, 0, rule)
}

async function loadData() {
  loading.value = true
  error.value = ''
  try {
    const data = await http.get('/api/v1/alerts/dashboard', {
      params: { page: page.value, pageSize },
    }) as unknown as { items: AlertItem[]; total: number }
    items.value = data.items || []
    total.value = data.total || 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

async function reload() {
  await loadData()
}

async function save() {
  saving.value = true
  try {
    const saved = await http.put('/api/v1/alerts/dashboard-preference', {
      levels: pref.levels,
      statuses: pref.statuses,
      alertTypes: pref.alertTypes,
      systemCodes: pref.systemCodes,
      dateMode: pref.dateMode,
      recentDays: pref.recentDays,
      sortRules: pref.sortRules.map(rule => ({ ...rule })),
    }) as unknown as DashboardPreference
    applyPreference(saved)
    page.value = 1
    ElMessage.success('配置已保存')
    await loadData()
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  const [saved, options] = await Promise.all([
    http.get('/api/v1/alerts/dashboard-preference') as unknown as Promise<DashboardPreference>,
    http.get('/api/v1/alerts/dashboard-options') as unknown as Promise<{ systems: SystemOption[] }>,
  ])
  applyPreference(saved)
  systems.value = options.systems || []
  await loadData()
})

defineExpose({ reload })
</script>

<style scoped>
.dashboard-panel { display: flex; flex-direction: column; gap: 12px; }
.dashboard-filters { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.sort-builder { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.sort-title { font-size: 13px; font-weight: 600; }
.sort-row { display: flex; align-items: center; gap: 6px; }
.sort-index { width: 18px; text-align: center; color: #909399; font-size: 12px; }
.pager { display: flex; justify-content: flex-end; }
</style>
```

- [ ] **Step 5: Wire the component into `Alerts.vue`**

Modify `frontend/src/views/Alerts.vue`:

```ts
import AlertDashboard from '@/components/AlertDashboard.vue'
import { levelTag, statusLabel, statusTag, typeLabel } from '@/utils/alert'

const dashboardRef = ref<InstanceType<typeof AlertDashboard> | null>(null)
const { activeTab: tab, onTabChange: updateTab } = useQueryTab(
  ['dashboard', 'list', 'stats', 'policies'] as const,
  'dashboard',
)
```

Add the first tab pane before the existing list pane:

```vue
<el-tab-pane label="自定义看板" name="dashboard">
  <AlertDashboard ref="dashboardRef" @open="openDetail" />
</el-tab-pane>
```

Update refresh behavior:

```ts
function onTabReload() {
  if (tab.value === 'dashboard') {
    dashboardRef.value?.reload()
  } else if (tab.value === 'list') {
    reload()
    loadStats()
  } else {
    loadStats()
  }
}
```

Remove the local `statusTag`, `statusLabel`, `typeLabel`, and `levelTag`
functions from `Alerts.vue`; the existing `eventLabel` and `timelineType`
functions remain local because the detail drawer owns them.

Keep `onMounted(loadStats)` because the top statistic cards are shared by all tabs.

- [ ] **Step 6: Run frontend type checking**

Run:

```powershell
npm run lint
```

Workdir: `frontend`

Expected: PASS.

- [ ] **Step 7: Run the browser test**

Run:

```powershell
pytest testing/test_ui_menu_merge.py::test_alert_center_contains_custom_dashboard_tab -q
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/AlertDashboard.vue frontend/src/utils/alert.ts frontend/src/views/Alerts.vue testing/test_ui_menu_merge.py
git commit -m "feat: add custom alert dashboard UI"
```

---

### Task 6: Full Verification and Regression Check

**Files:**
- Verify only; modify files only if a verification failure identifies a real defect.

**Interfaces:**
- Consumes all outputs from Tasks 1-5.
- Produces verified backend, frontend, and browser behavior.

- [ ] **Step 1: Run backend tests**

Run:

```powershell
go test ./backend/internal/alert/... ./backend/internal/app/... ./backend/internal/persistence/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend type checking and production build**

Run:

```powershell
npm run lint
npm run build
```

Workdir: `frontend`

Expected: both commands exit 0.

- [ ] **Step 3: Run the focused browser regression suite**

Run:

```powershell
pytest testing/test_ui_menu_merge.py -q
```

Expected: PASS.

- [ ] **Step 4: Verify login redirect remains unchanged**

Run:

```powershell
rg -n "redirect: '/dashboard'|name: 'dashboard'" frontend/src/router/index.ts frontend/src/views/Login.vue
```

Expected: Login and root redirect still resolve to `dashboard`; no alert dashboard route is introduced.

- [ ] **Step 5: Verify endpoint registration and permissions**

Run:

```powershell
rg -n "alerts/dashboard|RequirePerm\(\"alert:view\"\)" backend/internal/app/routes.go
```

Expected: all four dashboard routes are protected by `alert:view`.

- [ ] **Step 6: Commit any verification fixes**

If verification required code changes:

```bash
git add backend/internal/alert backend/internal/app backend/internal/persistence frontend/src/components/AlertDashboard.vue frontend/src/views/Alerts.vue testing/test_ui_menu_merge.py
git commit -m "fix: address alert dashboard verification findings"
```

If no changes were required, do not create an empty commit.
