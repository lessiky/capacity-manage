package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DailyHit is the normalized daily threshold result used by the escalation
// engine. Missing and anomalous days never count as hits.
type DailyHit struct {
	BizDate   time.Time
	WarnHit   bool
	DangerHit bool
	DataState string // valid/missing/anomaly
}

// EscalationPolicy is the deterministic rule input. Service persistence adds
// scope and priority; the pure evaluator only needs the matching policies.
type EscalationPolicy struct {
	ID              int64
	SourceLine      string // warn/danger
	TargetLevel     string // p2/p1
	Mode            string // consecutive/window_count
	ConsecutiveDays int
	WindowDays      int
	HitDays         int
	Priority        int
}

type EscalationDecision struct {
	Level    string
	Matched  bool
	PolicyID int64
	Evidence map[string]any
	Reason   string
}

type normalizedHit struct {
	bizDate   time.Time
	warnHit   bool
	dangerHit bool
	state     string
}

// EvaluateEscalation evaluates consecutive and window-count policies.
// The latest date represented by hits anchors the window. A consecutive rule
// requires an explicit valid hit row for every date in the window, so a
// missing/anomaly row breaks the streak; the same row is simply skipped by a
// window-count rule.
func EvaluateEscalation(policies []EscalationPolicy, hits []DailyHit) EscalationDecision {
	normalized := normalizeHits(hits)
	if len(normalized) == 0 {
		return EscalationDecision{}
	}
	latest := normalized[len(normalized)-1].bizDate

	type match struct {
		decision EscalationDecision
		priority int
	}
	var matches []match
	for _, p := range policies {
		matched, evidence := evaluatePolicy(p, normalized, latest)
		if !matched {
			continue
		}
		level := p.TargetLevel
		reason := fmt.Sprintf("命中%s累积升级规则: %s", sourceLineLabel(p.SourceLine), modeLabel(p))
		matches = append(matches, match{
			decision: EscalationDecision{
				Level: level, Matched: true, PolicyID: p.ID,
				Evidence: evidence, Reason: reason,
			},
			priority: p.Priority,
		})
	}
	if len(matches) == 0 {
		return EscalationDecision{}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		ri, rj := severityRank(matches[i].decision.Level), severityRank(matches[j].decision.Level)
		if ri != rj {
			return ri < rj
		}
		return matches[i].priority > matches[j].priority
	})
	return matches[0].decision
}

func evaluatePolicy(p EscalationPolicy, hits []normalizedHit, latest time.Time) (bool, map[string]any) {
	if p.TargetLevel != "p1" && p.TargetLevel != "p2" {
		return false, nil
	}
	if p.SourceLine != "warn" && p.SourceLine != "danger" {
		return false, nil
	}
	switch p.Mode {
	case "consecutive":
		if p.ConsecutiveDays <= 0 {
			return false, nil
		}
		start := latest.AddDate(0, 0, -(p.ConsecutiveDays - 1))
		var hitDates []string
		for i := 0; i < p.ConsecutiveDays; i++ {
			day := start.AddDate(0, 0, i)
			h, ok := findHit(hits, day)
			if !ok || h.state != "valid" || !isSourceHit(h, p.SourceLine) {
				return false, evidence(p, start, latest, hitDates)
			}
			hitDates = append(hitDates, day.Format("2006-01-02"))
		}
		return true, evidence(p, start, latest, hitDates)
	case "window_count":
		if p.WindowDays <= 0 || p.HitDays <= 0 || p.HitDays > p.WindowDays {
			return false, nil
		}
		start := latest.AddDate(0, 0, -(p.WindowDays - 1))
		var hitDates []string
		for i := 0; i < p.WindowDays; i++ {
			day := start.AddDate(0, 0, i)
			h, ok := findHit(hits, day)
			if ok && h.state == "valid" && isSourceHit(h, p.SourceLine) {
				hitDates = append(hitDates, day.Format("2006-01-02"))
			}
		}
		return len(hitDates) >= p.HitDays, evidence(p, start, latest, hitDates)
	default:
		return false, nil
	}
}

