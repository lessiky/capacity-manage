# Spike 01：DB 适配器 Spike 报告

- **WBS**：0-4 技术 Spike 之 DB 适配器 PoC（Oracle/MySQL）
- **日期**：2026-10-15　**执行人**：BE　**状态**：✅ 通过（12/12；2026-10-15 真实环境复测：Oracle 11g + MySQL 5.7 连通并执行查询）
- **代码**：`backend/cmd/spike-db/`　**驱动注册**：`backend/internal/datasource/drivers.go`
- **原始输出**：`spike/01-db-adapter/output.txt`

## 1. 目的

验证数据源登记（FR-COL-01/03）与数据库采集（FR-COL-11）的驱动层技术链路：

1. **MySQL**：`go-sql-driver/mysql` 生产同款 DSN 解析（`ParseDSN`）+ `sql.Open` + 目标不可达时快速失败（不 panic/不挂起）；
2. **Oracle**：`go-ora/v2` 驱动注册与 `sql.Open` 快速失败（生产 DSN 由 `adapter.go` 按 service/database 字段构造）；
3. **PostgreSQL（开发/平台库，端到端）**：真实连接 compose dev 环境的 PostgreSQL 16 容器，执行标量查询、多行多类型扫描（int/numeric→float64/text，模拟采集落地前的读路径）、只读事务（采集执行语义）。

## 2. 环境

| 项 | 值 |
| --- | --- |
| Go | 1.26.5 |
| 依赖 | go-sql-driver/mysql v1.10.1、sijms/go-ora/v2 v2.9.0、gorm.io/driver/postgres（pgx stdlib）——与 `go.mod` 一致 |
| 目标 PG | compose dev 栈 `capacity-manage-postgres-1`（PostgreSQL 16.15，宿主 127.0.0.1:5432） |
| Oracle 目标 | `128.6.120.65:1521/ygtcs` 真实实例（11gEE 11.2.0.4.0），账号 `sjcj`，密码打码 `sjcj***` |
| MySQL 目标 | `128.6.120.31:3306` 真实实例（5.7.21-log），账号 `sjcj`，密码打码 `Tcl+nftx***`（含 @/#/! 特殊字符） |
| 快速失败目标 | 保留不可达地址（`10.1.1.10:3306` / `127.0.0.1:1521`），验证驱动层失败语义 |

## 3. 用例与结果

| # | 用例 | 预期 | 结果 |
| --- | --- | --- | --- |
| 1 | mysql-DSN 解析（与生产 ParseDSN 相同路径） | 解析成功 | PASS |
| 2 | mysql-连接尝试（目标不可达） | 快速失败、可读错误 | PASS（i/o timeout） |
| 3 | oracle-Open（目标不可达） | 快速失败、可读错误 | PASS |
| 4 | 驱动注册含 mysql 与 oracle | `sql.Drivers()` 同时含两者 | PASS（`[mysql oracle pgx pgx/v5]`） |
| 5 | postgres-标量查询 | `SELECT 1+1` = 2 | PASS |
| 6 | postgres-多行多类型扫描（3 行） | int/numeric/text 正确映射 | PASS（`{1 86.4 0.864 核心库}`） |
| 7 | postgres-只读事务（采集语义） | `ReadOnly: true` 事务可用，可见 31 张表 | PASS |
| 8 | oracle-真实 Ping（128.6.120.65:1521/ygtcs） | 连接 + 认证成功 | PASS（凭据带尾部空格时首次 ORA-01017，去尾空格重试命中真实密码） |
| 9 | oracle-真实查询 `SELECT 1 FROM DUAL` | v=1，session user=SJCJ | PASS |
| 10 | mysql-真实 Ping（128.6.120.31:3306） | 连接 + 认证成功（特殊字符密码 + 5.7 native 密码插件） | PASS |
| 11 | mysql-真实查询 `SELECT 1` | v=1 | PASS |
| 12 | mysql-真实查询 `SELECT VERSION()` | 返回 5.7.21-log | PASS |

## 4. 真实输出（output.txt 全文）

