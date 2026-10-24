# 容量管理平台 API 规范（v1）

| 项 | 内容 |
| --- | --- |
| 文档版本 | V1.0 |
| 状态 | M0 设计冻结基线 |
| 基路径 | `/api/v1`（前端经 Nginx 反代；开放 API 二期 `/openapi/v1`，API Key 认证） |

## 1. 通用约定

- 协议：HTTPS；`Content-Type: application/json; charset=utf-8`；
- 认证：`Authorization: Bearer <JWT>`（access token 2h，会话超时由 Redis 会话表控制，默认 30 分钟）；
- 幂等：写操作支持 `Idempotency-Key` 头（可选，防重复提交）；
- 时间：请求参数 `yyyy-MM-dd`（日期）/ `yyyy-MM-ddTHH:mm:ss`（时间，东八区不带 Z）；响应统一 `timestamptz` ISO8601 带时区；
- 命名：资源名词复数、kebab-case 路径；查询参数 camelCase 与蛇形均可，统一按表中示例。

### 1.1 响应包裹

```json
{ "code": 0, "message": "ok", "data": { }, "traceId": "a1b2c3" }
```

- `code=0` 成功；非 0 为业务错误码（HTTP 状态码同步映射：4xx/5xx）；
- 列表响应：`data: { items: [...], total: 100, page: 1, pageSize: 20 }`；
- 导出类接口：`Content-Type` 文件流 + `Content-Disposition: attachment; filename*=UTF-8''xxx.xlsx`，不走包裹。

### 1.2 错误码表

| code | HTTP | 说明 |
| --- | --- | --- |
| 0 | 200 | 成功 |
| 40001 | 400 | 参数错误（message 含字段级明细） |
| 40100 | 401 | 未认证/令牌过期 |
| 40101 | 401 | 账号已锁定 |
| 40300 | 403 | 无功能权限（Casbin） |
| 40301 | 403 | 无数据权限（业务系统范围外） |
| 40400 | 404 | 资源不存在 |
| 40900 | 409 | 冲突（唯一键/状态机非法迁移） |
| 42200 | 422 | 业务校验失败（如阈值不合理） |
| 42900 | 429 | 请求过于频繁 |
| 50000 | 500 | 服务内部错误 |
| 50300 | 503 | 依赖不可用（DB/Redis/Worker/LLM，message 含依赖名） |
| 50400 | 504 | 执行超时（采集/导出/LLM） |

## 2. 认证与会话（auth）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | /auth/login | 登录 `{username,password}` → `{accessToken,expiresIn,userInfo}`；连续 5 次失败锁定 15 分钟 | 公开 |
| POST | /auth/logout | 注销（删除会话） | 登录 |
| GET | /auth/me | 当前用户信息（含角色、功能权限码、数据权限系统集合） | 登录 |
| POST | /auth/password | 修改密码 `{oldPassword,newPassword}`；首次登录强制（mustChangePwd=true 时仅允许该接口与 /me） | 登录 |

## 3. 系统管理（admin）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET/POST | /admin/users | 用户列表/创建 | admin:user:manage |
| PUT/DELETE | /admin/users/{id} | 编辑/停用（DELETE=停用，软处理） | admin:user:manage |
| POST | /admin/users/{id}/reset-password | 重置密码 | admin:user:manage |
| PUT | /admin/users/{id}/roles | 设置用户角色集合 | admin:user:manage |
| GET/POST | /admin/roles | 角色列表/创建 | admin:role:manage |
| PUT | /admin/roles/{id} | 编辑角色（含权限点勾选） | admin:role:manage |
| GET | /admin/permissions | 权限点树（菜单/按钮/API） | admin:role:manage |
| PUT | /admin/data-scope | 批量保存数据权限 `{userIds?,roleIds?,systems:[{code,scope}]}` | admin:datascope:manage |
| GET/PUT | /admin/params | 参数列表/更新（热生效） | admin:param:manage |
| GET | /admin/operate-logs | 操作日志检索（操作人/模块/时间）+ 导出 | admin:log:view |
| GET | /admin/health | 平台自检（调度器/数据源/日汇总/存储占用） | admin:health:view |

