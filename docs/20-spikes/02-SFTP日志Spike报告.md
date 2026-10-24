# Spike 02：SFTP 日志采集 Spike 报告

- **WBS**：0-4 技术 Spike 之 SFTP 日志解析 PoC
- **日期**：2026-10-14　**执行人**：BE　**状态**：✅ 通过（11/11；2026-10-15 真实 SFTP 环境复测通过）
- **代码**：`backend/cmd/spike-sftp/`（进程内 SFTP 服务器 + 生产同款客户端栈 + `internal/logparse` 正则演示）
- **原始输出**：`spike/02-sftp-log/output.txt`

## 1. 目的

验证日志类数据源（FR-COL-01/12）的完整技术链路在 Windows 开发环境与 Linux 容器目标环境下均可实现：

1. Go 客户端通过 SFTP 连接远程服务器（SSH 握手、SFTP 会话）；
2. 列目录、按 glob 命名规则（支持交易日变量形态的文件名，如 `ora-20261014.log`）下载文件；
3. `internal/logparse` 正则提取容量值（value/unit），非容量行跳过；
4. 认证失败时返回可读错误（不 panic/不挂起），供"连接测试"（FR-COL-03）与"数据源状态"（FR-COL-04）使用。

## 2. 环境

| 项 | 值 |
| --- | --- |
| OS | Windows 11（生产目标：Linux 容器） |
| Go | 1.26.5 |
| 依赖 | golang.org/x/crypto/ssh v0.58、github.com/pkg/sftp v1.13（与 `go.mod` 一致） |
| 模拟服务端 | 进程内 ssh.ServerConn + sftp.NewServer（端口随机），文件放 `C:\logs\coredb` |
| 真实服务端 | `128.6.120.41:22`（OpenSSH），账号 `aboss`，密码打码 `aboss***`，日志根目录 `/data/aboss/log`（上一交易日目录 `20261014`） |

## 3. 用例与结果

| # | 用例 | 预期 | 结果 |
| --- | --- | --- | --- |
| 1 | SFTP 根目录准备 | 生成两个交易日命名的日志文件 | PASS |
| 2 | 进程内 SFTP 服务器启动 | 监听随机端口 | PASS |
| 3 | SSH 握手 + SFTP 会话建立 | 无错误 | PASS |
| 4 | 列目录 | 返回 2 个文件 | PASS |
| 5 | Glob + 文件下载 + Stat | 命中 2 文件，下载 335 字节 | PASS |
| 6 | logparse 正则提取 value/unit | 4 行命中、1 行非容量行跳过 | PASS |
| 7 | 错误密码拒绝 | 返回"认证失败"可读错误 | PASS |
| 8 | sftp-真实 SSH 握手 + SFTP 会话（128.6.120.41:22） | 无错误 | PASS |
| 9 | sftp-真实列目录 `/data/aboss/log` | 返回交易日子目录（含 `20261014`） | PASS |
| 10 | sftp-真实日期目录列文件 + Glob(`*run*20261014.txt`) | 103 文件，glob 命中（`a5_of_aip_41_run_20261014.txt` 等） | PASS |
| 11 | sftp-真实样例文件下载 + 读取 | 完整读回 1552 字节 | PASS |

## 4. 真实输出（output.txt 全文）

