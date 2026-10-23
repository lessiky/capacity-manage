# 告警中心自定义看板设计

**日期:** 2026-10-23

**状态:** 已确认，待用户复核

## 1. 背景与目标

告警中心已有告警列表、告警统计与告警策略，但列表筛选只支持单值级别、状态和类型，日期需要手工选择，排序固定为触发时间倒序，用户每次进入页面都需要重新设置。

本功能增加用户级“自定义告警看板”，让用户保存自己的告警筛选条件与组合排序规则。用户换设备或重新登录后，看板仍按后台保存的配置展示数据。

登录后的默认落地页仍为首页，不因本功能改变。进入告警中心后，默认打开“自定义看板”页签。

## 2. 范围

### 2.1 本次实现

- 级别、状态、类型、系统多值范围筛选。
- 日期模式：当日或近 N 日。
- 按级别、状态、类型、系统、触发时间组合排序。
- 每个排序字段独立设置升序或降序。
- 排序字段可调整优先级。
- 配置按登录用户保存到后台。
- 看板数据查询与原有告警数据权限保持一致。
- 告警中心新增“自定义看板”页签，原有页签保持不变。

### 2.2 不在本次范围

- 不修改登录后的默认落地页。
- 不把自定义配置共享给其他用户或角色。
- 不支持导出看板配置。
- 不改变告警生成、升级、恢复、关闭和通知规则。
- 不替换原有“告警列表”页签。

## 3. 用户交互

### 3.1 页签

告警中心页签顺序调整为：

1. 自定义看板
2. 告警列表
3. 告警统计
4. 告警策略

进入告警中心时默认打开“自定义看板”。登录成功后仍跳转首页。

### 3.2 筛选条件

看板顶部提供以下条件：

- 级别：多选，可选 P1、P2、P3、P4。
- 状态：多选，可选活动、处理中、已恢复、已关闭。
- 类型：多选，可选阈值、缺数、采集失败、异常值、数据源异常。
- 系统：多选，选项只包含当前用户有数据权限的业务系统。
- 日期模式：单选的“当日”或“近 N 日”。
- 近 N 日：选择“近 N 日”时显示天数输入，范围为 1 至 365。

空的多选集合表示不限制该字段。

### 3.3 组合排序

排序构建器支持：

- 添加排序字段。
- 删除排序字段。
- 上移或下移排序字段，调整优先级。
- 为每个字段选择升序或降序。

允许的排序字段为：

- 级别
- 状态
- 类型
- 系统
- 触发时间

排序字段不得重复，最多 5 个。配置为空时使用默认排序“触发时间倒序”。

### 3.4 保存与恢复

- 点击“保存配置”后写入后台，保存成功后立即重新查询看板。
- 保存失败时保留当前表单内容，并显示后端返回的具体错误。
- 点击“恢复默认”只恢复表单，不立即写后台；用户确认保存后才持久化。
- 首次进入且后台没有配置时，直接使用默认配置。

## 4. 默认配置

首次使用或后台没有用户配置时，默认值为：

- 级别：P1、P2、P3、P4。
- 状态：活动、处理中、已恢复、已关闭。
- 类型：阈值、缺数、采集失败、异常值、数据源异常。
- 系统：空集合，表示全部可见系统。
- 日期模式：当日。
- 近 N 日数值：7。
- 排序：触发时间倒序。

## 5. 数据模型

新增迁移 `0031_alert_dashboard_preference.sql` 和表 `alert_dashboard_preference`：

```sql
CREATE TABLE alert_dashboard_preference (
  user_id      bigint PRIMARY KEY REFERENCES sys_user(id) ON DELETE CASCADE,
  levels       jsonb NOT NULL DEFAULT '["p1","p2","p3","p4"]'::jsonb,
  statuses     jsonb NOT NULL DEFAULT '["active","processing","recovered","closed"]'::jsonb,
  alert_types  jsonb NOT NULL DEFAULT '["threshold","missing","collect_fail","anomaly","ds_error"]'::jsonb,
  system_codes jsonb NOT NULL DEFAULT '[]'::jsonb,
  date_mode    varchar(16) NOT NULL DEFAULT 'today',
  recent_days  int NOT NULL DEFAULT 7,
  sort_rules   jsonb NOT NULL DEFAULT '[{"field":"triggerAt","direction":"desc"}]'::jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_alert_dashboard_date_mode CHECK (date_mode IN ('today', 'recent_days')),
  CONSTRAINT chk_alert_dashboard_recent_days CHECK (recent_days BETWEEN 1 AND 365)
);
```

新增 GORM 模型 `AlertDashboardPreference`：

- 字符串集合使用 `persistence.StringList`。
- 排序规则使用独立结构并通过 `jsonb` 持久化。
- `UserID` 为主键，保存时使用 upsert。

## 6. 后端接口

### 6.1 读取配置

`GET /api/v1/alerts/dashboard-preference`

返回当前登录用户的配置。无记录时返回默认配置，不写数据库。

### 6.2 保存配置

`PUT /api/v1/alerts/dashboard-preference`

请求示例：

```json
{
  "levels": ["p1", "p2"],
  "statuses": ["active", "processing"],
  "alertTypes": ["threshold", "missing"],
  "systemCodes": ["SYS_A"],
  "dateMode": "recent_days",
  "recentDays": 7,
  "sortRules": [
    { "field": "level", "direction": "asc" },
    { "field": "triggerAt", "direction": "desc" }
  ]
}
```

