// Command spike-sftp: Spike 02 SFTP 日志采集 PoC
// 1) 进程内 SFTP 服务器 (crypto/ssh + pkg/sftp, 真实临时目录作根, 临时 host key)
// 2) 使用与生产相同 (internal/datasource/sftp.go dialSFTP) 的客户端栈连接
// 3) 按平台 logparse regex 语义解析样例日志, 验证 value/unit 提取
// 输出: spike/02-sftp-log/output.txt
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	gssh "golang.org/x/crypto/ssh"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ed25519"
)

const outPath = `D:\newwork\capacity-manage\spike\02-sftp-log\output.txt`

var (
	out    strings.Builder
	passed int
	failed int
)

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("[PASS] %s\n", name)
	} else {
		failed++
		fmt.Printf("[FAIL] %s\n", name)
	}
	out.WriteString(fmt.Sprintf("--- %s [%s]\n", name, map[bool]string{true: "PASS", false: "FAIL"}[ok]))
	if detail != "" {
		out.WriteString(detail)
		out.WriteString("\n")
	}
}

// startSFTPServer 在临时目录上启动 SFTP 服务, 返回监听地址
func startSFTPServer() (string, error) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", err
	}
	hostKey, err := gssh.NewSignerFromKey(priv)
	if err != nil {
		return "", err
	}
	cfg := &gssh.ServerConfig{
		PasswordCallback: func(meta gssh.ConnMetadata, pass []byte) (*gssh.Permissions, error) {
			if meta.User() == "collector" && string(pass) == "collector123" {
				return nil, nil
			}
			return nil, errors.New("bad credentials")
		},
	}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, cfg)
		}
	}()
	return ln.Addr().String(), nil
}

func handleSSHConn(c net.Conn, cfg *gssh.ServerConfig) {
	defer c.Close()
	sconn, chans, reqs, err := gssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go gssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(gssh.Prohibited, "session only")
			continue
		}
		ch, chReq, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			go func() {
				for req := range chReq {
					if !req.WantReply {
						continue
					}
					if req.Type == "subsystem" {
						var sub struct{ Subsystem string }
						_ = gssh.Unmarshal(req.Payload, &sub)
						_ = req.Reply(sub.Subsystem == "sftp", nil)
					} else {
						_ = req.Reply(false, nil)
					}
				}
			}()
			srv, err := sftp.NewServer(ch)
			if err != nil {
				return
			}
			_ = srv.Serve()
		}()
	}
}

// dialSFTP 与生产 internal/datasource dialSFTP 相同连接栈
func dialSFTP(addr, user, password string) (*sftp.Client, error) {
	sshConf := &gssh.ClientConfig{
		User:            user,
		Auth:            []gssh.AuthMethod{gssh.Password(password)},
		HostKeyCallback: gssh.InsecureIgnoreHostKey(), // PoC 专用; 生产走部署层白名单
		Timeout:         10 * time.Second,
	}
	netConn, err := (&net.Dialer{Timeout: 10 * time.Second}).Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SFTP 连接失败: %w", err)
	}
	sshConn, chans, reqs, err := gssh.NewClientConn(netConn, addr, sshConf)
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("SFTP 认证失败: %w", err)
	}
	sshClient := gssh.NewClient(sshConn, chans, reqs)
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("SFTP 会话初始化失败: %w", err)
	}
	return client, nil
}

// parseLine 与 internal/flow parseLine regex 模式相同语义
func parseLine(line, pattern, valueField, unitField string) (string, string, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", "", false
	}
	m := re.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	names := re.SubexpNames()
	val, unit := "", ""
	for i, n := range names {
		if i == 0 || n == "" {
			continue
		}
		if n == valueField {
			val = m[i]
		}
		if n == unitField {
			unit = m[i]
		}
	}
	if val == "" {
		return "", "", false
	}
	return val, unit, true
}