```text
Go spike 02 (SFTP log) @ 2026-10-15 11:52:46

--- 准备-SFTP 根目录 [PASS]
dir=C:\logs\coredb (测试后清理)
--- SFTP-进程内服务器启动 [PASS]
addr=127.0.0.1:18795 err=<nil>
--- SFTP-SSH 握手 + SFTP 会话建立 [PASS]
err=<nil>
--- SFTP-列目录 [PASS]
entries=[ora-20261013.log ora-20261014.log] err=<nil>
--- SFTP-Glob + 文件下载 + Stat [PASS]
matches=[/logs/coredb/ora-20261013.log /logs/coredb/ora-20261014.log] bytes=335 err=<nil>
解析结果:
  2026-10-14 09:30:05 [INFO] disk /u01/oradata usage=86.4 unit=percent -> value=86.4 unit=percent
  2026-10-14 09:30:05 [INFO] disk /u02/archive usage=63.2 unit=percent -> value=63.2 unit=percent
  2026-10-14 09:30:06 [WARN] disk /u01/fastlog usage=91.8 unit=percent -> value=91.8 unit=percent
  (跳过) 2026-10-14 09:31:00 [INFO] checkpoint complete elapsed=42s
  2026-10-14 09:35:12 [INFO] disk /u01/oradata usage=86.4 unit=percent -> value=86.4 unit=percent
--- logparse-regex 提取 value/unit (4 行命中, 1 行非容量行跳过) [PASS]
parsed=4 skipped=1
--- SFTP-错误密码拒绝 [PASS]
err=SFTP 认证失败: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain
--- sftp-真实 SSH 握手 + SFTP 会话 [PASS]
err=<nil>
--- sftp-真实列目录/data/aboss/log [PASS]
dirs(前 10)=[20261011 20261014_stack 20261010 20261008 20261013 20261015 20261008_stack 20261007 20261009_stack 20261012_stack] err=<nil>
--- sftp-真实日期目录列文件 + Glob(*run*20261014.txt) [PASS]
files=103 matches=前10:[/data/aboss/log/20261014/a5_of_aip_41_run_20261014.txt /data/aboss/log/20261014/a5_cs_so_mc_smsend_dhzq41_run_20261014.txt /data/aboss/log/20261014/a5_rs_zx_41_report_run_20261014.txt /data/aboss/log/20261014/a5_fos_zx_41_run_20261014.txt /data/aboss/log/20261014/a5_dc_fos_zx_41_run_20261014.txt /data/aboss/log/20261014/a5_sc_mq_sys_zx_41_run_20261014.txt /data/aboss/log/20261014/a5_dc_fos_zx_41_report_run_20261014.txt /data/aboss/log/20261014/a5_sc_rs_local_zx_41_run_20261014.txt /data/aboss/log/20261014/a5_ss_fos_zx_41_run_20261014.txt /data/aboss/log/20261014/a5_of_jjhqdr_41_run_20261014.txt] err=<nil> gerr=<nil>
  样例文件: /data/aboss/log/20261014/a5_of_aip_41_run_20261014.txt size=1552 bytes
  原文前 15 行:
  | 20261014 20:28:38.177 ҵ��ͳ�� a5glht 20261013 19:50:17.394 - 20261014 20:28:38.177 (��λ:����).
  | 20261014 20:28:38.185 ���ܺ�,����,����ʱ,����,����,ƽ��,����
  | 20261014 20:28:38.200 0�����񱻵���.
  | 20261014 20:28:38.211 write thread pool proc state.
  | 20261014 20:28:38.215 Proc01 state=0, proc=0, rcv=0, func=, thrid=1704, last_calltime(tick)=.
  | 20261014 20:28:38.220 Proc02 state=0, proc=0, rcv=0, func=, thrid=1705, last_calltime(tick)=.
  | 20261014 20:28:38.225 Proc03 state=0, proc=0, rcv=0, func=, thrid=1706, last_calltime(tick)=.
  | 20261014 20:28:38.229 Proc04 state=0, proc=0, rcv=0, func=, thrid=1707, last_calltime(tick)=.
  | 20261014 20:28:38.233 Proc05 state=0, proc=0, rcv=0, func=, thrid=1708, last_calltime(tick)=.
  | 20261014 20:28:38.272 Proc06 state=0, proc=0, rcv=0, func=, thrid=1709, last_calltime(tick)=.
  | 20261014 20:28:38.277 Proc07 state=0, proc=0, rcv=0, func=, thrid=1710, last_calltime(tick)=.
  | 20261014 20:28:38.281 Proc08 state=0, proc=0, rcv=0, func=, thrid=1711, last_calltime(tick)=.
  | 20261014 20:28:38.286 Proc09 state=0, proc=0, rcv=0, func=, thrid=1712, last_calltime(tick)=.
  | 20261014 20:28:38.290 Proc010 state=0, proc=0, rcv=0, func=, thrid=1713, last_calltime(tick)=.
  | 20261014 20:28:38.294 write thread pool proc state end.
  | ...
--- sftp-真实样例文件下载 + 读取 [PASS]
readBytes=1552 size=1552 err=<nil>

===== 11 passed, 0 failed =====
```

