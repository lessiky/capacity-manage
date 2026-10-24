# 远程脚本采集（SFTP 临时目录执行）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让采集流程能把参数与脚本经 SFTP 投送到目标机临时目录执行，执行后删除，脚本语言支持 python / bash / powershell，Linux 与 Windows 同一套规则。

**Architecture:** 新增数据源类型 `ssh`（执行目标，复用既有凭据加密与健康巡检）；新增采集方式 `script`（远程脚本采集），流程绑定 `ssh` 数据源后由 Go 侧 `x/crypto/ssh` + `pkg/sftp` 直连目标机：上传 `<临时目录>/script.<ext>` 与 `params.json` → 按 OS+语言拼命令执行 → 读 stdout 最后一行 JSON → **无论如何都删除临时目录**。既有 `collectType=python`（本地沙箱）保持原样，仅允许 python 脚本。

**Tech Stack:** Go（gin + gorm）、`golang.org/x/crypto/ssh`、`github.com/pkg/sftp`、Vue 3 + Element Plus、PostgreSQL 迁移。

**Spec:** 本计划即设计说明（用户 2026-10-20 指令：Windows/Linux 同规则、支持入参、支持 python/bash/powershell、SFTP 临时目录执行后删除）。

## Global Constraints

- 凭据一律 AES-256-GCM 加密入库，任何日志/接口回显不得出现明文（沿用 `datasource.Registry`）。
- 脚本出参约定不变：stdout **最后一行** JSON，collect 形如 `[{"metric","value","unit"}]`。
- 入参：`params.json` 与脚本同目录落盘；内置变量（biz_date/prev_trading_day/next_trading_day/hour/system_code/flow_code/metric_code）与流程自定义参数合并，**同名以内置为准**。
- 临时目录必须在执行结束后删除（成功/失败/超时三条路径都要删）。
- 目标机不落永久文件；脚本与参数只存在于本次任务临时目录内。
- 既有 `collectType=python` 行为不得改变（本地沙箱、仅 python）。

---

### Task 1: 脚本语言字段（PythonScript.Language）

**Files:**
- Modify: `backend/internal/persistence/models.go`（`PythonScript` 增加 `Language`）
- Create: `db/migrations/0015_script_language.sql`
- Modify: `backend/internal/flow/handler.go`（`CreateScript` 校验 `oneof=python bash powershell`，空值补 `python`）
- Test: `backend/internal/flow/handler_test.go`

**Interfaces:**
- Produces: `persistence.PythonScript.Language string`（`json:"language"`，默认 `python`）。

- [x] **Step 1: 写失败测试** `TestCreateScriptLanguageValidation`：`language=bash` 建脚本成功且回读为 `bash`；`language=perl` 返回 `CodeBadRequest`；不传 language 默认 `python`。
- [x] **Step 2: 运行并确认失败** `go test ./internal/flow/ -run TestCreateScriptLanguageValidation`
- [x] **Step 3: 最小实现** 加字段 + 迁移 + handler 校验。
- [x] **Step 4: 通过** `go test ./internal/flow/ -run TestCreateScriptLanguage`

### Task 2: ssh 执行目标数据源

**Files:**
- Modify: `backend/internal/datasource/adapter.go`（`Config` 增加 `OS`、`TempDir`）
- Modify: `backend/internal/datasource/handler.go`（`dsReq.Type` 允许 `ssh`；`normalizeConfig` 增 `ssh` 分支：host/username 必填、port 默认 22、os 默认 linux、tempDir 默认 `/tmp`（windows 默认 `C:/Windows/Temp`）、密码加密）
- Modify: `backend/internal/datasource/sftp.go`（抽出可复用的 `dialSSH`，返回 `*ssh.Client`，供 SFTP 与 exec 共用）
- Test: `backend/internal/datasource/handler_test.go`

**Interfaces:**
- Produces: `Config.OS string`（`json:"os"`，`linux|windows`）、`Config.TempDir string`（`json:"tempDir"`）。
- Produces: `dialSSH(ctx, cfg) (*gssh.Client, error)`；`dialSFTP` 改为基于它实现，行为不变。