保存成功后返回归一化后的配置。

### 6.3 看板数据

`GET /api/v1/alerts/dashboard?page=1&pageSize=20`

后端读取当前用户配置，按配置查询告警，返回与现有告警分页相同的数据结构。

### 6.4 可见系统选项

`GET /api/v1/alerts/dashboard-options`

返回当前用户可用于系统筛选的系统列表：

```json
{
  "systems": [
    { "systemCode": "SYS_A", "name": "系统 A" }
  ]
}
```

平台管理员返回全部业务系统；其他用户只返回数据权限内系统。

所有接口使用 `alert:view` 权限，用户身份取自认证中间件中的当前用户 ID。

## 7. 校验规则

### 7.1 集合字段

- 级别只允许 `p1`、`p2`、`p3`、`p4`。
- 状态只允许 `active`、`processing`、`recovered`、`closed`。
- 类型只允许 `threshold`、`missing`、`collect_fail`、`anomaly`、`ds_error`。
- 保存时，非空系统编码集合中的每一项都必须存在于当前用户可见系统集合中。
- 集合内去重；空集合表示不限制。

### 7.2 日期

- `dateMode` 只允许 `today` 或 `recent_days`。
- `recentDays` 必须在 1 至 365 之间。
- 当 `dateMode=today` 时仍保存 `recentDays`，便于用户后续切换。
- 日期区间按平台业务时区计算：当日为当天 00:00 至次日 00:00；近 N 日包含今天。

### 7.3 排序

- `field` 只允许 `level`、`status`、`alertType`、`systemCode`、`triggerAt`。
- `direction` 只允许 `asc` 或 `desc`。
- 排序字段不得重复，最多 5 条。
- 排序字段为空时使用 `triggerAt desc`。
- 所有查询最后追加 `id desc`，确保分页稳定。

业务排序口径：

- 级别从小到大：P1、P2、P3、P4。
- 状态从小到大：活动、处理中、已恢复、已关闭。
- 类型和系统按编码升序。
- 触发时间按时间值排序。

## 8. 数据权限与安全

- 仅能读取和修改当前用户的配置，接口不接受 `userId` 参数。
- 保存时逐项校验系统编码；存在不可见或已删除的系统时返回 400。
- 查询时读取持久化的原始系统编码集合。原始集合为空表示全部可见系统；原始集合非空时，再与当前可见系统求交集。
- 若持久化的系统集合非空但当前交集为空，查询返回空结果，不回退为全部系统。
- 读取配置时保留持久化编码，避免把“指定系统后失去权限”静默改写为“全部系统”；前端只把可见系统作为可选选项，用户再次保存前必须移除已失效编码。
- 所有排序字段经过白名单映射，禁止将前端字段直接拼接到 SQL。
- 查询始终叠加原有告警数据权限。
- 保存配置写入操作日志，模块为 `alert`，动作为 `alert.dashboard.preference.save`。

## 9. 错误处理

- 未登录或会话失效：沿用统一 401 处理。
- 无 `alert:view` 权限：沿用统一 403 处理。
- 非法枚举、天数或排序规则：返回 400，并指出具体字段。
- 数据库读取失败：返回统一内部错误。
- 历史配置包含非法值时：读取时归一化；无法归一化的部分回退默认值，保证看板可打开。
- 配置中的系统已被删除：保存时拒绝；读取时忽略不存在的系统，避免阻断看板。

## 10. 前端实现边界

主要修改 `frontend/src/views/Alerts.vue`：

- 新增“自定义看板”页签。
- 复用现有告警表格列、标签映射和分页。
- 新增多选、日期模式、天数输入、排序构建器和保存操作。
- 进入看板时先读取配置和可见系统，再查询数据。
- 保存配置成功后重新查询数据。

不新增第三方前端依赖，继续使用 Vue 3、TypeScript、Element Plus 和项目已有 `http` 封装。

## 11. 验证策略

### 11.1 后端

- 迁移测试：表、默认值、外键和检查约束可正常创建。
- Handler 测试：
  - 未保存配置时返回默认配置。
  - PUT 后 GET 返回同一用户的配置。
  - 不同用户配置互不影响。
  - 非法级别、状态、类型、天数、排序字段和重复排序字段返回错误。
  - 系统范围叠加数据权限。
  - 多值筛选与多字段排序结果正确。
  - 分页稳定，末级排序包含 `id desc`。
- 路由测试：三个新接口注册在 `alert:view` 权限下。

### 11.2 前端

- `npm run lint` 通过。
- `npm run build` 通过。
- 浏览器验证：
  - 登录后仍进入首页。
  - 进入告警中心默认打开自定义看板。
  - 修改条件并保存后，刷新页面仍展示已保存条件。
  - 多字段排序和排序优先级生效。
  - 切换“当日 / 近 N 日”可正常查询。
  - 普通用户无法选择无权限系统。

## 12. 验收标准

- 用户可配置级别、状态、类型、系统和日期范围。
- 用户可配置最多 5 个排序字段，并为每个字段独立选择升序或降序。
- 配置按用户保存，重新登录或更换设备后仍能恢复。
- 看板数据与配置一致，并严格遵守告警数据权限。
- 登录后的默认页面仍为首页。
- 原有告警列表、统计和策略功能不受影响。
