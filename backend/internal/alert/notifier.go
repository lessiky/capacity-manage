package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Notification is the channel-neutral send request.
type Notification struct {
	Event          string         `json:"event"`
	ChannelType    string         `json:"channelType"`
	Target         string         `json:"target"`
	Secret         string         `json:"secret,omitempty"`
	IdempotencyKey string         `json:"idempotencyKey"`
	Subject        string         `json:"subject"`
	Body           string         `json:"body"`
	Level          string         `json:"level"`
	AlertNo        string         `json:"alertNo,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
}

type Notifier interface {
	Send(ctx context.Context, n Notification) error
}

var notificationHTTPClient = &http.Client{Timeout: 15 * time.Second}

func renderNotificationTemplate(body string, vars map[string]string) (string, error) {
	if strings.Contains(body, "{{") {
		return "", fmt.Errorf("通知模板仅支持 ${变量} 语法")
	}
	return os.Expand(body, func(key string) string {
		if v, ok := vars[key]; ok {
			return v
		}
		return "${" + key + "}"
	}), nil
}

// SignWebhook implements the generic receiver contract:
// hex(HMAC-SHA256(secret, timestamp + "." + body)).
func SignWebhook(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

type WebhookNotifier struct{}

func NewWebhookNotifier() *WebhookNotifier { return &WebhookNotifier{} }

func (n *WebhookNotifier) Send(ctx context.Context, msg Notification) error {
	payload, _ := json.Marshal(map[string]any{
		"event": msg.Event, "level": msg.Level, "alertNo": msg.AlertNo,
		"subject": msg.Subject, "body": msg.Body, "idempotencyKey": msg.IdempotencyKey,
	})
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	headers := map[string]string{
		"X-Cap-Event":           msg.Event,
		"X-Cap-Timestamp":       ts,
		"X-Cap-Idempotency-Key": msg.IdempotencyKey,
	}
	if msg.Secret != "" {
		headers["X-Cap-Signature"] = SignWebhook(msg.Secret, ts, payload)
	}
	for k, v := range configHeaders(msg.Config) {
		headers[k] = v
	}
	return postJSON(ctx, msg.Target, headers, payload)
}

type WeComNotifier struct{}

func NewWeComNotifier() *WeComNotifier { return &WeComNotifier{} }

func (n *WeComNotifier) Send(ctx context.Context, msg Notification) error {
	payload, _ := json.Marshal(map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": "### " + msg.Subject + "\n> " + msg.Body,
		},
	})
	return postJSON(ctx, msg.Target, nil, payload)
}

type DingTalkNotifier struct{}

func NewDingTalkNotifier() *DingTalkNotifier { return &DingTalkNotifier{} }

func (n *DingTalkNotifier) Send(ctx context.Context, msg Notification) error {
	target := msg.Target
	if msg.Secret != "" {
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		sign := signDingTalk(msg.Secret, ts)
		target = appendQuery(target, "timestamp", ts)
		target = appendQuery(target, "sign", sign)
	}
	payload, _ := json.Marshal(map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": msg.Subject,
			"text":  "### " + msg.Subject + "\n" + msg.Body,
		},
	})
	return postJSON(ctx, target, nil, payload)
}

func signDingTalk(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type FeishuNotifier struct{}

func NewFeishuNotifier() *FeishuNotifier { return &FeishuNotifier{} }

func (n *FeishuNotifier) Send(ctx context.Context, msg Notification) error {
	payload, _ := json.Marshal(map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]any{"wide_screen_mode": true},
			"header": map[string]any{
				"template": levelCardColor(msg.Level),
				"title":    map[string]any{"tag": "plain_text", "content": msg.Subject},
			},
			"elements": []any{
				map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": msg.Body}},
			},
		},
	})
	return postJSON(ctx, msg.Target, nil, payload)
}

func levelCardColor(level string) string {
	switch strings.ToLower(level) {
	case "p1":
		return "red"
	case "p2":
		return "orange"
	case "p3":
		return "blue"
	default:
		return "grey"
	}
}

type SMTPNotifier struct {
	SendMail func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

func NewSMTPNotifier() *SMTPNotifier {
	return &SMTPNotifier{SendMail: smtp.SendMail}
}

func (n *SMTPNotifier) Send(_ context.Context, msg Notification) error {
	host := configString(msg.Config, "host")
	port := configInt(msg.Config, "port", 25)
	from := configString(msg.Config, "from")
	if from == "" {
		from = configString(msg.Config, "username")
	}
	to := splitRecipients(msg.Target)
	if host == "" || from == "" || len(to) == 0 {
		return fmt.Errorf("SMTP 配置不完整: host/from/target 必填")
	}
	raw, err := BuildSMTPMessage(from, to, msg.Subject, msg.Body)
	if err != nil {
		return err
	}
	addr := host + ":" + strconv.Itoa(port)
	var auth smtp.Auth
	username := configString(msg.Config, "username")
	password := configString(msg.Config, "password")
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}
	send := n.SendMail
	if send == nil {
		send = smtp.SendMail
	}
	return send(addr, auth, from, to, raw)
}

func BuildSMTPMessage(from string, to []string, subject, body string) ([]byte, error) {
	if from == "" || len(to) == 0 {
		return nil, fmt.Errorf("SMTP from/to 必填")
	}
	subject = mime.QEncoding.Encode("UTF-8", subject)
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String()), nil
}

func postJSON(ctx context.Context, target string, headers map[string]string, payload []byte) error {
	if target == "" {
		return fmt.Errorf("通知目标未配置")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := notificationHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("通知 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return responseBusinessError(body)
}

func responseBusinessError(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		return nil
	}
	for _, key := range []string{"errcode", "code", "StatusCode", "status_code"} {
		if v, ok := data[key]; ok && fmt.Sprint(v) != "0" && fmt.Sprint(v) != "200" {
			return fmt.Errorf("通知服务返回失败: %v", data)
		}
	}
	return nil
}

func appendQuery(raw, key, value string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	if v, ok := config[key].(string); ok {
		return v
	}
	return ""
}

func configInt(config map[string]any, key string, def int) int {
	if config == nil {
		return def
	}
	switch v := config[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func configHeaders(config map[string]any) map[string]string {
	out := map[string]string{}
	if config == nil {
		return out
	}
	raw, ok := config["headers"].(map[string]any)
	if !ok {
		return out
	}
	for k, v := range raw {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func splitRecipients(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var _ = http.StatusOK