- [x] **Step 1: 写失败测试** `TestNormalizeConfigSSHDefaults`：`type=ssh` 缺 host 报错；给 host/username/os=windows 后 port=22、tempDir=`C:/Windows/Temp`、密码为密文；`os=linux` 时 tempDir=`/tmp`。
- [x] **Step 2: 运行并确认失败** `go test ./internal/datasource/ -run TestNormalizeConfigSSH`
- [x] **Step 3: 最小实现**
- [x] **Step 4: 通过** `go test ./internal/datasource/`

> 补充（实现中发现并修复的两个真实缺陷）：
> 1. `DecryptForCollect` 原先只解密 `db` 与 `log+sftp`，ssh 目标会用密文当密码 → 已补 `ssh` 分支（`TestDecryptForCollectSSH`）；
> 2. `Registry.Test` 无 `ssh` 分支，新建执行目标会被判"未知数据源类型" → 已补 `sshAdapter.Test`：认证 + 在临时目录建目录再删除验证可写（`TestSSHProbeCommand`/`TestRegistryTestDispatchesSSH`/真实 sshd 集成测试）。

### Task 3: 远程脚本执行器（核心）

**Files:**
- Create: `backend/internal/flow/scriptexec.go`
- Test: `backend/internal/flow/scriptexec_test.go`

**Interfaces:**
- Produces: `ScriptSpec{Language, Content string}`、`RemoteTarget{OS, TempDir, Host string, Port int, Username, Password string}`
- Produces 纯函数（先测后写）：
  - `ScriptFileName(lang string) string` → `.py` / `.sh` / `.ps1`
  - `RemoteTempDir(os, base, taskID string) string` → `<base>/capcollect-<taskID>`
  - `BuildRemoteCommand(os, lang, scriptPath string) (string, error)`
  - `MergeScriptParams(builtin, user map[string]string) map[string]string`
  - `LastJSONLine(out string) (string, error)`
- Produces 编排函数：`RunRemoteScript(ctx, t RemoteTarget, spec ScriptSpec, params map[string]string, timeout time.Duration) (*ScriptRunResult, error)`，`ScriptRunResult{Status, Data, Stdout, Stderr, ExitCode, DurationMS, Dir}`。
- 依赖注入点：`uploadFile` / `runCommand` / `removeDir` 三个函数字段，便于单测替换（真实实现走 sftp/ssh）。

命令矩阵（`BuildRemoteCommand`）：

| OS | python | bash | powershell |
| --- | --- | --- | --- |
| linux | `python3 <path>` | `bash <path>` | `pwsh -NoProfile -NonInteractive -File <path>` |
| windows | `python <path>` | `bash <path>` | `powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File <path>` |

- [x] **Step 1: 写失败测试** `TestScriptFileName`、`TestBuildRemoteCommand`（6 组 + 路径含空格加引号 + 未知语言报错）、`TestMergeScriptParams`（内置优先）、`TestLastJSONLine`（多行日志取最后一行 JSON / 无 JSON 报错 / 空输出报错）。
- [x] **Step 2: 运行并确认失败** `go test ./internal/flow/ -run 'TestScriptFileName|TestBuildRemoteCommand|TestMergeScriptParams|TestLastJSONLine'`
- [x] **Step 3: 最小实现纯函数**
- [x] **Step 4: 通过**
- [x] **Step 5: 写编排测试** `TestRunRemoteScriptUploadsExecutesAndCleans`、`TestRunRemoteScriptCleansOnFailure`、`TestRunRemoteScriptTimeoutAndBadOutput`、`TestRunRemoteScriptWindowsPathForms`（失败/超时/无 JSON 也必须清理）。
- [x] **Step 6: 实现编排（sftp 上传 script + params.json → exec → 解析 → defer 清理）**
- [x] **Step 7: 通过** `go test ./internal/flow/ -run TestRunRemoteScript`

> 实现取舍：注入点由"三个函数字段"改为单个 `RemoteTransport`（`NewTransport` 工厂），一次执行只建一条 SSH 连接，避免"两次上传 + 执行 + 清理"各建一次握手。

### Task 4: 接入采集流程（collectType=script）