## 4. 业务系统（systems）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /systems | 列表（名称模糊/部门/级别/状态筛选） | system:view（按数据权限过滤） |
| POST | /systems | 创建 `{systemCode,name,dept,owner,level,onlineDate,remark}` | system:manage |
| GET/PUT | /systems/{code} | 详情/编辑 | system:manage（或 scope=manage 的系统） |
| POST | /systems/{code}/status | 状态变更 `{status,reason?}`（maintenance 静默/offline 冻结级联） | system:manage |
| DELETE | /systems/{code} | 删除业务系统（**仅 `offline` 可删**；级联清理该系统自有的采集流程/指标/数据源与分类/历史数据/告警/资源/数据权限；存在跨系统引用时 409 拒绝） | system:manage |
| GET/PUT | /systems/{code}/resources | 资源信息列表/保存 | system:manage |
| GET | /systems/export | 按当前筛选导出 Excel | system:export |
> 资源对象字段: `{resType,name,address,remark}`。`resType` 取 `server/db/middleware/storage/network`（落库列 `res_type`），`address` 存规格或 IP；PUT 为整体替换（先清空该系统资源再写入 `items`）。资源信息仅作采集配置与排查参考，不参与指标计算。

## 5. 交易日（trading-days）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /trading-days | 日历查询 `{market,from,to}` → `[{date,isTrading,dayType}]` | tradingday:view |
| PUT | /trading-days | 批量保存/修正 `{market,dates:[{date,isTrading,dayType?}]}` | tradingday:manage |
| POST | /trading-days/import | Excel 导入（模板下载 /trading-days/template） | tradingday:manage |
| GET | /trading-days/export | 导出 | tradingday:view |
| POST | /trading-days/derive | 推导 `{date,market,action: prev\|next\|offset, n?}` → 日期 | tradingday:view |

## 6. 数据源（data-sources）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /data-sources | 列表（类型/状态/系统/分类筛选；**密码字段恒脱敏**；返回含 systemName/categoryName） | datasource:view |
| POST | /data-sources | 创建（db: driver/host/port/dbname/username/password；log: scheme/path/encoding/glob + sftp 凭据；**systemCode + categoryId 必填**, 分类须为该系统下） | datasource:manage |
| GET/PUT | /data-sources/{id} | 详情（脱敏, 含 systemName/categoryName）/编辑（password 留空=不修改；**systemCode + categoryId 必填**） | datasource:manage |
> log 数据源两个路径字段: `pathTemplate`（目录/单文件, 供连接测试与健康巡检列目录）与 `globTemplate`（采集实际读取的文件匹配模式, 优先级高于 `pathTemplate`, 支持 `${biz_date:YYYYMMDD}` 等变量）。编辑（PUT）配置按"字段缺省=沿用原值"合并: 请求体不含 `globTemplate` 则保留旧值（避免旧客户端/表单整体覆盖抹掉采集入口）, 显式传空串才清空。
| GET | /data-source-categories | 分类列表（`systemCode` 筛选） | datasource:view |
| POST | /data-source-categories | 新建分类（`{systemCode,name,remark?,sort?}`；**systemCode 必填**, 系统内名称唯一, 40900 重名） | datasource:manage |
| PUT | /data-source-categories/{id} | 编辑分类（`{name,remark?,sort?}`；系统归属不可变更） | datasource:manage |
| DELETE | /data-source-categories/{id} | 删除分类（仍被数据源归属时 40900） | datasource:manage |
| POST | /data-sources/{id}/test | 测试连接（DB 执行 test_sql；日志列目录+试读一行）→ `{ok,message,rows?}` | datasource:manage |
| POST | /data-sources/{id}/disable | 停用 | datasource:manage |

