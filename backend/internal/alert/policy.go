package alert

import (
	"context"
	"fmt"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"gorm.io/gorm"
)

// PolicyInput is the editable escalation-rule contract used by the API and
// normalization tests. Scope is multi-valued: system codes for system scope,
// metric codes for metric scope (resolved to "SYSTEM.METRIC" before saving).
// ScopeValue and ScopeValues are two accepted wire forms of the same field;
// ScopeValues exists so a caller can be explicit, and both are merged.
type PolicyInput struct {
	Name            string                 `json:"name" binding:"required,min=2,max=128"`
	SourceLine      string                 `json:"sourceLine" binding:"required,oneof=warn danger"`
	TargetLevel     string                 `json:"targetLevel" binding:"required,oneof=p1 p2"`
	Mode            string                 `json:"mode" binding:"required,oneof=consecutive window_count"`
	ConsecutiveDays int                    `json:"consecutiveDays"`
	WindowDays      int                    `json:"windowDays"`
	HitDays         int                    `json:"hitDays"`
	DayBasis        string                 `json:"dayBasis"`
	ScopeType       string                 `json:"scopeType" binding:"required,oneof=global system metric"`
	ScopeValue      persistence.StringList `json:"scopeValue"`
	ScopeValues     persistence.StringList `json:"scopeValues"`
	Enabled         *bool                  `json:"enabled"`
	Priority        int                    `json:"priority"`
}

// mergeScopeValues unions the two accepted wire forms into one ordered list.
func mergeScopeValues(in PolicyInput) persistence.StringList {
	out := make(persistence.StringList, 0, len(in.ScopeValue)+len(in.ScopeValues))
	out = append(out, in.ScopeValue...)
	out = append(out, in.ScopeValues...)
	return normalizeScopeValues(out)
}

// normalizeScopeValues trims, drops blanks and de-duplicates while keeping order.
func normalizeScopeValues(in persistence.StringList) persistence.StringList {
	out := make(persistence.StringList, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func normalizeEscalationPolicyInput(in PolicyInput) (persistence.AlertEscalationPolicy, error) {
	in.Name = strings.TrimSpace(in.Name)
	values := mergeScopeValues(in)
	if in.Name == "" {
		return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeBadRequest, "策略名称不能为空")
	}
	if in.SourceLine == "warn" && in.TargetLevel != "p2" {
		return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "预警线规则只能升级为 P2")
	}
	if in.SourceLine == "danger" && in.TargetLevel != "p1" {
		return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "危险线规则只能升级为 P1")
	}
	switch in.Mode {
	case "consecutive":
		if in.ConsecutiveDays < 1 || in.ConsecutiveDays > 90 {
			return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "连续天数 N 须在 1~90")
		}
		in.WindowDays, in.HitDays = 0, 0
	case "window_count":
		if in.WindowDays < 1 || in.WindowDays > 90 {
			return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "窗口天数 N 须在 1~90")
		}
		if in.HitDays < 1 || in.HitDays > in.WindowDays {
			return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "命中次数 M 须满足 1<=M<=N")
		}
		in.ConsecutiveDays = 0
	default:
		return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeBadRequest, "未知累计模式")
	}
	if in.ScopeType == "global" {
		values = persistence.StringList{}
	} else if len(values) == 0 {
		return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "%s 作用域必须至少选择一个目标", in.ScopeType)
	}
	if in.DayBasis == "" {
		in.DayBasis = "natural"
	}
	if in.DayBasis != "natural" {
		return persistence.AlertEscalationPolicy{}, resp.NewErr(resp.CodeUnprocessable, "日口径仅支持 natural")
	}
	if in.Priority <= 0 {
		in.Priority = 100
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return persistence.AlertEscalationPolicy{
		Name: in.Name, SourceLine: in.SourceLine, TargetLevel: in.TargetLevel,
		Mode: in.Mode, ConsecutiveDays: in.ConsecutiveDays,
		WindowDays: in.WindowDays, HitDays: in.HitDays, DayBasis: in.DayBasis,
		ScopeType: in.ScopeType, ScopeValue: values,
		Enabled: enabled, Priority: in.Priority,
	}, nil
}