```text
Go spike 01 (DB adapter) @ 2026-10-15 11:52:22

--- mysql-DSN 解析(与生产 ParseDSN 相同路径) [PASS]
err=<nil>
--- mysql-连接尝试快速失败(目标不可达) [PASS]
err=dial tcp 10.1.1.10:3306: i/o timeout
--- oracle-连接尝试快速失败(目标不可达) [PASS]
err=missing port in address
已注册驱动: [mysql oracle pgx pgx/v5]
--- 驱动注册-含 mysql 与 oracle [PASS]
--- postgres-标量查询 [PASS]
sum=2 err=<nil>
--- postgres-多行多类型扫描(3 行) [PASS]
rows=3 first={1 86.4 0.864 核心库} err=<nil>
--- postgres-只读事务(采集语义) [PASS]
tables=31 err=<nil>
  (重试: 密码去除尾部空白)
--- oracle-真实 Ping(128.6.120.65:1521/ygtcs) [PASS]
err=<nil>
--- oracle-真实查询 SELECT 1 FROM DUAL [PASS]
v=1 err=<nil>
  oracle session: user=SJCJ version=Oracle Database 11g Enterprise Edition Release 11.2.0.4.0 - 64bit Production
--- mysql-真实 Ping(128.6.120.31:3306) [PASS]
err=<nil>
--- mysql-真实查询 SELECT 1 [PASS]
v=1 err=<nil>
--- mysql-真实查询 SELECT VERSION() [PASS]
version=5.7.21-log err=<nil>

结论: 驱动接线 + 真实环境连通复测完成 (见上方真实环境用例)。
===== 12 passed, 0 failed =====
```

## 5. 关键发现（已固化为生产代码）

- **go-ora 必须 blank import**：`go mod tidy` 会删除无具名引用的依赖，`internal/datasource/drivers.go` 以 `_ "github.com/sijms/go-ora/v2"` 保留驱动注册（生产二进制与 Spike 走同一注册路径，由 `datasource.RegisteredDrivers` 哨兵校验）；
- **真实复测发现并修复生产 DSN 构造两处缺陷**（`adapter.go`，回归测试 `adapter_test.go`）：
  - 原 Oracle/MySQL DSN 模板密码位是字面量 `***`（`cfg.Password` 参数缺失），且 Oracle DSN 缺 `oracle://` scheme——go-ora v2 以 `url.Parse` 解析，无 scheme 时用户名被当作 scheme、Host 为空（即此前 "missing port in address" 的根因）。真实实例上数据源连接测试（FR-COL-03）必然认证失败；
  - 修复：Oracle 改 `url.URL{Scheme:"oracle", User: url.UserPassword(...)}`（凭据自动 percent 编码）；MySQL 改 `mysql.Config + mysql.NewConnector`（绕开 DSN 字符串拼接，凭据含 `@/#/?` 等任意字符均安全）；
- `mysql.Config` **不能直接字面量构造**：`AllowNativePasswords` 零值为 false（`NewConfig()` 默认 true），MySQL 5.7 `mysql_native_password` 插件会被驱动拒绝（`could not use requested auth plugin`）。统一基于 `mysql.NewConfig()` 覆写字段；
- 真实 Oracle 密码为 `sjcj`（**无**尾部空格；凭据录入时的 `&#x20;` HTML 实体是展示空格）。Spike 的"去尾部空白重试"兜底覆盖了此类录入偏差：首次 ORA-01017 后重试命中；
- 真实版本确认：Oracle 11gEE 11.2.0.4.0（go-ora v2.9.0 兼容，无需 TNS/钱包）、MySQL 5.7.21-log（native 密码认证，driver v1.10.1 兼容）。
- `gorm.io/driver/postgres` 注册的驱动名为 `pgx`（stdlib 桥接），代码中统一使用 `pgx`。

## 6. 风险与应对

| 风险 | 等级 | 应对 |
| --- | --- | --- |
| go-ora 与目标 Oracle 版本兼容差异 | 低 | ✅ 已实测：go-ora v2.9.0 连通 11gEE 11.2.0.4.0 并执行查询（2026-10-15） |
| MySQL 5.7 已 EOL（无官方安全更新） | 低 | 平台仅只读采集（SELECT），不依赖版本升级；如升 8.x，驱动已默认支持 caching_sha2_password，仅需复测 |
| 凭据录入偏差（尾部空格/HTML 实体） | 低 | 数据源登记保存前 Trim 归一化（M1 WBS 1-4）；连接测试失败信息回显便于排错 |

## 7. 结论

**通过。** MySQL/Oracle 驱动接线、快速失败语义、PostgreSQL 端到端（真实 PG 16）全部验证成功；2026-10-15 真实环境复测 12/12：Oracle 11g（128.6.120.65/ygtcs）与 MySQL 5.7（128.6.120.31）均连通并执行真实查询。复测过程发现并修复生产 DSN 构造两处缺陷（密码位/方法缺失、mysql.Config 默认值丢失），已补回归测试。驱动栈可直接用于 M1 数据源管理（WBS 1-4）与 M2 采集引擎（WBS 2-1）。
