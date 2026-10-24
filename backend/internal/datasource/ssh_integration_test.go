package datasource

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
)

// 真实 sshd 上的"测试连接"验证(默认跳过):
//
//	CAP_SSH_TEST_ADDR=127.0.0.1:2222 CAP_SSH_TEST_USER=cap CAP_SSH_TEST_PASS=cap123 \
//	  go test ./internal/datasource/ -run TestSSHAdapterRealTarget -v
func TestSSHAdapterRealTarget(t *testing.T) {
	addr := os.Getenv("CAP_SSH_TEST_ADDR")
	if addr == "" {
		t.Skip("未设置 CAP_SSH_TEST_ADDR, 跳过真实 SSH 验证")
	}
	host, portStr, err := splitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	h, cipher := newTestHandler(t)
	// 数据源配置与生产一致: 密码以密文入库
	enc, err := cipher.Encrypt(envOrStr("CAP_SSH_TEST_PASS", "cap123"))
	if err != nil {
		t.Fatal(err)
	}
	src := &persistence.DataSource{
		Type: "ssh",
		Config: fmt.Sprintf(`{"type":"ssh","host":%q,"port":%d,"username":%q,"password":%q,"os":"linux","tempDir":"/tmp"}`,
			host, portStr, envOrStr("CAP_SSH_TEST_USER", "cap"), enc),
	}
	res, err := h.cred.Test(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("连接测试失败: %s", res.Message)
	}
	if !strings.Contains(res.Message, "临时目录可写") {
		t.Fatalf("message: %s", res.Message)
	}
}

func splitHostPort(addr string) (string, int, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("addr 须为 host:port: %s", addr)
	}
	p, err := strconv.Atoi(addr[i+1:])
	if err != nil {
		return "", 0, err
	}
	return addr[:i], p, nil
}

func envOrStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
