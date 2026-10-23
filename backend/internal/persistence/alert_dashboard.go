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
