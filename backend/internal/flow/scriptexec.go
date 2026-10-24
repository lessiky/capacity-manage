package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	gssh "golang.org/x/crypto/ssh"
)

// 远程脚本采集: 脚本与参数经 SFTP 落到目标机临时目录 → 执行 → 读 stdout 最后一行 JSON → 删除临时目录。
// Windows 与 Linux 同规则, 差异仅在运行器命令与 SFTP 路径形态(见 SFTPPath)。

// ScriptSpec 待执行的脚本
type ScriptSpec struct {
	Language string // python / bash / powershell
	Content  string
}

// RemoteTarget 执行目标(来自 type=ssh 数据源, 凭据已解密)
type RemoteTarget struct {
	OS       string // linux / windows
	Host     string
	Port     int
	Username string
	Password string
	TempDir  string // 目标机临时目录(执行视角路径)
}

// ScriptRunResult 执行结果
type ScriptRunResult struct {
	Status     string // success / failed / timeout
	Data       string // stdout 最后一行 JSON(成功时)
	Stdout     string
	Stderr     string
	ExitCode   int
	DurationMS int64
	Dir        string
}

// RemoteTransport 单次执行内的远程操作(一次执行只建一条 SSH 连接)
type RemoteTransport interface {
	UploadFile(ctx context.Context, remotePath, content string) error
	RunCommand(ctx context.Context, cmd string, timeout time.Duration) (stdout, stderr string, exitCode int, err error)
	RemoveDir(ctx context.Context, remoteDir string) error
	Close() error
}

type TransportFactory func(ctx context.Context, t RemoteTarget) (RemoteTransport, error)

// RemoteRunner 远程脚本执行器; NewTransport 可注入, 便于单测
type RemoteRunner struct {
	NewTransport TransportFactory
	Log          *slog.Logger
}

var scriptExt = map[string]string{"python": ".py", "bash": ".sh", "powershell": ".ps1"}

// ScriptFileName 语言 → 落地文件名
func ScriptFileName(lang string) (string, error) {
	ext, ok := scriptExt[lang]
	if !ok {
		return "", fmt.Errorf("不支持的脚本语言: %s(可选 python/bash/powershell)", lang)
	}
	return "script" + ext, nil
}

// RemoteTempDir 目标机临时目录(执行视角路径, 统一使用正斜杠)
func RemoteTempDir(osName, base, taskID string) string {
	base = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(base), "\\", "/"), "/")
	if base == "" {
		if osName == "windows" {
			base = "C:/Windows/Temp"
		} else {
			base = "/tmp"
		}
	}
	return base + "/capcollect-" + taskID
}

// SFTPPath 执行视角路径 → SFTP 视角路径。
// Windows OpenSSH 的 SFTP 子系统的根是盘符根, 需 `C:/x` → `/C:/x`; Linux 原样。
func SFTPPath(osName, p string) string {
	if osName == "windows" && len(p) >= 2 && p[1] == ':' {
		return "/" + p
	}
	return p
}

// BuildRemoteCommand 按 OS + 语言拼运行命令(脚本路径含空格时加引号)
func BuildRemoteCommand(osName, lang, scriptPath string) (string, error) {
	if _, err := ScriptFileName(lang); err != nil {
		return "", err
	}
	q := quoteRemotePath(osName, scriptPath)
	switch osName {
	case "linux":
		switch lang {
		case "python":
			return "python3 " + q, nil
		case "bash":
			return "bash " + q, nil
		case "powershell":
			return "pwsh -NoProfile -NonInteractive -File " + q, nil
		}
	case "windows":
		switch lang {
		case "python":
			return "python " + q, nil
		case "bash":
			return "bash " + q, nil
		case "powershell":
			return "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + q, nil
		}
	}
	return "", fmt.Errorf("不支持的执行目标 OS: %s(可选 linux/windows)", osName)
}

func quoteRemotePath(osName, p string) string {
	if osName == "windows" {
		if strings.ContainsAny(p, " \t") {
			return `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
		}
		return p
	}
	if strings.ContainsAny(p, " \t'\"$`\\&|;<>()*?[]#~!{}") {
		return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	return p
}

// MergeScriptParams 合并内置变量与流程自定义参数; 同名以内置为准
// (system_code/metric_code 等属于流程身份, 不允许被流程配置覆盖)
func MergeScriptParams(builtin, user map[string]string) map[string]string {
	out := make(map[string]string, len(builtin)+len(user))
	for k, v := range user {
		out[k] = v
	}
	for k, v := range builtin {
		out[k] = v
	}
	return out
}

// LastJSONLine 取 stdout 最后一行合法 JSON(脚本可先打日志再输出结果)
func LastJSONLine(out string) (string, error) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if json.Valid([]byte(line)) {
			return line, nil
		}
	}
	return "", fmt.Errorf("stdout 无 JSON 输出行")
}

