package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"capacity-manage/backend/internal/datasource"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"
	"capacity-manage/backend/internal/system"
	"capacity-manage/backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func newScriptCollectHarness(t *testing.T, dsType, dsConfig string) (*Collectors, *persistence.PythonScript, *persistence.CapacityMetric, *persistence.DataSource) {
	t.Helper()
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.PythonScript{}, &persistence.PythonScriptVersion{},
		&persistence.DataSource{}, &persistence.CapacityMetric{}, &persistence.SysParam{})
	cipher, err := aesgcm.New(strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	cred := datasource.NewRegistry(h.DB, cipher, nil, slog.Default())
	collectors := NewCollectors(h.DB, cred, nil, system.NewParams(h.DB), nil)

	script := persistence.PythonScript{Name: "disk_" + dsType, ScriptType: "collect", Language: "python"}
	if err := h.DB.Create(&script).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.PythonScriptVersion{
		ScriptID: script.ID, Version: 1, Content: "print(1)", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	metric := persistence.CapacityMetric{
		MetricCode: "DISK_USAGE_PCT", Name: "磁盘利用率", SystemCode: "S1",
		MetricType: "usage", Unit: "%", StatMode: "avg", JudgeDirection: "higher_worse", Status: "active",
	}
	if err := h.DB.Create(&metric).Error; err != nil {
		t.Fatal(err)
	}
	ds := persistence.DataSource{Name: "target-" + dsType, Type: dsType, Status: "available", Config: dsConfig, SystemCode: "S1"}
	if err := h.DB.Create(&ds).Error; err != nil {
		t.Fatal(err)
	}
	return collectors, &script, &metric, &ds
}

// 脚本采集必须绑定 ssh 执行目标
func TestCollectScriptRequiresRemoteTarget(t *testing.T) {
	collectors, script, metric, ds := newScriptCollectHarness(t, "log", `{"type":"log","scheme":"local","globTemplate":"/tmp/*.log"}`)
	fl := &persistence.CollectFlow{
		FlowCode: "f-script", MetricCode: metric.MetricCode, CollectType: "script",
		CollectConfig: `{"scriptId":` + fmt.Sprint(script.ID) + `,"version":1}`,
		DataSourceID:  &ds.ID,
	}
	_, _, err := collectors.Collect(context.Background(), fl, []*persistence.DataSource{ds}, metric,
		map[string]string{"biz_date": "2026-10-20"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "ssh") {
		t.Fatalf("应提示须绑定 ssh 执行目标, got %v", err)
	}
}

// 脚本采集: 参数合并(内置优先) + 输出按指标过滤
func TestCollectScriptRunsRemotelyAndFiltersMetric(t *testing.T) {
	collectors, script, metric, ds := newScriptCollectHarness(t, "ssh",
		`{"type":"ssh","host":"10.0.0.9","port":22,"username":"cap","os":"linux","tempDir":"/tmp"}`)
	fl := &persistence.CollectFlow{
		FlowCode: "f-script", MetricCode: metric.MetricCode, CollectType: "script",
		CollectConfig: `{"scriptId":` + fmt.Sprint(script.ID) + `,"version":1,"params":{"mount":"/data","biz_date":"1999-01-01"}}`,
		DataSourceID:  &ds.ID,
	}
	var uploadedParams string
	collectors.remote = &RemoteRunner{
		NewTransport: func(_ context.Context, tgt RemoteTarget) (RemoteTransport, error) {
			if tgt.Host != "10.0.0.9" || tgt.OS != "linux" || tgt.TempDir != "/tmp" {
				t.Fatalf("target: %+v", tgt)
			}
			return &fakeRemote{
				uploaded: map[string]string{},
				stdout: "start\n" +
					`[{"metric":"DISK_USAGE_PCT","value":88.5,"unit":"%"},{"metric":"OTHER","value":1,"unit":"x"}]` + "\n",
			}, nil
		},
	}
	// 记录上传的 params.json, 校验参数合并结果
	orig := collectors.remote.NewTransport
	collectors.remote.NewTransport = func(ctx context.Context, tgt RemoteTarget) (RemoteTransport, error) {
		tr, err := orig(ctx, tgt)
		if err != nil {
			return nil, err
		}
		return &captureTransport{RemoteTransport: tr, onUpload: func(path, content string) {
			if strings.HasSuffix(path, "params.json") {
				uploadedParams = content
			}
		}}, nil
	}

	vars := map[string]string{"biz_date": "2026-10-20", "system_code": "S1"}
	rows, meta, err := collectors.Collect(context.Background(), fl, []*persistence.DataSource{ds}, metric, vars, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Raw != "88.5" || rows[0].Unit != "%" {
		t.Fatalf("rows: %+v", rows)
	}
	if meta == nil || meta.RowsTotal != 2 {
		t.Fatalf("meta: %+v", meta)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(uploadedParams), &got); err != nil {
		t.Fatalf("params.json 非法: %q", uploadedParams)
	}
	if got["mount"] != "/data" {
		t.Fatalf("自定义参数未下发: %+v", got)
	}
	if got["biz_date"] != "2026-10-20" {
		t.Fatalf("内置变量被流程参数覆盖: %+v", got)
	}
	if got["system_code"] != "S1" {
		t.Fatalf("内置变量丢失: %+v", got)
	}
}

// captureTransport 包装 transport, 便于拦截上传内容
type captureTransport struct {
	RemoteTransport
	onUpload func(path, content string)
}

// 流程保存校验: script 采集须绑 ssh 执行目标; python(本地沙箱) 不允许多语言脚本
func TestValidateScriptFlowBindingAndLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.CapacityMetric{}, &persistence.DataSource{},
		&persistence.PythonScript{}, &persistence.CollectFlow{})
	hh := &Handler{db: h.DB}

	if err := h.DB.Create(&persistence.CapacityMetric{
		MetricCode: "M1", Name: "m1", SystemCode: "S1", MetricType: "usage", Unit: "%", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	sshDS := persistence.DataSource{Name: "t1", Type: "ssh", Status: "available", Config: "{}", SystemCode: "S1"}
	logDS := persistence.DataSource{Name: "l1", Type: "log", Status: "available", Config: "{}", SystemCode: "S1"}
	for _, ds := range []*persistence.DataSource{&sshDS, &logDS} {
		if err := h.DB.Create(ds).Error; err != nil {
			t.Fatal(err)
		}
	}
	bashScript := persistence.PythonScript{Name: "b1", ScriptType: "collect", Language: "bash"}
	if err := h.DB.Create(&bashScript).Error; err != nil {
		t.Fatal(err)
	}

	req := func(dsID *int64, collectType, cfg string) *flowReq {
		return &flowReq{
			FlowCode: "f1", Name: "f1", MetricCode: "M1", DataSourceID: dsID,
			CollectType: collectType, CollectConfig: json.RawMessage(cfg),
			ScheduleMode: "normal", CronExpr: "0 * * * *",
		}
	}
	sshCfg := fmt.Sprintf(`{"scriptId":%d,"version":1}`, bashScript.ID)

	if err := hh.validate(req(&logDS.ID, "script", sshCfg)); err == nil || !strings.Contains(err.Error(), "执行目标") {
		t.Fatalf("script 绑 log 数据源应被拒: %v", err)
	}
	if err := hh.validate(req(&sshDS.ID, "script", sshCfg)); err != nil {
		t.Fatalf("script 绑 ssh 数据源应通过: %v", err)
	}
	// 本地沙箱只跑 python: 引用 bash 脚本应被拒
	if err := hh.validate(req(&sshDS.ID, "python", sshCfg)); err == nil || !strings.Contains(err.Error(), "python") {
		t.Fatalf("python 采集引用 bash 脚本应被拒: %v", err)
	}
}

func (c *captureTransport) UploadFile(ctx context.Context, remotePath, content string) error {
	if c.onUpload != nil {
		c.onUpload(remotePath, content)
	}
	return c.RemoteTransport.UploadFile(ctx, remotePath, content)
}

// python 采集在平台沙箱执行, 不读数据源 → 未绑数据源时不得提前报"未关联可用数据源";
// 其他采集方式仍然必须绑定数据源。
func TestCollectPythonWithoutDataSourceSkipsDSRequirement(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.PythonScript{}, &persistence.PythonScriptVersion{})
	c := &Collectors{db: h.DB}

	flowOf := func(collectType string) *persistence.CollectFlow {
		return &persistence.CollectFlow{
			ID: 1, FlowCode: "f1", MetricCode: "M1",
			CollectType: collectType, CollectConfig: `{"scriptId":0}`,
		}
	}
	metric := &persistence.CapacityMetric{MetricCode: "M1"}

	_, _, err := c.Collect(context.Background(), flowOf("python"), nil, metric, map[string]string{}, time.Now())
	if err == nil || strings.Contains(err.Error(), "未关联可用数据源") {
		t.Fatalf("python 采集不应因未绑数据源失败: %v", err)
	}
	_, _, err = c.Collect(context.Background(), flowOf("sql"), nil, metric, map[string]string{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "未关联可用数据源") {
		t.Fatalf("sql 采集未绑数据源应被拒: %v", err)
	}
}