**Files:**
- Modify: `backend/internal/flow/collectors.go`（`Collect` 增 `case "script"`；新增 `scriptConfig{ScriptID,Version,Params}` 与 `collectScript`）
- Modify: `backend/internal/flow/handler.go`（流程校验：`script` 必须绑 `ssh` 数据源、脚本语言与执行环境匹配；`python` 流程不得引用非 python 脚本）
- Test: `backend/internal/flow/collectors_test.go`

**Interfaces:**
- Consumes: Task 2 的 `datasource.Config.OS/TempDir`、Task 3 的 `RunRemoteScript`。
- Produces: `collectConfig` 形如 `{"scriptId":12,"version":3,"params":{"mount":"/data"}}`。

- [x] **Step 1: 写失败测试** `TestCollectScriptRequiresRemoteTarget`（绑定 `log` 数据源 → 报错提示须绑定 ssh 执行目标）；`TestCollectScriptRunsRemotelyAndFiltersMetric`（params.json 内容 = 内置 + 自定义且内置优先；出参按指标过滤）。
- [x] **Step 2: 运行并确认失败**
- [x] **Step 3: 实现 `collectScript`**（加载脚本 → 合并参数 → RunRemoteScript → LastJSONLine → 复用既有 metric 过滤与 RunLog）
- [x] **Step 4: 通过** `go test ./internal/flow/`
- [x] **Step 5: 流程校验** `TestValidateScriptFlowBindingAndLanguage`：script 流程必须绑 ssh 数据源；本地 python 流程不得引用 bash/powershell 脚本。

### Task 5: 前端配置

**Files:**
- Modify: `frontend/src/views/Scripts.vue`（脚本语言选择 + 列表展示）
- Modify: `frontend/src/views/Datasources.vue`（`ssh` 类型：host/port/username/password/os/tempDir 表单）
- Modify: `frontend/src/views/Flows.vue`（采集方式加"脚本采集"；脚本/版本/入参键值对编辑；数据源选择过滤为执行目标）

- [x] **Step 1: Scripts.vue** 语言下拉（python/bash/powershell）+ 列表"语言"列 + 按语言提示的编辑器 placeholder
- [x] **Step 2: Datasources.vue** ssh 类型表单（os/host/port/账号/密码/临时目录）与表格展示、列表概要、筛选
- [x] **Step 3: Flows.vue** `collectType=script` 表单分支、入参键值对编辑、数据源按类型过滤（script 只列 ssh，其余排除 ssh）、`collectConfig` 序列化/回填
- [x] **Step 4: 通过** `npx vue-tsc --noEmit`（并验证过 vue-tsc 确实会报错：注入 `number` 类型错误时 exit=2）

### Task 6: 文档与端到端验证

**Files:**
- Modify: `docs/10-design/05-接口契约.md`（远程脚本采集契约：params.json + 命令矩阵 + 清理）
- Modify: `backend/worker/examples/`（新增 `collect_disk_remote.py` 示例：读同目录 params.json）

- [x] **Step 1: 契约文档**补 §1.4 远程脚本执行；同步用户手册 §6/§7 与数据库设计表说明
- [x] **Step 2: 示例脚本** `backend/worker/examples/collect_disk_remote.py`（读同目录 params.json → 磁盘利用率）
- [x] **Step 3: 端到端验证**：`linuxserver/openssh-server` 容器，`TestSSHRunnerRealTarget`（python + bash 两条语言路径，断言临时目录已删除）与 `TestSSHAdapterRealTarget`（数据源"测试连接"探测临时目录可写）均 PASS
- [x] **Step 4: 全量回归** `go build ./...`、`go test -p 1 ./...`、`npx vue-tsc --noEmit` 全绿

## 实现记录（与计划的差异）

| 项 | 计划 | 实际 | 原因 |
| --- | --- | --- | --- |
| 采集方式取值 | 新增 `script` | 同 | 保留 `python`（本地沙箱）不动，向后兼容 |
| 注入点 | 3 个函数字段 | 1 个 `RemoteTransport` 工厂 | 一次执行一条连接 |
| 脚本语言存放 | `PythonScript.Language` | 同（迁移 0015） | `ScriptType` 已被 collect/clean 占用 |
| 附带修复 | 无 | `DecryptForCollect` 补 `ssh`；`Registry.Test` 补 `ssh` 分支 | 否则 ssh 目标密码不解密、新建即被判"未知类型" |