## 7. 指标与阈值（metrics）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /metrics | 列表（系统/类型/单位/状态筛选；含当前值与水位摘要） | metric:view |
| POST | /metrics | 创建（含三级阈值；`metricKind` normal/group/aggregate + `parentCode`/`statModule` 支持汇总指标树；汇总指标可配 `mainModule`(主统计模块)与可选阈值；普通指标可配 `weightMetricCode`(权重指标, 加权均值; 须为同一父级下的同级普通指标)；42200 校验合理性与树形规则） | metric:manage |
| GET/PUT | /metrics/{code} | 详情（含阈值视图数据）/编辑（PUT 可更新 `mainModule`/`weightMetricCode`，传空串清除权重） | metric:manage |
| GET | /metrics/tree | 指标树（汇总指标→子指标分组→统计模块；独立指标平铺为顶层；`systemCode`/`keyword` 筛选）→ {roots, count} | metric:view |
| GET | /metrics/{code}/modules | 汇总指标/子指标统计模块视图 `{granularity:day\|rt, days:1~400 或 from/to}` → 各模块 最新值/序列/区间统计/子指标；汇总指标按 stat_module 跨子指标聚合：单叶子 self，配置权重指标 `weighted_avg`（加权均值 sum(值×权重)/sum(权重)，权重按同一业务日/时间戳对齐），否则取首个叶子 `statMode`（sum/max/avg/min，日内按同一时间戳对齐） | metric:view |
| POST | /metrics/{code}/disable | 停用（已产生告警需 `confirm=true` 二次确认） | metric:manage |
| POST | /metrics/{code}/enable | 重新启用（联动恢复被停用联动的采集流程） | metric:manage |
| DELETE | /metrics/{code} | 删除（仅停用且无关联流程/历史数据/告警/阈值历史；否则提示保持停用） | metric:manage |
| PUT | /metrics/{code}/threshold | 阈值调整（即时生效，写 threshold_history，`reason` 选填） | metric:manage |
| GET | /metrics/{code}/threshold-history | 调整历史 | metric:view |

> 汇总指标（aggregate）无采集流：日汇总（定时/`POST /data/summary/run`）时按 `mainModule`（空=首个模块）跨子指标聚合生成 `metric_value_day` 行（`source=aggregate`，含水位/级别），并按其阈值联动阈值告警（生成/合并/升级/恢复，与常规指标同一告警状态机）。聚合口径与 `/modules` 视图一致（`metricagg` 包）。

## 8. 采集流程（flows）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /flows | 列表（指标/数据源/数据源分类/状态/启用筛选） | flow:view |
| POST | /flows | 创建（created 状态；数据源/数据源分类二选一；含 collect_config/clean_config/调度配置） | flow:manage |
| GET/PUT | /flows/{id} | 详情/编辑（可重绑数据源/分类；编辑使 version+1，需重新预览生效） | flow:manage |
| POST | /flows/{id}/preview | 预览试跑（不落库，返回数据日期 bizDate + 原始数据 + 清洗后数据 + 校验提示） | flow:manage |
| POST | /flows/{id}/activate | 生效（created/编辑后 → active） | flow:manage |
| POST | /flows/{id}/disable | 停用 | flow:manage |
| POST | /flows/{id}/archive | 归档（不可恢复执行） | flow:manage |
| POST | /flows/{id}/trigger | 手工触发一次（source=manual） | flow:manage |
| GET | /flows/export-template | 批量导入模板下载（P1） | flow:view |
| POST | /flows/import | 批量导入（P1） | flow:manage |

> 调度与数据日期: trading_day 模式须配 `windowTime`（非交易时点, 单 "HH:MM", 15:00 至次日 09:00, 15 分钟间隔; 不分市场, 按 SH 日历）; 采集配置含数据日期 `bizDateMode`: `natural`=当日（默认）/ `prev_day`=上日（前一自然日）/ `calendar`=上一交易日（决定 `${biz_date}` 与落地业务日）; 编辑这些配置均使 version+1 并回到 created 状态。
>
> 绑定规则: 流程的 `dataSourceId`（数据源）与 `dataSourceCategoryId`（数据源分类）须且仅可选一项。数据源分类仅 logparse 采集可用; 分类下须至少有一个未停用的日志数据源。分类采集读取该分类下全部未停用日志数据源, 共用同一采集/清洗配置: 正则/分隔符/JSON 模式原始行合并后按指标 statMode 聚合; 段落聚合（section）模式则每台数据源先按同一段落逻辑聚合, 再跨数据源合并（次数求和 / 最快取最小 / 最慢取最大 / 平均按调用次数加权 = Σ总用时/Σ次数）。单个数据源读取失败或当日无匹配文件不中断整体（执行日志 runLog 记录明细, 执行后该数据源健康标记 error, 全部失败才判定采集失败）。编辑（PUT）绑定字段三态: 字段缺省=沿用原值, `null`=清空, 数字=设置; 绑定变更同样 version+1 并回到 created。预览响应含 `bizDate`（本次数据日期, 按 bizDateMode 推导）。

### 8.1 脚本（scripts）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET/POST | /scripts | 列表/登记（name, script_type） | script:manage |
| GET | /scripts/{id}/versions | 版本列表（含生效标记与脚本内容, 供前端查看/编辑） | script:view |
| POST | /scripts/{id}/versions | 新版本（Python 语法校验通过才保存; bash/powershell 由目标机解释器执行, 保存时不做本地校验） | script:manage |
| POST | /scripts/{id}/versions/{v}/activate | 切换生效版本（热生效） | script:manage |