## 5. 关键发现（已固化为生产代码经验）

- `x/crypto/ssh` v0.58 的 `NewChannel` 回调需 `Accept()` + `ChannelType()` 显式处理；
- SSH subsystem 请求（`subsystem "sftp"`）必须显式 `req.Reply(true, nil)`，否则默认 DiscardRequests 会回 NO 导致客户端挂起；
- `pkg/sftp` 服务端 `sftp.NewServer(ch, opts...)` 传入的是 session Channel；`WithServerWorkingDirectory` 只对相对路径生效（生产客户端统一使用绝对 POSIX 路径规避）；
- Windows 下 POSIX 路径映射到当前盘根（`/logs/coredb` → `C:\logs\coredb`），Linux 下为原生绝对路径，客户端代码无需区分。
- **真实环境形态确认**（2026-10-15 复测）：`/data/aboss/log` 按交易日目录组织（`20261007`~`20261015`，另有 `{date}_stack` 目录并存）；`20261014` 目录含 103 个文件，命名 `{服务名}_run_{交易日}.txt`（如 `a5_of_aip_41_run_20261014.txt`、`a5_dc_fos_zx_41_report_run_20261014.txt`），与需求给定的 `{服务名}_*run_*{上一交易日}.txt` 形态一致，glob 模板 `*run*{date}.txt` 命中；
- **真实日志为 GBK 编码**：中文行在 UTF-8 环境下呈乱码（见上方输出 `20261014 20:28:38.177` 行），M1 解析管线须加 GBK/GB18030 解码层（`golang.org/x/text/encoding/simplifiedchinese`），先解码再进 logparse 正则；
- 样例日志内容为线程池状态快照（`Proc01 state=0, proc=0, rcv=0, ...`）+ 业务耗时统计，**容量指标行的具体格式需与服务属主确认后配置解析模板**（当前 logparse 模板按设计文档示例格式，正则可配置化已具备）；
- 客户端栈（`x/crypto/ssh` + `pkg/sftp`，PoC 用 InsecureIgnoreHostKey）在真实 OpenSSH 服务器上手握/列目录/glob/下载全部正常；生产需补 host key 校验（M1 部署层）。

## 6. 风险与应对

| 风险 | 等级 | 应对 |
| --- | --- | --- |
| 真实 SFTP 服务端兼容性 | 低 | ✅ 已实测：真实 OpenSSH 服务器（128.6.120.41）握手/列目录/glob/下载全部通过（2026-10-15）；如接入国产堡垒机需补 host key 白名单 |
| 真实日志 GBK 编码（与 UTF-8 解析管线不匹配） | 中 | M1 解析管线加入 GB18030 解码层（先解码后正则）；编码探测失败时按 FR-COL-12 失败率监控告警并跳过 |
| 大文件下载性能 | 低 | 试点日志文件为 KB~MB 级；如超预期再引入分块/并发 |
| 日志格式变更导致解析失败（计划风险 9） | 中 | 解析规则版本化 + 失败率监控（FR-COL-12），格式变更走变更流程 |

## 7. 结论

**通过。** SFTP 客户端栈 + glob 命名 + 正则解析全链路验证成功；2026-10-15 真实环境复测 11/11：真实 OpenSSH 服务器连通、交易日目录定位、`*run*{date}.txt` glob 命中（103 文件）、样例文件完整读回（1552 字节）。新发现：真实日志 GBK 编码（M1 需解码层）、容量指标行格式待与服务属主确认。可直接用于 M2 采集引擎（WBS 2-1 日志解析）开发。
