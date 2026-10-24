package datasource

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
)

const testCredKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newTestHandler(t *testing.T) (*Handler, *aesgcm.Cipher) {
	t.Helper()
	cipher, err := aesgcm.New(testCredKey)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(nil, cipher, nil, slog.Default())
	return &Handler{db: nil, cred: reg, log: slog.Default()}, cipher
}

func oracleCfgJSON(extra string) string {
	s := `{"type":"db","driver":"oracle","host":"10.1.1.1","port":1521,"service":"ORCL","username":"scott"` + extra + `}`
	return s
}

func passwordOf(t *testing.T, cfgJSON string) string {
	t.Helper()
	var cfg Config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Password
}

// 编辑时密码留空 => 旧密文原样沿用, 不得二次加密
func TestMergeConfigBlankPasswordKeepsOldCiphertext(t *testing.T) {
	h, cipher := newTestHandler(t)
	oldEnc, err := cipher.Encrypt("tiger")
	if err != nil {
		t.Fatal(err)
	}
	src := &persistence.DataSource{Config: oracleCfgJSON(`,"password":"` + oldEnc + `"`) }
	merged, err := h.mergeConfig(src, []byte(`{"driver":"oracle","host":"10.1.1.2","port":1521,"service":"ORCL","username":"scott"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := passwordOf(t, string(merged))
	if got != oldEnc {
		t.Fatalf("password changed: got prefix %q, want old ciphertext kept", got[:min(16, len(got))])
	}
}

// 展示层掩码值回传 => 视为未修改
func TestMergeConfigMaskedPasswordKeepsOldCiphertext(t *testing.T) {
	h, cipher := newTestHandler(t)
	oldEnc, _ := cipher.Encrypt("tiger")
	src := &persistence.DataSource{Config: oracleCfgJSON(`,"password":"` + oldEnc + `"`) }
	merged, err := h.mergeConfig(src, []byte(`{"driver":"oracle","host":"10.1.1.2","port":1521,"service":"ORCL","username":"scott","password":"******"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := passwordOf(t, string(merged)); got != oldEnc {
		t.Fatal("masked password must keep old ciphertext")
	}
}

// 新密码 => 加密后落库, 解密回原文
func TestMergeConfigNewPasswordEncrypted(t *testing.T) {
	h, cipher := newTestHandler(t)
	src := &persistence.DataSource{Config: oracleCfgJSON(`,"password":"` + func() string { e, _ := cipher.Encrypt("old"); return e }() + `"`) }
	merged, err := h.mergeConfig(src, []byte(`{"driver":"oracle","host":"10.1.1.1","port":1521,"service":"ORCL","username":"scott","password":"tiger2"}`))
	if err != nil {
		t.Fatal(err)
	}
	enc := passwordOf(t, string(merged))
	p, err := cipher.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if p != "tiger2" {
		t.Fatalf("decrypted %q", p)
	}
}

// 存量数据二次加密 => 读取时逐层解密自动修复
func TestDecryptForCollectRepairsDoubleEncryption(t *testing.T) {
	h, cipher := newTestHandler(t)
	e1, _ := cipher.Encrypt("p@ss:word@#")
	e2, _ := cipher.Encrypt(e1) // 历史 bug 产生的双层密文
	cfg := &Config{Type: "db", Driver: "oracle", Password: e2}
	if err := h.cred.DecryptForCollect(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "p@ss:word@#" {
		t.Fatalf("repair failed: %q", cfg.Password)
	}
}

// 单层密文与普通明文行为不变
func TestDecryptForCollectSingleAndPlaintext(t *testing.T) {
	h, cipher := newTestHandler(t)
	e1, _ := cipher.Encrypt("abc")
	cfg := &Config{Type: "db", Password: e1}
	if err := h.cred.DecryptForCollect(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "abc" {
		t.Fatalf("single: %q", cfg.Password)
	}
	cfg2 := &Config{Type: "db", Password: "plain-legacy"}
	if err := h.cred.DecryptForCollect(cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.Password != "plain-legacy" {
		t.Fatalf("plaintext: %q", cfg2.Password)
	}
}

// ssh 执行目标的凭据与 db/sftp 一样, 采集前必须解密(否则用密文当密码认证必失败)
func TestDecryptForCollectSSH(t *testing.T) {
	h, cipher := newTestHandler(t)
	enc, err := cipher.Encrypt("cap123")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Type: "ssh", Password: enc}
	if err := h.cred.DecryptForCollect(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "cap123" {
		t.Fatalf("ssh 密码未解密: %q", cfg.Password)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 远程脚本执行目标: ssh 类型默认值(端口/OS/临时目录) + 凭据加密
func TestNormalizeConfigSSHDefaults(t *testing.T) {
	h, cipher := newTestHandler(t)

	// 缺 host/username → 拒绝
	if _, err := h.normalizeConfig("ssh", json.RawMessage(`{"port":22}`)); err == nil {
		t.Fatal("ssh 缺 host/username 应被拒绝")
	}

	// linux: 默认 port=22, os=linux, tempDir=/tmp
	out, err := h.normalizeConfig("ssh", json.RawMessage(`{"host":"10.1.1.9","username":"cap","password":"secret","os":"linux"}`))
	if err != nil {
		t.Fatal(err)
	}
	var linuxCfg Config
	if err := json.Unmarshal(out, &linuxCfg); err != nil {
		t.Fatal(err)
	}
	if linuxCfg.Type != "ssh" || linuxCfg.Port != 22 || linuxCfg.OS != "linux" || linuxCfg.TempDir != "/tmp" {
		t.Fatalf("linux defaults: %+v", linuxCfg)
	}
	plain, err := cipher.Decrypt(linuxCfg.Password)
	if err != nil || plain != "secret" {
		t.Fatalf("password not encrypted: %q err=%v", linuxCfg.Password, err)
	}

	// windows: os=windows 时临时目录默认 C:/Windows/Temp
	out, err = h.normalizeConfig("ssh", json.RawMessage(`{"host":"10.1.1.10","username":"cap","password":"secret","os":"windows"}`))
	if err != nil {
		t.Fatal(err)
	}
	var winCfg Config
	if err := json.Unmarshal(out, &winCfg); err != nil {
		t.Fatal(err)
	}
	if winCfg.OS != "windows" || winCfg.TempDir != "C:/Windows/Temp" {
		t.Fatalf("windows defaults: %+v", winCfg)
	}

	// 已显式给出的 tempDir 不被覆盖
	out, err = h.normalizeConfig("ssh", json.RawMessage(`{"host":"h","username":"u","os":"linux","tempDir":"/data/tmp"}`))
	if err != nil {
		t.Fatal(err)
	}
	var custom Config
	if err := json.Unmarshal(out, &custom); err != nil {
		t.Fatal(err)
	}
	if custom.TempDir != "/data/tmp" {
		t.Fatalf("tempDir overridden: %+v", custom)
	}
}

// 执行目标连接测试: 探测临时目录可写(建目录→删目录), 命令按 OS 区分
func TestSSHProbeCommand(t *testing.T) {
	got := sshProbeCommand("linux", "/data/tmp")
	if !strings.Contains(got, "mkdir -p") || !strings.Contains(got, "'/data/tmp/capcollect-probe'") {
		t.Fatalf("linux probe: %q", got)
	}
	got = sshProbeCommand("windows", "C:/Windows/Temp")
	if !strings.Contains(got, `mkdir "C:/Windows/Temp/capcollect-probe"`) {
		t.Fatalf("windows probe: %q", got)
	}
	// 含空格路径必须加引号
	if got := sshProbeCommand("linux", "/data/cap tmp"); !strings.Contains(got, "'/data/cap tmp/capcollect-probe'") {
		t.Fatalf("linux probe with space: %q", got)
	}
}

// ssh 类型必须走连接测试分支, 不能落到"未知数据源类型"
func TestRegistryTestDispatchesSSH(t *testing.T) {
	h, _ := newTestHandler(t)
	src := &persistence.DataSource{
		Type:   "ssh",
		Config: `{"type":"ssh","host":"127.0.0.1","port":1,"username":"x","os":"linux","tempDir":"/tmp"}`,
	}
	res, err := h.cred.Test(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("不可达目标不应返回 OK")
	}
	if strings.Contains(res.Message, "未知数据源类型") {
		t.Fatalf("ssh 未接入连接测试分支: %s", res.Message)
	}
	if !strings.Contains(res.Message, "SSH") {
		t.Fatalf("错误信息应说明 SSH 连接问题: %s", res.Message)
	}
}
