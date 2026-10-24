# Spike 03：Python 沙箱 Spike 报告

- **WBS**：0-4 技术 Spike 之 Python 沙箱 PoC
- **日期**：2026-10-14　**执行人**：BE　**状态**：✅ 通过（9/9）
- **代码**：`spike/03-python-sandbox/run_spike.py`（复用生产代码 `backend/worker/sandbox.py`，9 个独立 case 目录）
- **原始输出**：`spike/03-python-sandbox/output.txt`

## 1. 目的

验证 Python 脚本采集/清洗（FR-COL-17/18/19）的执行沙箱满足安全基线：

1. 模块白名单：禁止 `socket`/`subprocess`/`requests` 等网络与进程模块导入；
2. 文件系统写限制：仅允许写入脚本工作目录，越界写抛 `PermissionError`；
3. 超时强杀：超时（本 spike 8s 预算）后子进程被强制终止（SIGKILL），不影响 Worker 主进程；
4. 正常脚本（计算/写工作目录）成功执行并回收 stdout JSON。

## 2. 环境

| 项 | 值 |
| --- | --- |
| OS | Windows 11（生产目标：Linux 容器内 python:3.11-slim） |
| Python | 3.11.9 |
| 沙箱实现 | `backend/worker/sandbox.py`（生产代码，非 spike 专用实现） |
| 跑法 | `python -X utf8 spike\03-python-sandbox\run_spike.py` |

## 3. 用例与结果

| # | 用例 | 预期 | 结果 |
| --- | --- | --- | --- |
| 1 | ok_compute：正常计算脚本 | 成功，stdout JSON | PASS（exit=0, 287ms） |
| 2 | ok_write_workdir：写工作目录 | 成功，文件落盘 | PASS（exit=0, 387ms） |
| 3 | block_write_outside：写 `/tmp` | PermissionError 拒绝 | PASS（exit=1, 1137ms） |
| 4 | block_import_socket | ImportError 拒绝 | PASS（exit=1, 534ms） |
| 5 | block_import_subprocess | ImportError 拒绝 | PASS（exit=1, 805ms） |
| 6 | block_import_requests | ImportError 拒绝 | PASS（exit=1, 1023ms） |
| 7 | timeout_infinite_loop：死循环 | 超时强杀 | PASS（exit=-9, 8137ms） |
| 8 | timeout_sleep：长 sleep | 超时强杀 | PASS（exit=-9, 8230ms） |
| 9 | cpu_hot_loop：CPU 忙等 | 超时强杀 | PASS（exit=-9, 8101ms） |

## 4. 真实输出（output.txt 摘要，全文见 `spike/03-python-sandbox/output.txt`）

```text
Python 3.11.9 @ 2026-10-14 22:24:37
sandbox: D:\newwork\capacity-manage\backend\worker\sandbox.py

--- ok_compute [PASS] success exit=0 287ms
stdout: {"ok": true, "sqrt": 1.4142135623730951}
--- ok_write_workdir [PASS] success exit=0 387ms
stdout: {"ok": true, "file": true}
--- block_write_outside [PASS] failed exit=1 1137ms
PermissionError: [sandbox] 禁止工作目录外写入: /tmp/escape_test.txt
--- block_import_socket [PASS] failed exit=1 534ms
ImportError: [sandbox] 模块 socket 在黑名单, 禁止导入
--- block_import_subprocess [PASS] failed exit=1 805ms
ImportError: [sandbox] 模块 subprocess 在黑名单, 禁止导入
--- block_import_requests [PASS] failed exit=1 1023ms
ImportError: [sandbox] 模块 requests 在黑名单, 禁止导入
--- timeout_infinite_loop [PASS] timeout-killed exit=-9 8137ms
--- timeout_sleep [PASS] timeout-killed exit=-9 8230ms
--- cpu_hot_loop [PASS] timeout-killed exit=-9 8101ms

===== 9/9 cases passed =====
```

> 注：Windows 控制台（GBK）下 stderr 中文显示为乱码，为控制台编码问题，日志消息本身正确（生产 Linux 容器统一 UTF-8，无此问题）。`exit=-9` 即 SIGKILL，Windows 下为强制终止子进程，Linux 下语义一致。

## 5. 风险与应对

| 风险 | 等级 | 应对 |
| --- | --- | --- |
| 沙箱为进程级隔离（非 OS 级），理论上可被高权限脚本绕过（如 `ctypes` 调 WinAPI） | 中 | M2 生产容器叠加 `--security-opt no-new-privileges` + cgroup 内存/CPU 限制；`ctypes`/`os.system` 等入口纳入黑名单复查清单；安全测试（M5）专项验证 |
| 超时预算与单流程 5 分钟上限（FR-COL-33）的关系 | 低 | 沙箱超时独立配置（默认值纳入 FR-ADM-06 系统参数子集），与调度超时联动测试在 M2 覆盖 |
| Windows 与 Linux 行为差异（信号/文件 API） | 低 | 用例集随仓库进 M2 worker 单测，Linux CI 中复跑 |

## 6. 结论

**通过。** 生产 `sandbox.py` 在超时强杀、模块白名单、写限制三类安全约束下全部生效且不影响主进程，满足 FR-COL-19 脚本管理与安全的 MVP 要求；OS 级加固项已列入 M2/M5 计划。