func normalizeHits(hits []DailyHit) []normalizedHit {
	byDate := map[string]normalizedHit{}
	for _, h := range hits {
		if h.BizDate.IsZero() {
			continue
		}
		date := clock.DayStart(h.BizDate)
		key := date.Format("2006-01-02")
		state := h.DataState
		if state == "" {
			state = "valid"
		}
		cur, ok := byDate[key]
		if !ok {
			byDate[key] = normalizedHit{
				bizDate: date, warnHit: h.WarnHit, dangerHit: h.DangerHit, state: state,
			}
			continue
		}
		cur.warnHit = cur.warnHit || h.WarnHit
		cur.dangerHit = cur.dangerHit || h.DangerHit
		if stateRank(state) < stateRank(cur.state) {
			cur.state = state
		}
		byDate[key] = cur
	}
	out := make([]normalizedHit, 0, len(byDate))
	for _, h := range byDate {
		if h.dangerHit {
			h.warnHit = true // danger line also satisfies warn line
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].bizDate.Before(out[j].bizDate) })
	return out
}

func findHit(hits []normalizedHit, day time.Time) (normalizedHit, bool) {
	key := clock.DayStart(day).Format("2006-01-02")
	for i := range hits {
		if hits[i].bizDate.Format("2006-01-02") == key {
			return hits[i], true
		}
	}
	return normalizedHit{}, false
}

func isSourceHit(h normalizedHit, source string) bool {
	if source == "danger" {
		return h.dangerHit
	}
	return h.warnHit || h.dangerHit
}

func evidence(p EscalationPolicy, start, end time.Time, hitDates []string) map[string]any {
	out := map[string]any{
		"sourceLine":  p.SourceLine,
		"targetLevel": p.TargetLevel,
		"mode":        p.Mode,
		"windowStart": start.Format("2006-01-02"),
		"windowEnd":   end.Format("2006-01-02"),
		"hitDates":    hitDates,
		"hitDays":     len(hitDates),
	}
	if p.Mode == "consecutive" {
		out["windowDays"] = p.ConsecutiveDays
		out["requiredDays"] = p.ConsecutiveDays
	} else {
		out["windowDays"] = p.WindowDays
		out["requiredDays"] = p.HitDays
	}
	return out
}

func modeLabel(p EscalationPolicy) string {
	if p.Mode == "consecutive" {
		return fmt.Sprintf("连续 %d 天", p.ConsecutiveDays)
	}
	return fmt.Sprintf("最近 %d 天内 %d 天", p.WindowDays, p.HitDays)
}

func sourceLineLabel(line string) string {
	if line == "danger" {
		return "危险线"
	}
	return "预警线"
}

func severityRank(level string) int {
	switch level {
	case "p1":
		return 1
	case "p2":
		return 2
	case "p3":
		return 3
	case "p4":
		return 4
	default:
		return 99
	}
}

func stateRank(state string) int {
	switch state {
	case "valid":
		return 0
	case "missing":
		return 1
	default:
		return 2
	}
}

// UpsertDailyHits rebuilds one day's hit row from the authoritative daily
// summary. A missing row is stored with data_state=missing so consecutive
// escalation rules are interrupted.
func UpsertDailyHits(tx *gorm.DB, metric *persistence.CapacityMetric, bizDate time.Time) error {
	if tx == nil || metric == nil {
		return nil
	}
	day := clock.DayStart(bizDate)
	var row persistence.MetricValueDay
	err := tx.Where("metric_code = ? AND biz_date = ?", metric.MetricCode, day).First(&row).Error
	hit := persistence.AlertDailyHit{
		MetricCode: metric.MetricCode, BizDate: day,
		SystemCode: metric.SystemCode, DataState: "missing",
		Source: "collect", ComputedAt: time.Now(),
	}
	switch {
	case err == gorm.ErrRecordNotFound:
	case err != nil:
		return err
	case row.Level == "missing" || row.StatValue == nil && row.PointCount == 0:
		hit.DataState = "missing"
		hit.Source = row.Source
	case row.StatValue == nil:
		hit.DataState = "anomaly"
		hit.Source = row.Source
	default:
		hit.DataState = "valid"
		hit.Source = row.Source
		hit.WarnHit, hit.DangerHit = thresholdHits(metric, *row.StatValue)
	}
	if hit.Source == "" {
		hit.Source = "collect"
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "metric_code"}, {Name: "biz_date"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"system_code", "warn_hit", "danger_hit", "data_state", "source", "computed_at",
		}),
	}).Create(&hit).Error
}