// validatePolicyScope rejects policies outside a non-admin manager's visible
// system set. Every selected target must be visible: a partially visible
// multi-value scope is rejected as a whole so the saved rule never reaches
// systems the manager cannot see. Metric scope uses "SYSTEM.METRIC"; the HTTP
// layer resolves a bare metric code to that form before validation.
func validatePolicyScope(scopeType string, scopeValues []string, visibleSystems []string, platformAdmin bool) error {
	if scopeType == "global" || platformAdmin {
		return nil
	}
	if len(visibleSystems) == 0 {
		return resp.NewErr(resp.CodeDataForbidden, "无该策略作用域的数据权限")
	}
	visible := make(map[string]bool, len(visibleSystems))
	for _, s := range visibleSystems {
		visible[s] = true
	}
	switch scopeType {
	case "system":
		for _, v := range scopeValues {
			if !visible[v] {
				return resp.NewErr(resp.CodeDataForbidden, "无业务系统数据权限: %s", v)
			}
		}
	case "metric":
		for _, v := range scopeValues {
			i := strings.Index(v, ".")
			if i <= 0 || !visible[v[:i]] {
				return resp.NewErr(resp.CodeDataForbidden, "无指标归属系统数据权限: %s", v)
			}
		}
	default:
		return resp.NewErr(resp.CodeBadRequest, "未知作用域")
	}
	return nil
}

func policyToInput(p persistence.AlertEscalationPolicy) PolicyInput {
	enabled := p.Enabled
	return PolicyInput{
		Name: p.Name, SourceLine: p.SourceLine, TargetLevel: p.TargetLevel,
		Mode: p.Mode, ConsecutiveDays: p.ConsecutiveDays,
		WindowDays: p.WindowDays, HitDays: p.HitDays, DayBasis: p.DayBasis,
		ScopeType: p.ScopeType, ScopeValue: p.ScopeValue,
		Enabled: &enabled, Priority: p.Priority,
	}
}

func policyDescription(p persistence.AlertEscalationPolicy) string {
	if p.Mode == "consecutive" {
		return fmt.Sprintf("连续 %d 天", p.ConsecutiveDays)
	}
	return fmt.Sprintf("最近 %d 天中 %d 天", p.WindowDays, p.HitDays)
}

func policyMatchesScope(p persistence.AlertEscalationPolicy, metric *persistence.CapacityMetric) bool {
	if p.ScopeType == "global" {
		return true
	}
	if p.ScopeType == "system" {
		for _, v := range p.ScopeValue {
			if v == metric.SystemCode {
				return true
			}
		}
		return false
	}
	for _, v := range p.ScopeValue {
		if v == metric.MetricCode || v == metric.SystemCode+"."+metric.MetricCode {
			return true
		}
	}
	return false
}

func policyEnabledAt(p persistence.AlertEscalationPolicy, at time.Time) bool {
	return p.Enabled && !p.CreatedAt.After(at)
}

func createEscalationPolicy(db *gorm.DB, in PolicyInput, username string) (*persistence.AlertEscalationPolicy, error) {
	row, err := normalizeEscalationPolicyInput(in)
	if err != nil {
		return nil, err
	}
	row.CreatedBy = username
	if err := db.Create(&row).Error; err != nil {
		return nil, resp.NewErr(resp.CodeConflict, "策略名称已存在或创建失败: %v", err)
	}
	return &row, nil
}