## 收尾补丁

- **清洗脚本语言限制**：`CreateScript` 拒绝 `scriptType=clean` 且 `language!=python`（返回 400，文案"清洗脚本在本地沙箱执行, 仅支持 python"）；前端 `Scripts.vue` 在用途为"清洗"时禁用 Bash/PowerShell 选项并归位为 Python。
- **验证**：`TestCreateScriptLanguageValidation` 先 RED 后 GREEN；`go build ./...`、`go test -p 1 ./...`、`go vet ./internal/flow ./internal/datasource`、`npx vue-tsc --noEmit` 全绿。

## 收尾补丁 2（参考脚本）

- 新增 `backend/worker/examples/collect_disk_linux.sh`（Bash，入参 `mount`，`df -Pk` 计算；`jq` 可选，缺失时退化到 `sed` 解析扁平 JSON）与 `backend/worker/examples/collect_disk_windows.ps1`（PowerShell，入参 `drive`，`Get-CimInstance Win32_LogicalDisk`，退化到 `Get-PSDrive`）。
- **Windows 脚本保持纯 ASCII**：Windows PowerShell 5.1 按系统 ANSI 代码页读取无 BOM 文件，中文注释会乱码、个别字节被当作转义符导致解析失败（实测 `powershell.exe -File` 报 `Unexpected token '}'`）；改为 ASCII 注释后 5.1 解析/执行均通过。
- **验证**：Windows 侧用 `powershell.exe`（5.1）跑通成功/非法盘符(exit 2)/缺 params(回退默认) 三种路径并核对 JSON 数组输出；Linux 侧用 `alpine:3.20` 容器分别验证 sed 回退、jq 路径、缺 params、路径不存在(exit 2)。

## 收尾补丁 3（Bash 版本无法保存的缺陷）

- **缺陷**：`POST /scripts/{id}/versions` 无条件调用沙箱 `/v1/check`（Python 解析器），登记 bash 脚本时被误判为 `语法校验失败: invalid decimal literal (line 48)`，导致非 Python 脚本根本存不了版本。
- **修复**：`CreateVersion` 按 `PythonScript.Language` 分流，仅 `python`（含空值默认）走语法校验；bash/powershell 跳过本地校验（由目标机解释器执行，正确性以 RunLog 为准）。
- **前端**：`Scripts.vue` 按语言区分提示 —— 头部说明拆分为"Python 沙箱 + 校验 / Bash·PowerShell 目标机执行"，新增版本与编辑弹窗的按钮和成功提示按语言切换（非 Python 不再宣称"语法校验通过"）。
- **验证**：新增 `TestCreateVersionSyntaxCheckByLanguage`（假 worker `/v1/check` 一律返回失败）先 RED（复现用户报错）后 GREEN；`go test -p 1 ./internal/flow/ ./internal/datasource/`、`npx vue-tsc --noEmit` 通过。

## 收尾补丁 4（指标名参数化）

- **需求**：指标名要能随参数传入，否则同一脚本无法复用到不同设备的同类指标。
- **做法**：三份参考脚本统一按 `metric` 入参 > 平台内置 `metric_code`（`ResolveVars` 已注入当前流程指标）> 脚本默认值 的优先级决定出参 `metric`；**入参留空即跟随流程指标**。
- **解析健壮性（Linux）**：`get_param` 依次尝试 `jq` → `python3` → `sed`；末位 `sed` 仅适合简单值，`metric` 含转义引号/反斜杠时需目标机有 `jq` 或 `python3`（实测 jq/python3 分支可正确还原 `A"B\C`，sed 会截断）。
- **Windows**：`ConvertTo-Json` 负责转义，无需手工处理；保持纯 ASCII。
- **验证**：容器内跑通 explicit/builtin/default/escape 四类用例（alpine 无 jq/python3 走 sed、python:3.12-alpine 走 python3、alpine+jq 走 jq）；Windows 侧 `powershell.exe` 5.1 同样四类用例全部通过。
