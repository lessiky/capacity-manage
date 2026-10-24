package flow

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 真实 SSH 目标的端到端验证(默认跳过, 避免 CI 依赖外部 sshd):
//
//	CAP_SSH_TEST_ADDR=127.0.0.1:2222 CAP_SSH_TEST_USER=cap CAP_SSH_TEST_PASS=cap123 \
//	  go test ./internal/flow/ -run TestSSHRunnerRealTarget -v
func TestSSHRunnerRealTarget(t *testing.T) {
	addr := os.Getenv("CAP_SSH_TEST_ADDR")
	if addr == "" {
		t.Skip("未设置 CAP_SSH_TEST_ADDR, 跳过真实 SSH 验证")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("CAP_SSH_TEST_ADDR 须为 host:port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	user := envOr("CAP_SSH_TEST_USER", "cap")
	pass := envOr("CAP_SSH_TEST_PASS", "cap123")
	target := RemoteTarget{OS: "linux", Host: host, Port: port, Username: user, Password: pass, TempDir: "/tmp"}

	t.Run("python", func(t *testing.T) {
		// 脚本从同目录 params.json 取入参, 输出最后一行 JSON
		script := `import json, os
here = os.path.dirname(os.path.abspath(__file__))
with open(os.path.join(here, "params.json"), encoding="utf-8") as f:
    p = json.load(f)
print("collecting disk usage for", p.get("mount"))
print(json.dumps([{"metric": "DISK_USAGE_PCT", "value": round(float(p.get("seed", 0)), 2), "unit": "%"}]))
`
		res := runOnTarget(t, target, ScriptSpec{Language: "python", Content: script}, "itest-py")
		if !strings.Contains(res.Data, "88.5") || !strings.Contains(res.Data, "DISK_USAGE_PCT") {
			t.Fatalf("出参异常: %q (stdout=%q)", res.Data, res.Stdout)
		}
	})

	t.Run("bash", func(t *testing.T) {
		script := `#!/bin/bash
here="$(cd "$(dirname "$0")" && pwd)"
mount=$(sed -n 's/.*"mount": *"\([^"]*\)".*/\1/p' "$here/params.json")
echo "collecting disk usage for ${mount}"
echo '[{"metric":"DISK_USAGE_PCT","value":42.5,"unit":"%"}]'
`
		res := runOnTarget(t, target, ScriptSpec{Language: "bash", Content: script}, "itest-sh")
		if !strings.Contains(res.Data, "42.5") {
			t.Fatalf("出参异常: %q (stdout=%q)", res.Data, res.Stdout)
		}
	})

	// 断言临时目录已删除: 直接复用 transport 列 /tmp 下的 capcollect-*
	ctx := context.Background()
	tr, err := dialSSHTransport(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	stdout, _, _, err := tr.RunCommand(ctx, "ls -d /tmp/capcollect-* 2>/dev/null || true", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("临时目录未清理: %q", stdout)
	}
}

// runOnTarget 执行并断言成功
func runOnTarget(t *testing.T, target RemoteTarget, spec ScriptSpec, taskID string) *ScriptRunResult {
	t.Helper()
	res, err := NewSSHRunner(slog.Default()).Run(context.Background(), target, spec,
		map[string]string{"mount": "/data", "seed": "88.5"}, taskID, 30*time.Second)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status=%s stderr=%s", res.Status, res.Stderr)
	}
	return res
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
