package masking

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// RedactPassword 展示层脱敏：保留尾 4 位
func RedactPassword(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "******" + s[len(s)-4:]
}

var (
	reConnStr = regexp.MustCompile(`(?i)(jdbc:[a-z0-9]+://|mysql://|postgresql://|postgres://|oracle://|sftp://)[^@\s]+@[^,\s'\"]+`)
	reHostPort = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d{2,5}\b`)
	reCredKV  = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api_?key|密码|口令)\s*[=:：]\s*('[^']*'|"[^"]*"|\S+)`)
	rePhone   = regexp.MustCompile(`\b1[3-9]\d{9}\b`)
	reIDCard  = regexp.MustCompile(`\b\d{17}[\dXx]\b`)
	reBankCard = regexp.MustCompile(`\b\d{15,19}\b`)
)

// SanitizeForAI 送 LLM 前的强制脱敏（接口契约 §2 规则）
func SanitizeForAI(input string) string {
	s := reConnStr.ReplaceAllString(input, "<CONN>")
	s = reHostPort.ReplaceAllString(s, "<HOST>")
	s = reCredKV.ReplaceAllStringFunc(s, func(m string) string {
		g := reCredKV.FindStringSubmatch(m)
		q := ""
		if v := g[2]; len(v) >= 2 && (v[0] == 39 || v[0] == 34) {
			q = string(v[0])
		}
		return g[1] + "=" + q + "******" + q
	})
	s = rePhone.ReplaceAllStringFunc(s, func(m string) string { return m[:3] + "****" + m[7:] })
	s = reIDCard.ReplaceAllStringFunc(s, func(m string) string { return m[:4] + "**********" + m[14:] })
	s = reBankCard.ReplaceAllStringFunc(s, func(m string) string { return m[:4] + "********" + m[len(m)-4:] })
	return s
}

// RedactJSON 对 jsonb 字符串中的凭据字段脱敏（列表/详情展示用）
func RedactJSON(raw string) string {
	if raw == "" {
		return raw
	}
	s := reConnStr.ReplaceAllString(raw, "<CONN>")
	s = reHostPort.ReplaceAllString(s, "<HOST>")
	// "password":"xxx" / "password_enc":"enc.v1..."
	s = regexp.MustCompile(`(?i)("password(_enc)?"\s*:\s*")[^"]+(")`).ReplaceAllString(s, "${1}******${3}")
	return s
}

// Digest 摘要（审计用）：长度截断
func Digest(s string, n int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}