package alert

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// validateWebhookTarget rejects URL forms that can be used for SSRF against
// loopback, link-local metadata services or carrier-grade NAT metadata.
// RFC1918 intranet targets remain allowed because enterprise IM/webhook
// gateways commonly live on internal networks.
func validateWebhookTarget(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("Webhook URL 非法")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("Webhook URL 缺少主机")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("Webhook 不允许访问本机地址")
	}

	if ip := net.ParseIP(host); ip != nil {
		if blockedWebhookIP(ip) {
			return fmt.Errorf("Webhook 目标地址不允许")
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("Webhook 域名无法解析")
	}
	for _, item := range ips {
		if blockedWebhookIP(item.IP) {
			return fmt.Errorf("Webhook 域名解析到受限地址")
		}
	}
	return nil
}

func blockedWebhookIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip.Equal(net.ParseIP("100.100.100.200")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(ip)
}
