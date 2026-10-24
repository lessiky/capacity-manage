package alert

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConfigCipher interface {
	Encrypt(plain string) (string, error)
	Decrypt(enc string) (string, error)
}

type NotificationManager struct {
	db       *gorm.DB
	cipher   ConfigCipher
	params   *system.Params
	log      *slog.Logger
	registry map[string]Notifier
	mu       sync.RWMutex
	now      func() time.Time
}

func NewNotificationManager(db *gorm.DB, cipher ConfigCipher, params *system.Params, log *slog.Logger) *NotificationManager {
	if log == nil {
		log = slog.Default()
	}
	return &NotificationManager{
		db: db, cipher: cipher, params: params, log: log,
		registry: map[string]Notifier{
			"smtp": NewSMTPNotifier(), "wecom": NewWeComNotifier(),
			"dingtalk": NewDingTalkNotifier(), "feishu": NewFeishuNotifier(),
			"webhook": NewWebhookNotifier(),
		},
		now: time.Now,
	}
}

func (m *NotificationManager) SetNotifier(kind string, n Notifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registry[kind] = n
}

func (m *NotificationManager) notifier(kind string) Notifier {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry[kind]
}

// Dispatch sends one alert event through every matching enabled subscription.
// It records suppressed, sent and retrying attempts in alert_notification_log.
func (m *NotificationManager) Dispatch(ctx context.Context, event string, rec *persistence.AlertRecord) error {
	if m == nil || m.db == nil || rec == nil {
		return nil
	}
	var subs []persistence.NotificationSubscription
	if err := m.db.Where("enabled = ?", true).Order("id").Find(&subs).Error; err != nil {
		return err
	}
	for i := range subs {
		sub := &subs[i]
		if !subscriptionMatches(sub, rec) {
			continue
		}
		channelIDs := parseInt64JSON(sub.ChannelIDs)
		if len(channelIDs) == 0 {
			continue
		}
		var channels []persistence.NotificationChannel
		if err := m.db.Where("id IN ? AND enabled = ?", channelIDs, true).Find(&channels).Error; err != nil {
			return err
		}
		recipients := parseStringJSON(sub.Recipients)
		silentReason := m.silenceReason(rec, sub, m.now())
		for j := range channels {
			ch := &channels[j]
			if err := m.dispatchChannel(ctx, event, rec, sub, ch, recipients, silentReason); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendGeneric sends a non-alert message (currently report subscriptions)
// through existing channels while preserving queue retry and deduplication.
func (m *NotificationManager) SendGeneric(ctx context.Context, channelIDs []int64, recipients []string,
	subject, body, idempotencyKey string) error {
	if m == nil || m.db == nil || len(channelIDs) == 0 {
		return nil
	}
	var channels []persistence.NotificationChannel
	if err := m.db.Where("id IN ? AND enabled = ?", channelIDs, true).Find(&channels).Error; err != nil {
		return err
	}
	now := m.now()
	hash := sha256.Sum256([]byte(idempotencyKey))
	alertNo := "RPT" + hex.EncodeToString(hash[:8])
	for i := range channels {
		ch := &channels[i]
		cfg, err := m.decodeChannelConfig(ch.Config)
		if err != nil {
			return err
		}
		target := notificationTarget(ch.Type, recipients, cfg)
		dedupeKey := fmt.Sprintf("report:%s:channel:%d", idempotencyKey, ch.ID)
		msg := Notification{
			Event: "report", ChannelType: ch.Type, Target: target,
			Secret: configString(cfg, "secret"), IdempotencyKey: dedupeKey,
			Subject: subject, Body: body, Level: "p3", AlertNo: alertNo, Config: cfg,
		}
		payload, _ := json.Marshal(msg)
		row := persistence.AlertNotificationLog{
			AlertID: 0, AlertNo: alertNo, ChannelID: ch.ID, ChannelType: ch.Type,
			Target: target, Level: "p3", Event: "report",
			DedupeKey: dedupeKey, Status: "queued", MaxAttempts: 4,
			Payload: string(payload), CreatedAt: now, UpdatedAt: now,
		}
		res := m.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			continue
		}
		if notifier := m.notifier(ch.Type); notifier != nil {
			if err := notifier.Send(ctx, msg); err != nil {
				if err := m.recordFailure(ctx, &row, err); err != nil {
					return err
				}
				continue
			}
		}
		if err := m.db.Model(&row).Updates(map[string]any{
			"status": "sent", "attempts": 1, "sent_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (m *NotificationManager) dispatchChannel(ctx context.Context, event string, rec *persistence.AlertRecord,
	sub *persistence.NotificationSubscription, ch *persistence.NotificationChannel,
	recipients []string, silentReason string) error {
	now := m.now()
	dedupeKey := fmt.Sprintf("alert:%s:event:%s:level:%s:channel:%d", rec.AlertNo, event, rec.Level, ch.ID)
	if m.withinDedupeWindow(rec.AlertNo, event, rec.Level, ch.ID, sub.DedupWindowMin, now) {
		return nil
	}
	tpl, vars, subject, body, err := m.renderFor(rec, event, ch.Type)
	if err != nil {
		return err
	}
	cfg, err := m.decodeChannelConfig(ch.Config)
	if err != nil {
		return err
	}
	target := notificationTarget(ch.Type, recipients, cfg)
	msg := Notification{
		Event: event, ChannelType: ch.Type, Target: target,
		Secret: configString(cfg, "secret"), IdempotencyKey: dedupeKey,
		Subject: subject, Body: body, Level: rec.Level, AlertNo: rec.AlertNo, Config: cfg,
	}
	payload, _ := json.Marshal(msg)
	row := persistence.AlertNotificationLog{
		AlertID: rec.ID, AlertNo: rec.AlertNo, ChannelID: ch.ID, ChannelType: ch.Type,
		Target: target, Level: rec.Level, Event: event,
		TemplateVer: tpl.Version, DedupeKey: dedupeKey, Status: "queued",
		MaxAttempts: 4, Payload: string(payload), CreatedAt: now, UpdatedAt: now,
	}
	if tpl.ID > 0 {
		row.TemplateID = &tpl.ID
	}
	res := m.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	if silentReason != "" {
		return m.db.Model(&row).Updates(map[string]any{
			"status": "suppressed", "error": "静默抑制: " + silentReason, "updated_at": now,
		}).Error
	}
	if n := m.notifier(ch.Type); n != nil {
		if err := n.Send(ctx, msg); err != nil {
			return m.recordFailure(ctx, &row, err)
		}
	}
	_ = vars
	return m.db.Model(&row).Updates(map[string]any{
		"status": "sent", "attempts": 1, "sent_at": now, "updated_at": now,
	}).Error
}

func (m *NotificationManager) renderFor(rec *persistence.AlertRecord, event, channelType string) (
	persistence.NotificationTemplate, map[string]string, string, string, error) {
	templateEvent := event
	if templateEvent == "trigger" {
		templateEvent = "alert"
	}
	var tpl persistence.NotificationTemplate
	q := m.db.Where("enabled = ? AND event = ? AND channel_type IN ?", true, templateEvent, []string{channelType, "*"}).
		Order(clause.Expr{SQL: "CASE WHEN channel_type = ? THEN 0 ELSE 1 END", Vars: []any{channelType}})
	if err := q.First(&tpl).Error; err != nil && err != gorm.ErrRecordNotFound {
		return tpl, nil, "", "", err
	}
	if tpl.ID == 0 {
		tpl = defaultNotificationTemplate(templateEvent, channelType)
	}
	vars := notificationVars(rec, event)
	subject, err := renderNotificationTemplate(tpl.Subject, vars)
	if err != nil {
		return tpl, vars, "", "", err
	}
	body, err := renderNotificationTemplate(tpl.Body, vars)
	if err != nil {
		return tpl, vars, "", "", err
	}
	return tpl, vars, subject, body, nil
}

func (m *NotificationManager) withinDedupeWindow(alertNo, event, level string, channelID int64, minutes int, now time.Time) bool {
	if minutes <= 0 {
		return false
	}
	var n int64
	_ = m.db.Model(&persistence.AlertNotificationLog{}).
		Where("alert_no = ? AND event = ? AND level = ? AND channel_id = ?", alertNo, event, level, channelID).
		Where("status IN ('sent','queued','retrying') AND created_at >= ?", now.Add(-time.Duration(minutes)*time.Minute)).
		Count(&n).Error
	return n > 0
}

func (m *NotificationManager) silenceReason(rec *persistence.AlertRecord, sub *persistence.NotificationSubscription, now time.Time) string {
	if sub != nil && inSilentClockWindow(now, sub.SilentStart, sub.SilentEnd) {
		return "订阅静默时段"
	}
	var windows []persistence.NotificationSilence
	_ = m.db.Where("start_at <= ? AND end_at >= ?", now, now).
		Where("(scope_type = 'global') OR (scope_type = 'system' AND scope_value = ?) OR (scope_type = 'metric' AND scope_value IN ?)",
			rec.SystemCode, []string{rec.MetricCode, rec.SystemCode + "." + rec.MetricCode}).
		Order("id DESC").Find(&windows).Error
	if len(windows) > 0 {
		if windows[0].Reason != "" {
			return windows[0].Reason
		}
		return "维护窗口"
	}
	var sys persistence.BusinessSystem
	if rec.SystemCode != "" && m.db.Where("system_code = ?", rec.SystemCode).First(&sys).Error == nil &&
		sys.Status == "maintenance" && sys.SilentMaintain {
		return "业务系统维护静默"
	}
	return ""
}

func inSilentClockWindow(now time.Time, startS, endS string) bool {
	if startS == "" || endS == "" {
		return false
	}
	start, err1 := time.Parse("15:04", startS)
	if err1 != nil {
		return false
	}
	end, err2 := time.Parse("15:04", endS)
	if err2 != nil {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	startMin := start.Hour()*60 + start.Minute()
	endMin := end.Hour()*60 + end.Minute()
	if startMin == endMin {
		return true
	}
	if startMin < endMin {
		return cur >= startMin && cur < endMin
	}
	return cur >= startMin || cur < endMin
}

func (m *NotificationManager) recordFailure(ctx context.Context, row *persistence.AlertNotificationLog, sendErr error) error {
	now := m.now()
	attempts := row.Attempts + 1
	updates := map[string]any{"attempts": attempts, "error": sendErr.Error(), "updated_at": now}
	if attempts < row.MaxAttempts {
		next := now.Add(notificationRetryDelay(attempts))
		updates["status"] = "retrying"
		updates["next_retry_at"] = next
	} else {
		updates["status"] = "failed"
		updates["next_retry_at"] = nil
	}
	if err := m.db.Model(row).Updates(updates).Error; err != nil {
		return err
	}
	if attempts >= row.MaxAttempts {
		_ = m.createChannelFailureAlert(ctx, row.ChannelID, row.ChannelType, sendErr)
	}
	return nil
}

func notificationRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// ProcessRetry executes one due retry log entry. The caller may invoke it from
// a scheduler tick or manually for verification.
func (m *NotificationManager) ProcessRetry(ctx context.Context, id int64) error {
	var row persistence.AlertNotificationLog
	if err := m.db.First(&row, id).Error; err != nil {
		return err
	}
	if row.Status != "retrying" {
		return nil
	}
	var ch persistence.NotificationChannel
	if err := m.db.First(&ch, row.ChannelID).Error; err != nil {
		return err
	}
	cfg, err := m.decodeChannelConfig(ch.Config)
	if err != nil {
		return err
	}
	var msg Notification
	if err := json.Unmarshal([]byte(row.Payload), &msg); err != nil {
		return err
	}
	msg.Config = cfg
	if n := m.notifier(ch.Type); n != nil {
		if err := n.Send(ctx, msg); err != nil {
			return m.recordFailure(ctx, &row, err)
		}
	}
	now := m.now()
	return m.db.Model(&row).Updates(map[string]any{
		"status": "sent", "attempts": row.Attempts + 1, "sent_at": now,
		"next_retry_at": nil, "error": "", "updated_at": now,
	}).Error
}

func (m *NotificationManager) RetryDue(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 50
	}
	var ids []int64
	if err := m.db.Model(&persistence.AlertNotificationLog{}).
		Where("status = 'retrying' AND next_retry_at IS NOT NULL AND next_retry_at <= ?", m.now()).
		Order("next_retry_at, id").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.ProcessRetry(ctx, id); err != nil {
			m.log.Warn("notification retry failed", slog.Int64("id", id), slog.Any("err", err))
		}
	}
	return nil
}

func (m *NotificationManager) createChannelFailureAlert(_ context.Context, channelID int64, channelType string, sendErr error) error {
	key := fmt.Sprintf("notify:%d", channelID)
	var n int64
	if err := m.db.Model(&persistence.AlertRecord{}).
		Where("metric_code = ? AND alert_type = 'notify_fail' AND status IN ('active','processing')", key).
		Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := m.now()
	detail, _ := json.Marshal(map[string]any{"channelId": channelID, "channelType": channelType, "error": sendErr.Error()})
	detailText := string(detail)
	rec := persistence.AlertRecord{
		AlertNo:    "AL" + now.Format("20060102150405") + "-" + uuid.NewString()[:6],
		MetricCode: key, Level: "p4", AlertType: "notify_fail", Status: "active",
		TriggerAt: now, LastValueAt: &now, DurationMin: intPtr(0), Detail: &detailText,
	}
	if m.params != nil {
		t := SlaTargets(m.params)["p4"]
		deadline := now.Add(time.Duration(t.claim) * time.Minute)
		rec.DeadlineAt = &deadline
	}
	return m.db.Create(&rec).Error
}

func (m *NotificationManager) decodeChannelConfig(raw string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	if m.cipher != nil && strings.HasPrefix(raw, "enc.v1.") {
		plain, err := m.cipher.Decrypt(raw)
		if err != nil {
			return nil, err
		}
		raw = plain
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("通知渠道配置非法 JSON: %w", err)
	}
	return out, nil
}

func subscriptionMatches(sub *persistence.NotificationSubscription, rec *persistence.AlertRecord) bool {
	if sub == nil || rec == nil {
		return false
	}
	if !csvContains(sub.Levels, rec.Level) {
		return false
	}
	if strings.TrimSpace(sub.AlertTypes) != "*" && !csvContains(sub.AlertTypes, rec.AlertType) {
		return false
	}
	switch sub.ScopeType {
	case "system":
		return sub.ScopeValue == rec.SystemCode
	case "metric":
		return sub.ScopeValue == rec.MetricCode || sub.ScopeValue == rec.SystemCode+"."+rec.MetricCode
	default:
		return true
	}
}

func csvContains(csv, value string) bool {
	for _, item := range strings.Split(csv, ",") {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}

func notificationTarget(channelType string, recipients []string, cfg map[string]any) string {
	switch channelType {
	case "smtp":
		return strings.Join(recipients, ",")
	default:
		return configString(cfg, "url")
	}
}

func parseStringJSON(raw string) []string {
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

func parseInt64JSON(raw string) []int64 {
	var out []int64
	if json.Unmarshal([]byte(raw), &out) == nil {
		return out
	}
	var nums []float64
	if json.Unmarshal([]byte(raw), &nums) == nil {
		for _, n := range nums {
			out = append(out, int64(n))
		}
	}
	return out
}

func notificationVars(rec *persistence.AlertRecord, event string) map[string]string {
	value, threshold, water, duration := "", "", "", ""
	if rec.FirstValue != nil {
		value = fmt.Sprintf("%.6g", *rec.FirstValue)
	}
	if rec.LastValue != nil {
		value = fmt.Sprintf("%.6g", *rec.LastValue)
	}
	if rec.Threshold != nil {
		threshold = fmt.Sprintf("%.6g", *rec.Threshold)
	}
	if rec.WaterLevel != nil {
		water = fmt.Sprintf("%.1f%%", *rec.WaterLevel*100)
	}
	if rec.DurationMin != nil {
		duration = fmt.Sprint(*rec.DurationMin)
	}
	recovered := ""
	if rec.RecoveredAt != nil {
		recovered = rec.RecoveredAt.Format("2006-01-02 15:04:05")
	}
	evidence := ""
	if rec.EvidenceWindow != nil {
		evidence = *rec.EvidenceWindow
	}
	return map[string]string{
		"event": event, "alertNo": rec.AlertNo, "system": rec.SystemCode,
		"systemCode": rec.SystemCode, "metric": rec.MetricCode, "metricCode": rec.MetricCode,
		"level": strings.ToUpper(rec.Level), "value": value, "threshold": threshold,
		"waterLevel": water, "duration": duration, "durationMin": duration,
		"triggerAt":   rec.TriggerAt.Format("2006-01-02 15:04:05"),
		"recoveredAt": recovered, "evidenceWindow": evidence,
		"link": "/alerts?alertNo=" + rec.AlertNo,
	}
}

func defaultNotificationTemplate(event, channelType string) persistence.NotificationTemplate {
	format := "markdown"
	if channelType == "smtp" {
		format = "text"
	}
	switch event {
	case "escalate":
		return persistence.NotificationTemplate{
			Event: event, ChannelType: channelType, Format: format, Version: 0,
			Subject: "容量告警升级 ${level} ${metricCode}",
			Body:    "${systemCode} / ${metricCode}\n级别已升级为 ${level}\n证据窗口: ${evidenceWindow}\n时间: ${triggerAt}",
		}
	case "recovered":
		return persistence.NotificationTemplate{
			Event: event, ChannelType: channelType, Format: format, Version: 0,
			Subject: "容量告警恢复 ${metricCode}",
			Body:    "${systemCode} / ${metricCode}\n级别: 已恢复\n时间: ${recoveredAt}",
		}
	default:
		return persistence.NotificationTemplate{
			Event: "alert", ChannelType: channelType, Format: format, Version: 0,
			Subject: "容量告警 ${level} ${metricCode}",
			Body:    "${systemCode} / ${metricCode}\n级别: ${level}\n当前值: ${value}\n阈值: ${threshold}\n时间: ${triggerAt}",
		}
	}
}

func notificationModels() []any {
	return []any{
		&persistence.NotificationChannel{},
		&persistence.NotificationTemplate{},
		&persistence.NotificationSubscription{},
		&persistence.NotificationSilence{},
		&persistence.AlertNotificationLog{},
		&persistence.AlertRecord{},
		&persistence.BusinessSystem{},
	}
}