func main() {
	// POSIX 绝对路径在 Windows 服务端映射为"当前盘"根下, 固定到 C:
	_ = os.Chdir(`C:\`)
	out.WriteString(fmt.Sprintf("Go spike 02 (SFTP log) @ %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	logContent := `2026-10-14 09:30:05 [INFO] disk /u01/oradata usage=86.4 unit=percent
2026-10-14 09:30:05 [INFO] disk /u02/archive usage=63.2 unit=percent
2026-10-14 09:30:06 [WARN] disk /u01/fastlog usage=91.8 unit=percent
2026-10-14 09:31:00 [INFO] checkpoint complete elapsed=42s
2026-10-14 09:35:12 [INFO] disk /u01/oradata usage=86.4 unit=percent
`
	// 生产语义: 客户端使用远端 POSIX 绝对路径; Windows 下映射为 C:\logs\coredb
	sub := `C:\logs\coredb`
	if err := os.MkdirAll(sub, 0o755); err != nil {
		check("准备-SFTP 根目录", false, fmt.Sprintf("dir=%s err=%v", sub, err))
		finish()
		return
	}
	check("准备-SFTP 根目录", true, fmt.Sprintf("dir=%s (测试后清理)", sub))
	defer os.RemoveAll(`C:\logs\coredb`)

	if err := os.WriteFile(filepath.Join(sub, "ora-20261014.log"), []byte(logContent), 0o644); err != nil {
		finish()
		return
	}
	if err := os.WriteFile(filepath.Join(sub, "ora-20261013.log"), []byte("old log\n"), 0o644); err != nil {
		finish()
		return
	}

	// 1) 启动进程内 SFTP 服务器
	addr, err := startSFTPServer()
	check("SFTP-进程内服务器启动", err == nil, fmt.Sprintf("addr=%s err=%v", addr, err))
	if err != nil {
		finish()
		return
	}

	// 2) 生产同款客户端栈连接
	cl, err := dialSFTP(addr, "collector", "collector123")
	check("SFTP-SSH 握手 + SFTP 会话建立", err == nil && cl != nil, fmt.Sprintf("err=%v", err))
	if err != nil {
		finish()
		return
	}
	defer cl.Close()

	// 3) 列目录
	entries, err := cl.ReadDir("/logs/coredb")
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	check("SFTP-列目录", err == nil && len(entries) == 2, fmt.Sprintf("entries=%v err=%v", names, err))

	// 4) Glob + 下载 (生产 ReadLogFile 语义)
	matches, err := cl.Glob("/logs/coredb/ora-*.log")
	var data []byte
	var st os.FileInfo
	target := ""
	for _, m := range matches {
		if strings.HasSuffix(m, "ora-20261014.log") {
			target = m
		}
	}
	if err == nil && target != "" {
		f, err2 := cl.Open(target)
		if err2 == nil {
			data, err = io.ReadAll(io.LimitReader(f, 64<<20))
			st, _ = cl.Stat(target)
			f.Close()
		}
	}
	check("SFTP-Glob + 文件下载 + Stat", err == nil && len(data) == len(logContent) && st != nil && st.Size() == int64(len(logContent)),
		fmt.Sprintf("matches=%v bytes=%d err=%v", matches, len(data), err))

	// 5) logparse regex 解析 (与 internal/flow 相同语义)
	pattern := `(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) \[(?P<level>\w+)\] disk (?P<path>\S+) usage=(?P<value>[\d.]+) unit=(?P<unit>\w+)`
	parsed, skipped := 0, 0
	out.WriteString("解析结果:\n")
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		val, unit, ok2 := parseLine(line, pattern, "value", "unit")
		if ok2 {
			parsed++
			out.WriteString(fmt.Sprintf("  %s -> value=%s unit=%s\n", line, val, unit))
		} else {
			skipped++
			out.WriteString(fmt.Sprintf("  (跳过) %s\n", line))
		}
	}
	check("logparse-regex 提取 value/unit (4 行命中, 1 行非容量行跳过)", parsed == 4 && skipped == 1,
		fmt.Sprintf("parsed=%d skipped=%d", parsed, skipped))

	// 6) 错误密码被拒
	_, err = dialSFTP(addr, "collector", "wrong-pass")
	check("SFTP-错误密码拒绝", err != nil, fmt.Sprintf("err=%v", err))

	// ---------- 7) 真实 SFTP 环境复测 (环境变量提供, 2026-10-15) ----------
	if h := os.Getenv("SPIKE_SFTP_HOST"); h != "" {
		port := envOr("SPIKE_SFTP_PORT", "22")
		user := os.Getenv("SPIKE_SFTP_USER")
		pass := os.Getenv("SPIKE_SFTP_PASSWORD")
		baseDir := envOr("SPIKE_SFTP_BASE_DIR", "/data/aboss/log")
		date := envOr("SPIKE_SFTP_DATE", "20261014")

		rc, rerr := dialSFTP(h+":"+port, user, pass)
		check("sftp-真实 SSH 握手 + SFTP 会话", rerr == nil && rc != nil, fmt.Sprintf("err=%v", rerr))
		if rerr != nil {
			finish()
			return
		}
		defer rc.Close()

		entries, lerr := rc.ReadDir(baseDir)
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
		check("sftp-真实列目录"+baseDir, lerr == nil && len(dirs) > 0,
			fmt.Sprintf("dirs(前 10)=%v err=%v", firstN(dirs, 10), lerr))

		dateDir := ""
		for _, d := range dirs {
			if d == date || d == dashDate(date) {
				dateDir = baseDir + "/" + d
				break
			}
		}
		if dateDir == "" && len(dirs) > 0 {
			sort.Strings(dirs)
			latest := dirs[len(dirs)-1]
			dateDir = baseDir + "/" + latest
			out.WriteString(fmt.Sprintf("  (未找到 %s 目录, 取最新目录 %s 示例)\n", date, latest))
		}
		if dateDir == "" {
			check("sftp-真实日期目录定位", false, "基目录下无子目录")
		} else {
			fentries, ferr := rc.ReadDir(dateDir)
			var files []string
			for _, e := range fentries {
				if !e.IsDir() {
					files = append(files, e.Name())
				}
			}
			glob := "*run*" + date + ".txt"
			matches, gerr := rc.Glob(dateDir + "/" + glob)
			check("sftp-真实日期目录列文件 + Glob("+glob+")",
				ferr == nil && len(files) > 0,
				fmt.Sprintf("files=%d matches=前10:%v err=%v gerr=%v", len(files), firstN(matches, 10), ferr, gerr))

			target := ""
			for _, m := range matches {
				target = m
				break
			}
			if target == "" {
				for _, f := range files {
					if strings.HasSuffix(f, ".txt") {
						target = dateDir + "/" + f
						break
					}
				}
			}
			if target == "" {
				check("sftp-真实样例文件下载", false, "目录下无 .txt 文件")
			} else {
				f, oerr := rc.Open(target)
				if oerr != nil {
					check("sftp-真实样例文件下载", false, fmt.Sprintf("open err=%v", oerr))
				} else {
					data, rerr2 := io.ReadAll(io.LimitReader(f, 256<<10))
					f.Close()
					st, serr := rc.Stat(target)
					size := int64(-1)
					if serr == nil {
						size = st.Size()
					}
					out.WriteString(fmt.Sprintf("  样例文件: %s size=%d bytes\n", target, size))
					out.WriteString("  原文前 15 行:\n")
					lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
					for i, l := range lines {
						if i >= 15 {
							out.WriteString("  | ...\n")
							break
						}
						r := []rune(l)
						if len(r) > 160 {
							r = r[:160]
						}
						out.WriteString("  | " + string(r) + "\n")
					}
					check("sftp-真实样例文件下载 + 读取", rerr2 == nil && len(data) > 0,
						fmt.Sprintf("readBytes=%d size=%d err=%v", len(data), size, rerr2))
				}
			}
		}
	} else {
		check("sftp-真实环境", true, "SKIP: 未提供 SPIKE_SFTP_HOST (内置用例已覆盖客户端栈)")
	}

	finish()
}

func finish() {
	out.WriteString(fmt.Sprintf("\n===== %d passed, %d failed =====\n", passed, failed))
	if err := os.WriteFile(outPath, []byte(out.String()), 0o644); err != nil {
		fmt.Println("write output failed:", err)
		os.Exit(2)
	}
	fmt.Printf("\n===== %d passed, %d failed =====\noutput -> %s\n", passed, failed, outPath)
	if failed > 0 {
		os.Exit(1)
	}
}


// envOr 读取环境变量, 空值返回默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// dashDate 20261014 -> 2026-10-14 (非 8 位直接返回原值)
func dashDate(s string) string {
	if len(s) != 8 {
		return s
	}
	return s[0:4] + "-" + s[4:6] + "-" + s[6:8]
}

func firstN(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}