func thresholdHits(metric *persistence.CapacityMetric, value float64) (warn, danger bool) {
	lowerWorse := metric.JudgeDirection == "lower_worse"
	if metric.DangerLine != nil {
		if lowerWorse {
			danger = value <= *metric.DangerLine
		} else {
			danger = value >= *metric.DangerLine
		}
	}
	if metric.WarnLine != nil {
		if lowerWorse {
			warn = value <= *metric.WarnLine
		} else {
			warn = value >= *metric.WarnLine
		}
	}
	return warn || danger, danger
}

func (s *Service) recordRealtimeHit(metric *persistence.CapacityMetric, line string, ts time.Time) {
	if s == nil || s.db == nil || metric == nil || line == "" {
		return
	}
	day := clock.DayStart(ts)
	var current persistence.AlertDailyHit
	err := s.db.Where("metric_code = ? AND biz_date = ?", metric.MetricCode, day).First(&current).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		s.logger().Warn("load realtime daily hit failed", "err", err)
		return
	}
	current.MetricCode = metric.MetricCode
	current.BizDate = day
	current.SystemCode = metric.SystemCode
	current.DataState = "valid"
	if current.Source == "" {
		current.Source = "collect"
	}
	if line == "danger" {
		current.DangerHit = true
		current.WarnHit = true
	} else {
		current.WarnHit = true
	}
	current.ComputedAt = time.Now()
	if err := s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "metric_code"}, {Name: "biz_date"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"system_code", "warn_hit", "danger_hit", "data_state", "source", "computed_at",
		}),
	}).Create(&current).Error; err != nil {
		s.logger().Warn("save realtime daily hit failed", "err", err)
	}
}

