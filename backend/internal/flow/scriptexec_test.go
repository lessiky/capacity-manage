package flow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestScriptFileName(t *testing.T) {
	cases := map[string]string{"python": "script.py", "bash": "script.sh", "powershell": "script.ps1"}
	for lang, want := range cases {
		got, err := ScriptFileName(lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", lang, got, want)
		}
	}
	if _, err := ScriptFileName("perl"); err == nil {
		t.Fatal("perl 应被拒绝")
	}
}

func TestBuildRemoteCommand(t *testing.T) {
	cases := []struct {
		os, lang, path, want string
	}{
		{"linux", "python", "/tmp/capcollect-1/script.py", "python3 /tmp/capcollect-1/script.py"},
		{"linux", "bash", "/tmp/capcollect-1/script.sh", "bash /tmp/capcollect-1/script.sh"},
		{"linux", "powershell", "/tmp/capcollect-1/script.ps1", "pwsh -NoProfile -NonInteractive -File /tmp/capcollect-1/script.ps1"},
		{"windows", "python", "C:/Windows/Temp/capcollect-1/script.py", "python C:/Windows/Temp/capcollect-1/script.py"},
		{"windows", "bash", "C:/Windows/Temp/capcollect-1/script.sh", "bash C:/Windows/Temp/capcollect-1/script.sh"},
		{"windows", "powershell", "C:/Windows/Temp/capcollect-1/script.ps1",
			"powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:/Windows/Temp/capcollect-1/script.ps1"},
		// 路径含空格必须加引号, 否则运行器会把路径截断
		{"linux", "python", "/data/cap collect/script.py", "python3 '/data/cap collect/script.py'"},
		{"windows", "python", "C:/Program Files/tmp/script.py", `python "C:/Program Files/tmp/script.py"`},
	}
	for _, c := range cases {
		got, err := BuildRemoteCommand(c.os, c.lang, c.path)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.os, c.lang, err)
		}
		if got != c.want {
			t.Fatalf("%s/%s: got %q want %q", c.os, c.lang, got, c.want)
		}
	}
	if _, err := BuildRemoteCommand("linux", "perl", "/tmp/x"); err == nil {
		t.Fatal("未知语言应报错")
	}
	if _, err := BuildRemoteCommand("plan9", "python", "/tmp/x"); err == nil {
		t.Fatal("未知 OS 应报错")
	}
}

func TestRemoteTempDirAndSFTPPath(t *testing.T) {
	if got := RemoteTempDir("linux", "/tmp", "flow-7-173"); got != "/tmp/capcollect-flow-7-173" {
		t.Fatalf("linux dir: %q", got)
	}
	if got := RemoteTempDir("linux", "/tmp/", "t1"); got != "/tmp/capcollect-t1" {
		t.Fatalf("trailing slash: %q", got)
	}
	if got := RemoteTempDir("windows", "C:/Windows/Temp", "t1"); got != "C:/Windows/Temp/capcollect-t1" {
		t.Fatalf("windows dir: %q", got)
	}
	// Windows OpenSSH 的 SFTP 需要 /C:/... 形式, 执行命令用 C:/... 形式
	if got := SFTPPath("windows", "C:/Windows/Temp/capcollect-t1/script.py"); got != "/C:/Windows/Temp/capcollect-t1/script.py" {
		t.Fatalf("windows sftp path: %q", got)
	}
	if got := SFTPPath("linux", "/tmp/capcollect-t1/script.py"); got != "/tmp/capcollect-t1/script.py" {
		t.Fatalf("linux sftp path: %q", got)
	}
}

func TestMergeScriptParams(t *testing.T) {
	builtin := map[string]string{"biz_date": "2026-10-20", "system_code": "S1"}
	user := map[string]string{"mount": "/data", "biz_date": "1999-01-01"}
	got := MergeScriptParams(builtin, user)
	if got["mount"] != "/data" {
		t.Fatalf("自定义参数丢失: %+v", got)
	}
	if got["biz_date"] != "2026-10-20" {
		t.Fatalf("内置变量被覆盖: %q", got["biz_date"])
	}
	if got["system_code"] != "S1" {
		t.Fatalf("内置变量丢失: %+v", got)
	}
	if MergeScriptParams(nil, nil) == nil {
		t.Fatal("nil 入参应返回空 map 而非 nil")
	}
}

func TestLastJSONLine(t *testing.T) {
	if got, err := LastJSONLine("some log\n{\"a\":1}\n"); err != nil || got != `{"a":1}` {
		t.Fatalf("got %q err %v", got, err)
	}
	if got, err := LastJSONLine("{\"a\":1}\n[{\"metric\":\"M\"}]\n"); err != nil || got != `[{"metric":"M"}]` {
		t.Fatalf("last of many: %q err %v", got, err)
	}
	if _, err := LastJSONLine("no json at all"); err == nil {
		t.Fatal("无 JSON 应报错")
	}
	if _, err := LastJSONLine("   \n  "); err == nil {
		t.Fatal("空输出应报错")
	}
}

// fakeRemote 记录调用顺序, 供编排测试断言(单连接 transport: 一次执行只建一次 SSH)
type fakeRemote struct {
	uploads  []string
	uploaded map[string]string
	commands []string
	removed  []string
	closed   int
	stdout   string
	stderr   string
	exitCode int
	runErr   error
}

