package alert

import "testing"

func TestValidateNotificationChannelBlocksSSRF(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "云元数据链路本地地址", url: "http://169.254.169.254/latest/meta-data"},
		{name: "回环地址", url: "http://127.0.0.1:8080/internal"},
		{name: "localhost", url: "http://localhost:8080/internal"},
		{name: "阿里云元数据地址", url: "http://100.100.100.200/latest/meta-data"},
		{name: "IPv6 回环地址", url: "http://[::1]:8080/internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNotificationChannel("webhook", map[string]any{"url": tc.url})
			if err == nil {
				t.Fatalf("validateNotificationChannel(%q) = nil, want SSRF rejection", tc.url)
			}
		})
	}
}

func TestValidateNotificationChannelAcceptsPublicWebhook(t *testing.T) {
	err := validateNotificationChannel("webhook", map[string]any{
		"url": "https://8.8.8.8/services/capacity",
	})
	if err != nil {
		t.Fatalf("validateNotificationChannel(public) = %v, want nil", err)
	}
}
