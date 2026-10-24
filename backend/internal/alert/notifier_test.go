package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"
)

type fakeNotifier struct {
	calls []Notification
	err   error
}

func (f *fakeNotifier) Send(_ context.Context, n Notification) error {
	f.calls = append(f.calls, n)
	return f.err
}

func TestRenderNotificationTemplate(t *testing.T) {
	vars := map[string]string{
		"system": "SYS_A", "metric": "CPU", "value": "91.5",
		"threshold": "90", "level": "P1", "duration": "120",
	}
	got, err := renderNotificationTemplate(
		"${system}/${metric} value=${value} threshold=${threshold} level=${level} duration=${duration}", vars)
	if err != nil {
		t.Fatal(err)
	}
	want := "SYS_A/CPU value=91.5 threshold=90 level=P1 duration=120"
	if got != want {
		t.Fatalf("rendered = %q, want %q", got, want)
	}
	if _, err := renderNotificationTemplate("{{.missing", vars); err == nil {
		t.Fatal("invalid template must fail")
	}
}

func TestRenderNotificationTemplateDoesNotExecuteInjectedVariables(t *testing.T) {
	vars := map[string]string{"system": `{{printf "%s" "PWNED"}}`}
	got, err := renderNotificationTemplate("系统=${system}", vars)
	if err != nil {
		t.Fatal(err)
	}
	want := `系统={{printf "%s" "PWNED"}}`
	if got != want {
		t.Fatalf("rendered = %q, want injected data to remain literal %q", got, want)
	}
}

func TestWebhookHMACSignature(t *testing.T) {
	body := []byte(`{"alertNo":"AL1"}`)
	ts := "1700000000"
	got := SignWebhook("secret", ts, body)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(ts + "."))
	_, _ = mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}

func TestWebhookNotifierSendsSignedIdempotentRequest(t *testing.T) {
	var captured *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	n := NewWebhookNotifier()
	err := n.Send(context.Background(), Notification{
		ChannelType: "webhook", Target: srv.URL, Secret: "topsecret",
		IdempotencyKey: "alert:AL1:escalate:p1", Subject: "容量告警",
		Body: "CPU 超限", Level: "p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured.Header.Get("X-Cap-Signature") == "" ||
		captured.Header.Get("X-Cap-Idempotency-Key") != "alert:AL1:escalate:p1" {
		t.Fatalf("headers = %#v", captured.Header)
	}
	if !strings.Contains(string(body), "CPU 超限") {
		t.Fatalf("body = %s", body)
	}
}

func TestIMNotifiersBuildChannelPayloads(t *testing.T) {
	cases := []struct {
		name     string
		notifier Notifier
		bodyKey  string
	}{
		{"wecom", NewWeComNotifier(), "markdown"},
		{"dingtalk", NewDingTalkNotifier(), "markdown"},
		{"feishu", NewFeishuNotifier(), "card"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			err := tc.notifier.Send(context.Background(), Notification{
				ChannelType: tc.name, Target: srv.URL,
				Subject: "容量告警", Body: "CPU **91.5**", Level: "p1",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), tc.bodyKey) || !json.Valid(body) {
				t.Fatalf("%s payload = %s", tc.name, body)
			}
		})
	}
}

func TestBuildSMTPMessageContainsHeadersAndBody(t *testing.T) {
	msg, err := BuildSMTPMessage("ops@example.com", []string{"oncall@example.com"}, "容量告警", "CPU 超限")
	if err != nil {
		t.Fatal(err)
	}
	text := string(msg)
	if !strings.Contains(text, "To: oncall@example.com") ||
		!strings.Contains(text, "Subject:") || !strings.Contains(text, "CPU 超限") {
		t.Fatalf("smtp message = %q", text)
	}
}

func TestNotificationDispatchDeduplicatesAndEscalationResends(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(notificationModels()...)
	params := system.NewParams(h.DB)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	ch := persistence.NotificationChannel{
		Name: "ops-webhook", Type: "webhook", Enabled: true,
		Config: `{"url":"http://example.invalid/hook","secret":"s"}`,
	}
	if err := h.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	sub := persistence.NotificationSubscription{
		Name: "ops", ScopeType: "global", Levels: "p1,p2,p3", AlertTypes: "threshold",
		ChannelIDs: "[1]", Recipients: `["oncall"]`, Enabled: true, DedupWindowMin: 60,
	}
	if err := h.DB.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	mgr := NewNotificationManager(h.DB, nil, params, nil)
	mgr.now = func() time.Time { return now }
	fake := &fakeNotifier{}
	mgr.SetNotifier("webhook", fake)
	rec := &persistence.AlertRecord{
		ID: 1, AlertNo: "AL1", SystemCode: "SYS", MetricCode: "CPU",
		Level: "p2", AlertType: "threshold", Status: "active",
		TriggerAt: now, LastValue: floatPtr(91), Threshold: floatPtr(90),
	}
	if err := mgr.Dispatch(context.Background(), "escalate", rec); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Dispatch(context.Background(), "escalate", rec); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1 due same-level dedupe", len(fake.calls))
	}
	rec.Level = "p1"
	if err := mgr.Dispatch(context.Background(), "escalate", rec); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("calls = %d, escalation to a new level must resend", len(fake.calls))
	}
}