// EvaluateAndEscalate rebuilds hits in the largest policy window and applies
// the matching policy to the single active threshold alert. It never creates a
// duplicate alert and never auto-downgrades.
func (s *Service) EvaluateAndEscalate(ctx context.Context, metric *persistence.CapacityMetric, bizDate time.Time) error {
	if s == nil || s.db == nil || metric == nil {
		return nil
	}
	var active persistence.AlertRecord
	if err := s.db.Where("metric_code = ? AND status IN ('active','processing') AND alert_type='threshold'",
		metric.MetricCode).Order("trigger_at DESC").First(&active).Error; err != nil {
		return nil
	}
	policies, err := s.loadEscalationPolicies(metric)
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	replayDays := maxPolicyWindow(policies)
	if s.params != nil {
		if n := s.params.Int("recover_days", 2); n > replayDays {
			replayDays = n
		}
	}
	end := clock.DayStart(bizDate)
	for i := replayDays - 1; i >= 0; i-- {
		if err := UpsertDailyHits(s.db, metric, end.AddDate(0, 0, -i)); err != nil {
			return err
		}
	}
	hits, err := s.loadDailyHits(metric.MetricCode, bizDate, replayDays)
	if err != nil {
		return err
	}
	decision := EvaluateEscalation(policies, hits)
	now := time.Now()
	recoverDays := 2
	if s.params != nil {
		recoverDays = s.params.Int("recover_days", 2)
	}
	if recoverDays > 0 && recoverStreak(hits, recoverDays) {
		recoveredAt := now
		if err := s.db.Model(&active).Updates(map[string]any{
			"status": "recovered", "recovered_at": recoveredAt,
		}).Error; err != nil {
			return err
		}
		s.notifyAsync("recovered", &active)
		return nil
	}
	if !decision.Matched {
		return s.recordDowngradeSuggestion(&active, "", "当前证据窗口不再满足升级规则", nil, nil)
	}
	if severityRank(decision.Level) >= severityRank(active.Level) {
		if severityRank(decision.Level) > severityRank(active.Level) {
			return s.recordDowngradeSuggestion(&active, decision.Level,
				"数据修正后，当前级别高于规则评估结果", decision.Evidence, &decision.PolicyID)
		}
		return nil
	}
	oldLevel := active.Level
	evidence, _ := json.Marshal(decision.Evidence)
	evidenceText := string(evidence)
	updates := map[string]any{
		"level":                decision.Level,
		"source_line":          sourceLineFromPolicies(decision, policies),
		"escalation_policy_id": decision.PolicyID,
		"escalated_at":         now,
		"escalation_count":     active.EscalationCount + 1,
		"evidence_window":      evidenceText,
		"deadline_at":          s.deadlineFor(decision.Level, now),
	}
	if err := s.db.Model(&active).Updates(updates).Error; err != nil {
		return err
	}
	history := persistence.AlertLevelHistory{
		AlertID: active.ID, FromLevel: oldLevel, ToLevel: decision.Level,
		Action: "escalate", Reason: decision.Reason, Evidence: evidenceText, PolicyID: &decision.PolicyID,
		Operator: "system", ChangedAt: now,
	}
	if err := s.db.Create(&history).Error; err != nil {
		return err
	}
	active.Level = decision.Level
	active.EscalationPolicyID = &decision.PolicyID
	active.EscalatedAt = &now
	active.EscalationCount++
	active.EvidenceWindow = &evidenceText
	active.DeadlineAt = updates["deadline_at"].(*time.Time)
	s.logger().Info("alert escalated",
		"metric", metric.MetricCode, "alertNo", active.AlertNo,
		"from", oldLevel, "to", decision.Level)
	s.notifyAsync("escalate", &active)
	return nil
}

// recordDowngradeSuggestion preserves the recommendation without silently
// lowering the alert. Repeated replays with the same evidence are de-duplicated.
func (s *Service) recordDowngradeSuggestion(active *persistence.AlertRecord, targetLevel, reason string,
	evidence map[string]any, policyID *int64) error {
	if active == nil {
		return nil
	}
	if targetLevel == "" {
		targetLevel = nextLowerLevel(active.Level)
	}
	if targetLevel == "" || targetLevel == active.Level {
		return nil
	}
	payload := map[string]any{
		"reason": reason, "currentLevel": active.Level, "targetLevel": targetLevel,
		"evaluatedAt": time.Now().Format(time.RFC3339),
	}
	if len(evidence) > 0 {
		payload["window"] = evidence
	}
	raw, _ := json.Marshal(payload)
	evidenceText := string(raw)
	var n int64
	q := s.db.Model(&persistence.AlertLevelHistory{}).
		Where("alert_id = ? AND action = ? AND from_level = ? AND to_level = ?",
			active.ID, "downgrade_suggestion", active.Level, targetLevel)
	if err := q.Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hist := persistence.AlertLevelHistory{
		AlertID: active.ID, FromLevel: active.Level, ToLevel: targetLevel,
		Action: "downgrade_suggestion", Reason: reason,
		Evidence: evidenceText, PolicyID: policyID,
		Operator: "system", ChangedAt: time.Now(),
	}
	if err := s.db.Create(&hist).Error; err != nil {
		return err
	}
	s.logger().Info("alert downgrade suggested",
		"alertNo", active.AlertNo, "from", active.Level, "to", targetLevel)
	return nil
}

func nextLowerLevel(level string) string {
	switch level {
	case "p1":
		return "p2"
	case "p2":
		return "p3"
	default:
		return ""
	}
}

