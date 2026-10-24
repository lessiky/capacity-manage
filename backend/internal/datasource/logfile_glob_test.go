package datasource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
)

// 无任何硬编码兜底: 仅配 pathTemplate(目录) 时不会自动去读 *run_*.txt, 必须显式配 globTemplate
func TestReadLogFiles_PathTemplateOnlyNoHardcodedGlob(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a5_ss_fos_zx_41_run_20261016.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Type: "log", Scheme: "local", Encoding: "utf-8", PathTemplate: filepath.ToSlash(dir) + "/"}
	files, err := ReadLogFiles(context.Background(), cfg, "")
	// 目录本身会被 glob 命中 → 试图按文件读取失败(Windows "Incorrect function" / Linux "is a directory"),
	// 关键是绝不能凭空读到目录下的 run 文件
	if err == nil && len(files) > 0 {
		t.Fatalf("仅 pathTemplate 时读到了文件 %+v, 说明存在硬编码兜底", files)
	}
	if err == nil {
		t.Fatal("仅 pathTemplate 时应报错(目录不是日志文件)")
	}
	if strings.Contains(err.Error(), "run_") {
		t.Fatalf("err = %v, 不应指向 run 文件", err)
	}
}

// globTemplate 才是真正决定读哪些文件的配置
func TestReadLogFiles_GlobTemplateDrivesMatch(t *testing.T) {
	dir := t.TempDir()
	name := "a5_ss_fos_zx_41_run_20261016.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Type: "log", Scheme: "local", Encoding: "utf-8",
		PathTemplate: filepath.ToSlash(dir) + "/",
		GlobTemplate: filepath.ToSlash(dir) + "/*run_*20261016.txt",
	}
	files, err := ReadLogFiles(context.Background(), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0].Path) != name {
		t.Fatalf("files = %+v, want 仅 %s", files, name)
	}
}

// 编辑表单未提交 globTemplate 字段(旧版前端/脚本) => 沿用旧值, 不能被整体覆盖抹掉
func TestMergeConfigKeepsGlobTemplateWhenFieldAbsent(t *testing.T) {
	h, _ := newTestHandler(t)
	src := &persistence.DataSource{Type: "log", Config: `{"type":"log","scheme":"local","encoding":"gbk",
		"pathTemplate":"/data/aboss/log/20261019/","globTemplate":"/data/aboss/log/20261019/*run_*20261019.txt"}`}
	merged, err := h.mergeConfig(src, []byte(`{"type":"log","scheme":"local","encoding":"gbk","pathTemplate":"/data/aboss/log/20261019/"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatal(err)
	}
	if got.GlobTemplate == "" {
		t.Fatal("globTemplate 被抹掉了, 采集将退化为只读目录")
	}
}

// 显式提交空 globTemplate => 视为清空(允许用户主动移除)
func TestMergeConfigClearsGlobTemplateWhenExplicitEmpty(t *testing.T) {
	h, _ := newTestHandler(t)
	src := &persistence.DataSource{Type: "log", Config: `{"type":"log","scheme":"local","encoding":"gbk",
		"pathTemplate":"/data/aboss/log/20261019/","globTemplate":"/data/aboss/log/20261019/*run_*20261019.txt"}`}
	merged, err := h.mergeConfig(src, []byte(`{"type":"log","scheme":"local","encoding":"gbk",
		"pathTemplate":"/data/aboss/log/20261019/","globTemplate":""}`))
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatal(err)
	}
	if got.GlobTemplate != "" {
		t.Fatalf("globTemplate = %q, want 清空", got.GlobTemplate)
	}
}