func TestNotificationBurst1000UnderFiveMinutes(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(notificationModels()...)
	params := system.NewParams(h.DB)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	_ = h.DB.Create(&persistence.NotificationChannel{
		Name: "burst-webhook", Type: "webhook", Enabled: true,
		Config: `{"url":"http://example.invalid/hook"}`,
	}).Error
	_ = h.DB.Create(&persistence.NotificationSubscription{
		Name: "burst", ScopeType: "global", Levels: "p1,p2,p3", AlertTypes: "threshold",
		ChannelIDs: "[1]", Recipients: `["oncall"]`, Enabled: true, DedupWindowMin: 60,
	}).Error
	mgr := NewNotificationManager(h.DB, nil, params, nil)
	mgr.now = func() time.Time { return now }
	fake := &fakeNotifier{calls: make([]Notification, 0, 1000)}
	mgr.SetNotifier("webhook", fake)

	started := time.Now()
	for i := 0; i < 1000; i++ {
		rec := &persistence.AlertRecord{
			ID: int64(i + 1), AlertNo: "AL-BURST-" + strconv.Itoa(i),
			SystemCode: "SYS", MetricCode: "CPU", Level: "p2",
			AlertType: "threshold", Status: "active", TriggerAt: now,
			LastValue: floatPtr(91), Threshold: floatPtr(90),
		}
		if err := mgr.Dispatch(context.Background(), "trigger", rec); err != nil {
			t.Fatalf("dispatch %d: %v", i, err)
		}
	}
	elapsed := time.Since(started)
	if elapsed > 5*time.Minute {
		t.Fatalf("1000 notification burst took %v, want <=5m", elapsed)
	}
	if len(fake.calls) != 1000 {
		t.Fatalf("notifier calls = %d, want 1000", len(fake.calls))
	}
	var logs int64
	if err := h.DB.Model(&persistence.AlertNotificationLog{}).Count(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if logs != 1000 {
		t.Fatalf("notification logs = %d, want 1000", logs)
	}
}

func TestNotificationDispatchSilentWindowLogsSuppression(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(notificationModels()...)
	params := system.NewParams(h.DB)
	now := time.Date(2026, 10, 20, 23, 0, 0, 0, time.Local)
	_ = h.DB.Create(&persistence.NotificationChannel{Name: "ops", Type: "webhook", Enabled: true, Config: `{"url":"http://example.invalid"}`}).Error
	_ = h.DB.Create(&persistence.NotificationSubscription{
		Name: "night", ScopeType: "global", Levels: "p1,p2,p3", AlertTypes: "threshold",
		ChannelIDs: "[1]", Recipients: `["oncall"]`, Enabled: true,
		SilentStart: "22:00", SilentEnd: "06:00", DedupWindowMin: 60,
	}).Error
	mgr := NewNotificationManager(h.DB, nil, params, nil)
	mgr.now = func() time.Time { return now }
	fake := &fakeNotifier{}
	mgr.SetNotifier("webhook", fake)
	rec := &persistence.AlertRecord{ID: 1, AlertNo: "AL1", SystemCode: "SYS", MetricCode: "CPU", Level: "p2", AlertType: "threshold", Status: "active", TriggerAt: now}
	if err := mgr.Dispatch(context.Background(), "trigger", rec); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("silent window must suppress sends")
	}
	var log persistence.AlertNotificationLog
	if err := h.DB.First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "suppressed" || !strings.Contains(log.Error, "静默") {
		t.Fatalf("log = %+v", log)
	}
}

func TestNotificationRetryBackoffAndFailureAlert(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(notificationModels()...)
	params := system.NewParams(h.DB)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	_ = h.DB.Create(&persistence.NotificationChannel{Name: "ops", Type: "webhook", Enabled: true, Config: `{"url":"http://example.invalid"}`}).Error
	_ = h.DB.Create(&persistence.NotificationSubscription{
		Name: "ops", ScopeType: "global", Levels: "p1,p2,p3", AlertTypes: "threshold",
		ChannelIDs: "[1]", Recipients: `["oncall"]`, Enabled: true, DedupWindowMin: 60,
	}).Error
	mgr := NewNotificationManager(h.DB, nil, params, nil)
	mgr.now = func() time.Time { return now }
	mgr.SetNotifier("webhook", &fakeNotifier{err: context.DeadlineExceeded})
	rec := &persistence.AlertRecord{ID: 1, AlertNo: "AL1", SystemCode: "SYS", MetricCode: "CPU", Level: "p2", AlertType: "threshold", Status: "active", TriggerAt: now}
	if err := mgr.Dispatch(context.Background(), "trigger", rec); err != nil {
		t.Fatal(err)
	}
	var log persistence.AlertNotificationLog
	if err := h.DB.First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "retrying" || log.Attempts != 1 || log.NextRetryAt == nil {
		t.Fatalf("log = %+v", log)
	}
	if got := log.NextRetryAt.Sub(now); got != time.Minute {
		t.Fatalf("first retry delay = %v, want 1m", got)
	}
	// 三次退避耗尽后生成 P4 渠道异常告警。
	fake := mgr.notifier("webhook").(*fakeNotifier)
	for i := 0; i < 3; i++ {
		now = *log.NextRetryAt
		if err := mgr.ProcessRetry(context.Background(), log.ID); err != nil {
			t.Fatal(err)
		}
		if err := h.DB.First(&log, log.ID).Error; err != nil {
			t.Fatal(err)
		}
		_ = fake
	}
	if log.Status != "failed" || log.Attempts != 4 {
		t.Fatalf("final log = %+v", log)
	}
	var alert persistence.AlertRecord
	if err := h.DB.Where("alert_type = 'notify_fail'").First(&alert).Error; err != nil {
		t.Fatal(err)
	}
	if alert.Level != "p4" {
		t.Fatalf("failure alert = %+v", alert)
	}
}