// Run 执行一次远程脚本采集。返回 error 仅表示"执行器本身无法工作"(连接/上传失败);
// 脚本失败/超时以 Status 表达, 与本地沙箱返回语义一致。
func (r *RemoteRunner) Run(ctx context.Context, t RemoteTarget, spec ScriptSpec, params map[string]string,
	taskID string, timeout time.Duration) (*ScriptRunResult, error) {
	fileName, err := ScriptFileName(spec.Language)
	if err != nil {
		return nil, err
	}
	if r.NewTransport == nil {
		return nil, fmt.Errorf("远程执行器未初始化")
	}
	tr, err := r.NewTransport(ctx, t)
	if err != nil {
		return nil, err
	}
	defer tr.Close()

	dir := RemoteTempDir(t.OS, t.TempDir, taskID)
	sftpDir := SFTPPath(t.OS, dir)
	// 临时目录必须删除: 成功/失败/超时三条路径共用同一清理逻辑
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := tr.RemoveDir(cctx, sftpDir); err != nil && r.Log != nil {
			r.Log.Warn("清理远程临时目录失败", slog.String("dir", dir), slog.String("err", err.Error()))
		}
	}()

	execScriptPath := dir + "/" + fileName
	if err := tr.UploadFile(ctx, SFTPPath(t.OS, execScriptPath), spec.Content); err != nil {
		return nil, fmt.Errorf("上传脚本失败: %w", err)
	}
	paramsJSON, err := json.MarshalIndent(MergeScriptParams(nil, params), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("参数序列化失败: %w", err)
	}
	execParamsPath := dir + "/params.json"
	if err := tr.UploadFile(ctx, SFTPPath(t.OS, execParamsPath), string(paramsJSON)); err != nil {
		return nil, fmt.Errorf("上传参数失败: %w", err)
	}
	cmd, err := BuildRemoteCommand(t.OS, spec.Language, execScriptPath)
	if err != nil {
		return nil, err
	}

	res := &ScriptRunResult{Status: "failed", Dir: dir}
	start := time.Now()
	stdout, stderr, exit, runErr := tr.RunCommand(ctx, cmd, timeout)
	res.DurationMS = time.Since(start).Milliseconds()
	res.Stdout, res.Stderr, res.ExitCode = stdout, stderr, exit

	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			res.Status = "timeout"
		}
		if strings.TrimSpace(res.Stderr) == "" {
			res.Stderr = runErr.Error()
		}
		return res, nil
	}
	if exit != 0 {
		if strings.TrimSpace(res.Stderr) == "" {
			res.Stderr = fmt.Sprintf("脚本退出码 %d", exit)
		}
		return res, nil
	}
	data, jerr := LastJSONLine(stdout)
	if jerr != nil {
		res.Stderr = strings.TrimSpace(res.Stderr + "\n" + jerr.Error())
		return res, nil
	}
	res.Status = "success"
	res.Data = data
	return res, nil
}

// NewSSHRunner 生产实现: 每次执行建一条 SSH 连接, 其上复用 SFTP 与 exec 会话
func NewSSHRunner(log *slog.Logger) *RemoteRunner {
	return &RemoteRunner{NewTransport: dialSSHTransport, Log: log}
}

func dialSSHTransport(ctx context.Context, t RemoteTarget) (RemoteTransport, error) {
	cfg := &gssh.ClientConfig{
		User:            t.Username,
		Auth:            []gssh.AuthMethod{gssh.Password(t.Password)},
		HostKeyCallback: gssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(t.Host, fmt.Sprint(t.Port))
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	netConn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	conn, chans, reqs, err := gssh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("SSH 认证失败: %w", err)
	}
	client := gssh.NewClient(conn, chans, reqs)
	sc, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("SFTP 会话初始化失败: %w", err)
	}
	return &sshTransport{client: client, sftp: sc}, nil
}

type sshTransport struct {
	client *gssh.Client
	sftp   *sftp.Client
}

func (s *sshTransport) UploadFile(_ context.Context, remotePath, content string) error {
	if dir := path.Dir(remotePath); dir != "" && dir != "." {
		if err := s.sftp.MkdirAll(dir); err != nil {
			return fmt.Errorf("创建临时目录失败: %w", err)
		}
	}
	f, err := s.sftp.Create(remotePath)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (s *sshTransport) RunCommand(ctx context.Context, cmd string, timeout time.Duration) (string, string, int, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(cmd); err != nil {
		return "", "", -1, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case werr := <-done:
		if werr == nil {
			return stdout.String(), stderr.String(), 0, nil
		}
		var exitErr *gssh.ExitError
		if errors.As(werr, &exitErr) {
			return stdout.String(), stderr.String(), exitErr.ExitStatus(), nil
		}
		return stdout.String(), stderr.String(), -1, werr
	case <-timer.C:
		// 跨网络无法强杀远端进程组: 断开会话, 由脚本自身有界
		_ = session.Signal(gssh.SIGKILL)
		_ = session.Close()
		return stdout.String(), stderr.String(), -1, context.DeadlineExceeded
	case <-ctx.Done():
		_ = session.Close()
		return stdout.String(), stderr.String(), -1, ctx.Err()
	}
}

func (s *sshTransport) RemoveDir(_ context.Context, remoteDir string) error {
	return s.sftp.RemoveAll(remoteDir)
}

func (s *sshTransport) Close() error {
	if s.sftp != nil {
		_ = s.sftp.Close()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