func (f *fakeRemote) UploadFile(_ context.Context, path, content string) error {
	f.uploads = append(f.uploads, path)
	f.uploaded[path] = content
	return nil
}

func (f *fakeRemote) RunCommand(_ context.Context, cmd string, _ time.Duration) (string, string, int, error) {
	f.commands = append(f.commands, cmd)
	return f.stdout, f.stderr, f.exitCode, f.runErr
}

func (f *fakeRemote) RemoveDir(_ context.Context, dir string) error {
	f.removed = append(f.removed, dir)
	return nil
}

func (f *fakeRemote) Close() error {
	f.closed++
	return nil
}

func newFakeRunner(f *fakeRemote) *RemoteRunner {
	f.uploaded = map[string]string{}
	return &RemoteRunner{
		NewTransport: func(_ context.Context, _ RemoteTarget) (RemoteTransport, error) {
			return f, nil
		},
	}
}

func TestRunRemoteScriptUploadsExecutesAndCleans(t *testing.T) {
	f := &fakeRemote{stdout: "starting\n[{\"metric\":\"DISK\",\"value\":88.5,\"unit\":\"%\"}]\n"}
	r := newFakeRunner(f)
	target := RemoteTarget{OS: "linux", TempDir: "/tmp"}
	res, err := r.Run(context.Background(), target,
		ScriptSpec{Language: "python", Content: "print(1)"},
		map[string]string{"mount": "/data"}, "flow-7-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "success" || !strings.Contains(res.Data, "DISK") {
		t.Fatalf("result: %+v", res)
	}
	dir := "/tmp/capcollect-flow-7-1"
	if len(f.uploads) != 2 || f.uploads[0] != dir+"/script.py" || f.uploads[1] != dir+"/params.json" {
		t.Fatalf("uploads: %+v", f.uploads)
	}
	if !strings.Contains(f.uploaded[dir+"/params.json"], "/data") {
		t.Fatalf("params.json 内容: %q", f.uploaded[dir+"/params.json"])
	}
	if len(f.commands) != 1 || f.commands[0] != "python3 "+dir+"/script.py" {
		t.Fatalf("commands: %+v", f.commands)
	}
	if len(f.removed) != 1 || f.removed[0] != dir {
		t.Fatalf("removed: %+v", f.removed)
	}
	if f.closed != 1 {
		t.Fatalf("连接未关闭: %d", f.closed)
	}
}

// Windows 目标: 上传走 /C:/... 形式, 执行命令与清理走 C:/... 形式
func TestRunRemoteScriptWindowsPathForms(t *testing.T) {
	f := &fakeRemote{stdout: `[{"metric":"M","value":1,"unit":"%"}]`}
	r := newFakeRunner(f)
	res, err := r.Run(context.Background(), RemoteTarget{OS: "windows", TempDir: "C:/Windows/Temp"},
		ScriptSpec{Language: "powershell", Content: "Write-Output 1"}, nil, "t1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "success" {
		t.Fatalf("status: %+v", res)
	}
	dir := "C:/Windows/Temp/capcollect-t1"
	if len(f.uploads) != 2 || f.uploads[0] != "/"+dir+"/script.ps1" || f.uploads[1] != "/"+dir+"/params.json" {
		t.Fatalf("windows uploads: %+v", f.uploads)
	}
	wantCmd := "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + dir + "/script.ps1"
	if len(f.commands) != 1 || f.commands[0] != wantCmd {
		t.Fatalf("windows command: %+v", f.commands)
	}
	if len(f.removed) != 1 || f.removed[0] != "/"+dir {
		t.Fatalf("windows removed: %+v", f.removed)
	}
}

func TestRunRemoteScriptCleansOnFailure(t *testing.T) {
	f := &fakeRemote{stderr: "boom", exitCode: 1}
	r := newFakeRunner(f)
	res, err := r.Run(context.Background(), RemoteTarget{OS: "linux", TempDir: "/tmp"},
		ScriptSpec{Language: "bash", Content: "exit 1"}, nil, "t9", 5*time.Second)
	if err != nil {
		t.Fatalf("执行失败不应返回 error: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status: %+v", res)
	}
	if len(f.removed) != 1 {
		t.Fatalf("失败也必须删除临时目录: %+v", f.removed)
	}
}

func TestRunRemoteScriptTimeoutAndBadOutput(t *testing.T) {
	f := &fakeRemote{runErr: context.DeadlineExceeded}
	r := newFakeRunner(f)
	res, err := r.Run(context.Background(), RemoteTarget{OS: "linux", TempDir: "/tmp"},
		ScriptSpec{Language: "python", Content: "while True: pass"}, nil, "t10", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "timeout" {
		t.Fatalf("timeout status: %+v", res)
	}
	if len(f.removed) != 1 {
		t.Fatalf("超时也必须删除临时目录: %+v", f.removed)
	}

	// 无 JSON 输出 → failed, 且仍清理
	f2 := &fakeRemote{stdout: "no json"}
	r2 := newFakeRunner(f2)
	res2, err := r2.Run(context.Background(), RemoteTarget{OS: "linux", TempDir: "/tmp"},
		ScriptSpec{Language: "python", Content: "print('x')"}, nil, "t11", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "failed" || !strings.Contains(res2.Stderr, "JSON") {
		t.Fatalf("bad output: %+v", res2)
	}
	if len(f2.removed) != 1 {
		t.Fatalf("无输出也必须删除临时目录: %+v", f2.removed)
	}
}