func (s *Service) loadEscalationPolicies(metric *persistence.CapacityMetric) ([]EscalationPolicy, error) {
	var rows []persistence.AlertEscalationPolicy
	// Scope is a multi-value JSON array, so matching happens in Go rather than as a
	// column equality (policyMatchesScope); the enabled policy set is small and
	// loading it whole keeps matching testable without SQL string surgery.
	q := s.db.Where("enabled = ?", true).Order("priority DESC, id")
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if err := s.ensureDefaultEscalationPolicies(); err != nil {
			return nil, err
		}
		if err := q.Find(&rows).Error; err != nil {
			return nil, err
		}
	}
	out := make([]EscalationPolicy, 0, len(rows))
	for _, r := range rows {
		if !policyMatchesScope(r, metric) {
			continue
		}
		out = append(out, EscalationPolicy{
			ID: r.ID, SourceLine: r.SourceLine, TargetLevel: r.TargetLevel,
			Mode: r.Mode, ConsecutiveDays: r.ConsecutiveDays,
			WindowDays: r.WindowDays, HitDays: r.HitDays, Priority: r.Priority,
		})
	}
	return out, nil
}

func (s *Service) ensureDefaultEscalationPolicies() error {
	var n int64
	if err := s.db.Model(&persistence.AlertEscalationPolicy{}).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	rows := []persistence.AlertEscalationPolicy{
		{Name: "默认-预警连续3天升级P2", SourceLine: "warn", TargetLevel: "p2", Mode: "consecutive", ConsecutiveDays: 3, DayBasis: "natural", ScopeType: "global", Enabled: true, Priority: 100, CreatedBy: "system"},
		{Name: "默认-预警7天3次升级P2", SourceLine: "warn", TargetLevel: "p2", Mode: "window_count", WindowDays: 7, HitDays: 3, DayBasis: "natural", ScopeType: "global", Enabled: true, Priority: 90, CreatedBy: "system"},
		{Name: "默认-危险连续2天升级P1", SourceLine: "danger", TargetLevel: "p1", Mode: "consecutive", ConsecutiveDays: 2, DayBasis: "natural", ScopeType: "global", Enabled: true, Priority: 100, CreatedBy: "system"},
		{Name: "默认-危险5天2次升级P1", SourceLine: "danger", TargetLevel: "p1", Mode: "window_count", WindowDays: 5, HitDays: 2, DayBasis: "natural", ScopeType: "global", Enabled: true, Priority: 90, CreatedBy: "system"},
	}
	return s.db.Create(&rows).Error
}

func (s *Service) loadDailyHits(metricCode string, end time.Time, days int) ([]DailyHit, error) {
	if days < 1 {
		days = 1
	}
	end = clock.DayStart(end)
	start := end.AddDate(0, 0, -(days - 1))
	var rows []persistence.AlertDailyHit
	if err := s.db.Where("metric_code = ? AND biz_date >= ? AND biz_date <= ?",
		metricCode, start, end).Order("biz_date").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]DailyHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, DailyHit{
			BizDate: r.BizDate, WarnHit: r.WarnHit, DangerHit: r.DangerHit, DataState: r.DataState,
		})
	}
	return out, nil
}

func maxPolicyWindow(policies []EscalationPolicy) int {
	max := 0
	for _, p := range policies {
		n := p.WindowDays
		if p.Mode == "consecutive" {
			n = p.ConsecutiveDays
		}
		if n > max {
			max = n
		}
	}
	return max
}

func recoverStreak(hits []DailyHit, days int) bool {
	if days <= 0 || len(hits) == 0 {
		return false
	}
	normalized := normalizeHits(hits)
	if len(normalized) < days {
		return false
	}
	latest := normalized[len(normalized)-1].bizDate
	for i := 0; i < days; i++ {
		day := latest.AddDate(0, 0, -(days - 1 - i))
		h, ok := findHit(normalized, day)
		if !ok || h.state != "valid" || h.warnHit || h.dangerHit {
			return false
		}
	}
	return true
}

func sourceLineFromPolicies(decision EscalationDecision, policies []EscalationPolicy) string {
	for _, p := range policies {
		if p.ID == decision.PolicyID {
			return p.SourceLine
		}
	}
	return ""
}