## 9. 任务监控（tasks）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /tasks | 执行日志（流程/状态/日期/来源筛选，分页） | task:view |
| GET | /tasks/{id} | 详情（含错误信息、解析失败率、清洗留痕摘要） | task:view |
| GET | /tasks/{id}/cleaning-traces | 清洗留痕明细 | task:view |
| POST | /tasks/backfill | 历史补采（P1）`{flowId,from,to}` | task:manage |

## 10. 数据查询（data）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /data/rt | 日内数据 `{metricCodes[],from,to,limit?}` | data:view |
| GET | /data/day | 日数据 `{metricCodes[],from,to,page?,pageSize?}`（带 page 返回 `{items,total,page,pageSize}`，pageSize≤500） | data:view |
| GET | /metrics/{code}/trend | 趋势 `{granularity:rt\|day, days:1~400 或 from/to:YYYY-MM-DD（跨度≤400天）}` → 序列+参考线+缺数标记+区间统计 stats(count/max/maxTs/min/minTs/avg)+历史峰谷值 peaks | data:view |
| POST | /metrics/{code}/peaks/recalc | 手工重算历史峰值/谷值并落地 metric_peak_stat → {metricCode, peaks} | data:correct |
| POST | /data/corrections | 手工修正 `{metricCode,bizDate,field,newValue,reason}`（原值保留、审计、刷新提示） | data:correct |
| GET | /data/corrections | 修正单检索 | data:view |

## 11. 告警（alerts）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /alerts | 列表（级别/系统/状态/类型/时间筛选；含持续时长） | alert:view |
| GET | /alerts/{id} | 详情（含指标趋势片段、处理轨迹） | alert:view |
| POST | /alerts/{id}/claim | 认领（active/processing → processing，记认领人/时间） | alert:handle |
| POST | /alerts/{id}/note | 处理备注（追加，多次） | alert:handle |
| POST | /alerts/{id}/close | 关闭（必填 `result`；→ closed） | alert:handle |
| GET | /alerts/stats | 统计 `{from,to,groupBy:level\|system\|type}` → 趋势/高频TOP/MTTA/MTTR/达成率 | alert:view |
| GET | /alerts/export | 导出（受数据权限约束） | alert:export |

> 状态机合法迁移见《04-告警状态机与判定规则》；恢复为系统行为（判定引擎写入），不提供人工"恢复"接口。

## 12. 监控与报表（dashboard / reports）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | /dashboard/overview | 平台总览（系统数/指标数/超预警/超危险/今日新增+活动告警/采集失败/TOP10 高水位） | dashboard:view |
| GET | /systems/{code}/overview | 系统视图（全部指标水位条+状态色块） | system:view |
| GET | /reports/daily | 日报 `{date,metricsLimit?,missingLimit?}`（各系统/指标当日值、水位、状态、超线清单、异常清单；metricsTotal=全量指标数） | report:view |
| GET | /reports/daily/{date}/export | 日报导出 `?format=xlsx\|pdf` | report:export |
| GET | /reports/charts/trend | 报表图表数据（趋势/TopN/水位，供 ECharts） | report:view |

## 13. AI 辅助（ai）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | /ai/chat | 配置助手对话 `{feature,context,message}` → 流式(SSE)或阻塞回复；不可用返回 50300+degraded 提示 | ai:use |
| POST | /ai/flow-generate | 自然语言 → 流程配置建议（SQL/解析规则/脚本 + 频率 + 清洗建议），返回 draft 供预览 | ai:use |
| POST | /ai/clean-generate | 样例+需求 → 清洗脚本（含试跑对比） | ai:use |
| GET | /ai/logs | AI 调用审计检索 | admin:log:view |

> 所有 AI 接口：入参样例强制脱敏（服务端再脱敏兜底）；生成结果必须经"人工审核 → 预览试跑 → 版本保存"才生效。

## 14. 健康检查

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /healthz | 存活（200 恒返回） |
| GET | /readyz | 就绪（DB/Redis/MinIO/Worker 检查，任一失败 503） |
| GET | /version | 版本与构建信息 |

## 15. 版本与兼容

- 破坏性变更升 `/api/v2`；非破坏性变更（新增字段/接口）在 v1 内迭代；
- 前端按接口契约做防御性解析（未知字段忽略）。