func updateEscalationPolicy(db *gorm.DB, id int64, in PolicyInput) (*persistence.AlertEscalationPolicy, error) {
	var old persistence.AlertEscalationPolicy
	if err := db.First(&old, id).Error; err != nil {
		return nil, resp.NewErr(resp.CodeNotFound, "升级策略不存在")
	}
	row, err := normalizeEscalationPolicyInput(in)
	if err != nil {
		return nil, err
	}
	row.ID = old.ID
	row.CreatedBy = old.CreatedBy
	row.CreatedAt = old.CreatedAt
	if err := db.Model(&old).Updates(map[string]any{
		"name": row.Name, "source_line": row.SourceLine, "target_level": row.TargetLevel,
		"mode": row.Mode, "consecutive_days": row.ConsecutiveDays,
		"window_days": row.WindowDays, "hit_days": row.HitDays,
		"day_basis": row.DayBasis, "scope_type": row.ScopeType, "scope_value": row.ScopeValue,
		"enabled": row.Enabled, "priority": row.Priority,
	}).Error; err != nil {
		return nil, resp.NewErr(resp.CodeConflict, "策略更新失败: %v", err)
	}
	if err := db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func deleteEscalationPolicy(db *gorm.DB, id int64) error {
	res := db.Delete(&persistence.AlertEscalationPolicy{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return resp.NewErr(resp.CodeNotFound, "升级策略不存在")
	}
	return nil
}

type SimulationDay struct {
	BizDate   string `json:"bizDate"`
	WarnHit   bool   `json:"warnHit"`
	DangerHit bool   `json:"dangerHit"`
	DataState string `json:"dataState"`
}

type SimulationResult struct {
	Matched     bool            `json:"matched"`
	Level       string          `json:"level,omitempty"`
	PolicyID    int64           `json:"policyId,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Evidence    map[string]any  `json:"evidence,omitempty"`
	EvaluatedOn string          `json:"evaluatedOn"`
	Days        []SimulationDay `json:"days"`
}

// SimulatePolicy replays a draft rule against authoritative daily values. It
// does not persist hits or mutate alerts.
func (s *Service) SimulatePolicy(ctx context.Context, metric *persistence.CapacityMetric,
	in PolicyInput, from, to time.Time) (*SimulationResult, error) {
	if s == nil || s.db == nil || metric == nil {
		return nil, resp.NewErr(resp.CodeBadRequest, "指标不能为空")
	}
	row, err := normalizeEscalationPolicyInput(in)
	if err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, resp.NewErr(resp.CodeBadRequest, "to 不得早于 from")
	}
	if to.Sub(from) > 366*24*time.Hour {
		return nil, resp.NewErr(resp.CodeBadRequest, "模拟区间须 ≤ 366 天")
	}
	start, end := dayStart(from), dayStart(to)
	var rows []persistence.MetricValueDay
	if err := s.db.WithContext(ctx).
		Where("metric_code = ? AND biz_date >= ? AND biz_date <= ?", metric.MetricCode, start, end).
		Order("biz_date").Find(&rows).Error; err != nil {
		return nil, err
	}
	byDate := make(map[string]persistence.MetricValueDay, len(rows))
	for _, r := range rows {
		byDate[dayStart(r.BizDate).Format("2006-01-02")] = r
	}
	hits := make([]DailyHit, 0)
	days := make([]SimulationDay, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		day := SimulationDay{BizDate: key, DataState: "missing"}
		if r, ok := byDate[key]; ok {
			switch {
			case r.Level == "missing" || r.StatValue == nil && r.PointCount == 0:
				day.DataState = "missing"
			case r.StatValue == nil:
				day.DataState = "anomaly"
			default:
				day.DataState = "valid"
				day.WarnHit, day.DangerHit = thresholdHits(metric, *r.StatValue)
			}
		}
		days = append(days, day)
		hits = append(hits, DailyHit{
			BizDate: d, WarnHit: day.WarnHit, DangerHit: day.DangerHit, DataState: day.DataState,
		})
	}
	decision := EvaluateEscalation([]EscalationPolicy{{
		ID: row.ID, SourceLine: row.SourceLine, TargetLevel: row.TargetLevel,
		Mode: row.Mode, ConsecutiveDays: row.ConsecutiveDays,
		WindowDays: row.WindowDays, HitDays: row.HitDays, Priority: row.Priority,
	}}, hits)
	return &SimulationResult{
		Matched: decision.Matched, Level: decision.Level, PolicyID: decision.PolicyID,
		Reason: decision.Reason, Evidence: decision.Evidence,
		EvaluatedOn: end.Format("2006-01-02"), Days: days,
	}, nil
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
