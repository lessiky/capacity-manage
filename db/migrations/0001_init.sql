-- =====================================================================
-- 容量管理平台 一期 DDL（PostgreSQL 14+）
-- 版本: 0001_init   日期: 2026-10-14   状态: M0 设计冻结基线
-- 说明:
--   1. 命名: 表/列小写下划线; 主键 bigint; 时间统一 timestamptz;
--   2. 审计字段: created_at/updated_at (trigger 自动维护);
--   3. 分区: metric_value_rt / metric_value_day 按月 RANGE 分区;
--      分区由 ensure_partitions() 函数按需创建(调度每日检查, 提前 1 个月);
--   4. 保留: rt 表在线 90 天(可配), day 表在线 3 年(可配), 超期归档;
--   5. 软删: 配置类表不物理删除, 用 status 表达(流程另含 archived)。
-- =====================================================================

BEGIN;

-- ---------------------------------------------------------------------
-- 0. 扩展与通用函数
-- ---------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid()

-- updated_at 自动维护
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 分区确保: 保证分区表在 [from_month, to_month] 范围内存在月分区
CREATE OR REPLACE FUNCTION ensure_partitions(
  parent   regclass,
  from_m   date,
  to_m     date
) RETURNS void AS $$
DECLARE
  m date;
  part_name text;
BEGIN
  m := date_trunc('month', from_m)::date;
  WHILE m <= date_trunc('month', to_m)::date LOOP
    part_name := parent::text || '_y' || to_char(m, 'YYYY') || 'm' || to_char(m, 'MM');
    IF NOT EXISTS (
      SELECT 1 FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE c.relname = part_name AND n.nspname = 'public'
    ) THEN
      EXECUTE format(
        'CREATE TABLE public.%I PARTITION OF %s FOR VALUES FROM (%L) TO (%L)',
        part_name, parent, m, (m + interval '1 month')::date
      );
      RAISE NOTICE 'created partition %', part_name;
    END IF;
    m := (m + interval '1 month')::date;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------
-- 1. 系统管理域
-- ---------------------------------------------------------------------
CREATE TABLE sys_user (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  username         varchar(64)  NOT NULL,
  password_hash    varchar(128) NOT NULL,
  real_name        varchar(64)  NOT NULL,
  dept             varchar(128),
  email            varchar(128),
  phone            varchar(32),
  status           varchar(16)  NOT NULL DEFAULT 'active',      -- active/disabled/locked
  must_change_pwd  boolean      NOT NULL DEFAULT false,
  last_login_at    timestamptz,
  last_login_ip    varchar(64),
  created_at       timestamptz  NOT NULL DEFAULT now(),
  updated_at       timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uk_sys_user_username UNIQUE (username)
);
COMMENT ON TABLE sys_user IS '平台用户';
COMMENT ON COLUMN sys_user.status IS 'active=启用 disabled=停用 locked=锁定(由登录失败策略管理)';

CREATE TABLE sys_role (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code        varchar(64) NOT NULL,
  name        varchar(128) NOT NULL,
  builtin     boolean NOT NULL DEFAULT false,
  status      varchar(16) NOT NULL DEFAULT 'active',
  remark      varchar(256),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_sys_role_code UNIQUE (code)
);
COMMENT ON TABLE sys_role IS '角色(内置: platform_admin/ops/system_owner/biz_owner/viewer)';

CREATE TABLE sys_permission (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  code        varchar(128) NOT NULL,
  name        varchar(128) NOT NULL,
  type        varchar(16)  NOT NULL,          -- menu/button/api
  path        varchar(256),                   -- 菜单路径或 API 匹配串, 如 /api/v1/metrics
  parent_id   bigint REFERENCES sys_permission(id) ON DELETE CASCADE,
  sort        int NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_sys_permission_code UNIQUE (code)
);
COMMENT ON TABLE sys_permission IS '功能权限点(菜单/按钮/API)';

CREATE TABLE sys_user_role (
  user_id    bigint NOT NULL REFERENCES sys_user(id) ON DELETE CASCADE,
  role_id    bigint NOT NULL REFERENCES sys_role(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, role_id)
);

CREATE TABLE sys_role_permission (
  role_id       bigint NOT NULL REFERENCES sys_role(id) ON DELETE CASCADE,
  permission_id bigint NOT NULL REFERENCES sys_permission(id) ON DELETE CASCADE,
  PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE sys_data_scope (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id      bigint REFERENCES sys_user(id) ON DELETE CASCADE,  -- 用户级授权(优先)
  role_id      bigint REFERENCES sys_role(id)   ON DELETE CASCADE, -- 角色级授权
  system_code  varchar(64) NOT NULL,
  scope_type   varchar(16) NOT NULL,          -- manage/view
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_data_scope_owner CHECK (user_id IS NOT NULL OR role_id IS NOT NULL)
);

-- 表达式唯一约束: PG 表级 UNIQUE 只接受列列表, 表达式须用唯一索引(表达式需双括号)
CREATE UNIQUE INDEX uk_data_scope ON sys_data_scope ((COALESCE(user_id, 0)), (COALESCE(role_id, 0)), system_code, scope_type);
COMMENT ON TABLE sys_data_scope IS '数据权限: 用户/角色 可管理或可查看的业务系统集合';

CREATE TABLE sys_operate_log (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id     bigint,
  username    varchar(64),
  module      varchar(64)  NOT NULL,
  action      varchar(64)  NOT NULL,
  target      varchar(256),
  detail      jsonb,
  ip          varchar(64),
  created_at  timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX idx_oplog_user_time ON sys_operate_log (username, created_at DESC);
CREATE INDEX idx_oplog_module_time ON sys_operate_log (module, created_at DESC);
COMMENT ON TABLE sys_operate_log IS '关键操作审计日志(阈值/流程/修正/告警处理/权限/导出/AI)';

CREATE TABLE sys_param (
  key         varchar(128) PRIMARY KEY,
  value       text         NOT NULL,
  description varchar(256),
  category    varchar(32)  NOT NULL DEFAULT 'general',  -- general/schedule/alert/retention
  updated_by  varchar(64),
  updated_at  timestamptz  NOT NULL DEFAULT now()
);
COMMENT ON TABLE sys_param IS '系统参数(热生效: 读缓存+写后失效)';

INSERT INTO sys_param (key, value, description, category) VALUES
  ('daily_summary_time',   '00:10',   '每日汇总触发时间(HH:MM, 平台时区)',      'schedule'),
  ('daily_summary_mode',   'calendar','汇总业务日期口径: natural=自然日 calendar=交易日','schedule'),
  ('rt_retain_days',       '90',      '日内数据在线保留天数(超期分区归档/清理)', 'retention'),
  ('day_retain_days',      '1095',    '日数据在线保留天数(默认3年)',             'retention'),
  ('collect_concurrency',  '8',       '调度并发数',                              'schedule'),
  ('flow_timeout_seconds', '300',     '单流程执行超时上限(秒)',                  'schedule'),
  ('retry_count',          '3',       '采集失败默认重试次数',                    'schedule'),
  ('retry_interval_seconds','30',     '重试间隔(秒)',                            'schedule'),
  ('judge_debounce_n',     '1',       '超线连续N次才告警(1=立即)',               'alert'),
  ('recover_debounce_m',   '1',       '低于阈值连续M次自动恢复(1=立即)',         'alert'),
  ('session_timeout_min',  '30',      '会话超时(分钟)',                          'general'),
  ('login_fail_limit',     '5',       '连续失败锁定阈值',                        'general'),
  ('login_lock_minutes',   '15',      '锁定时长(分钟)',                          'general'),
  ('anomaly_ratio_limit',  '0.2',     '单次采集异常值占比超限阈值(产生P2告警)',   'alert'),
  ('timezone',             'Asia/Shanghai','平台业务时区',                       'general');

-- ---------------------------------------------------------------------
-- 2. 业务系统域
-- ---------------------------------------------------------------------
CREATE TABLE business_system (
  system_code    varchar(64)  PRIMARY KEY,
  name           varchar(128) NOT NULL,
  dept           varchar(128),
  owner          varchar(64)  NOT NULL,            -- 负责人(用户名)
  level          varchar(16)  NOT NULL DEFAULT 'general', -- core/important/general
  status         varchar(16)  NOT NULL DEFAULT 'normal',  -- normal/maintenance/offline
  online_date    date,
  silent_maintain boolean NOT NULL DEFAULT true,   -- 维护中默认静默告警
  remark         varchar(512),
  created_by     varchar(64),
  created_at     timestamptz  NOT NULL DEFAULT now(),
  updated_at     timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uk_business_system_name UNIQUE (name)
);
COMMENT ON TABLE business_system IS '业务系统';

CREATE TABLE business_system_resource (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  system_code  varchar(64) NOT NULL REFERENCES business_system(system_code) ON DELETE CASCADE,
  res_type     varchar(16) NOT NULL,               -- server/db/storage
  name         varchar(128),
  address      varchar(256),
  remark       varchar(256),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sysres_system ON business_system_resource (system_code);
COMMENT ON TABLE business_system_resource IS '系统资源信息(采集配置选择与排查参考)';

-- ---------------------------------------------------------------------
-- 3. 交易日域
-- ---------------------------------------------------------------------
CREATE TABLE trading_day_calendar (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  market        varchar(16) NOT NULL DEFAULT 'SH',   -- SH/SZ/OVERSEAS
  cal_date      date        NOT NULL,
  is_trading    boolean     NOT NULL,
  day_type      varchar(16) NOT NULL,                -- trading/weekend/holiday/makeup
  source        varchar(16) NOT NULL DEFAULT 'manual', -- manual/excel/official
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_trading_calendar UNIQUE (market, cal_date)
);
CREATE INDEX idx_trading_calendar_date ON trading_day_calendar (cal_date, market, is_trading);
COMMENT ON TABLE trading_day_calendar IS '交易日日历(多市场, 覆盖当年+次年)';

-- ---------------------------------------------------------------------
-- 4. 指标与阈值域
-- ---------------------------------------------------------------------
CREATE TABLE capacity_metric (
  metric_code      varchar(64) PRIMARY KEY,
  name             varchar(128) NOT NULL,
  system_code      varchar(64) NOT NULL REFERENCES business_system(system_code) ON DELETE RESTRICT,
  category         varchar(32),                     -- compute/storage/database/middleware/log/other
  metric_type      varchar(16) NOT NULL,            -- usage/absolute/increment
  unit             varchar(32)  NOT NULL,           -- %/GB/TB/个/连接数...
  ds_type          varchar(16)  NOT NULL,           -- db/log (数据源类型)
  collect_method   varchar(16)  NOT NULL,           -- sql/logparse/python
  stat_mode        varchar(16)  NOT NULL DEFAULT 'latest', -- latest/max/min/avg/sum
  judge_direction  varchar(16)  NOT NULL DEFAULT 'higher_worse', -- higher_worse/lower_worse
  baseline         numeric(20,6),                   -- 容量基线(标称值)
  baseline_source  varchar(16) NOT NULL DEFAULT 'manual', -- manual/collect
  threshold_mode   varchar(16) NOT NULL DEFAULT 'percent',  -- percent/absolute
  warn_line        numeric(20,6),                   -- 预警线(百分比0-100 或 绝对值)
  danger_line      numeric(20,6),                   -- 危险线
  status           varchar(16) NOT NULL DEFAULT 'active', -- active/disabled
  remark           varchar(512),
  created_by       varchar(64),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_metric_name_system UNIQUE (system_code, name)
);
CREATE INDEX idx_metric_system ON capacity_metric (system_code);
CREATE INDEX idx_metric_status ON capacity_metric (status) WHERE status = 'active';
COMMENT ON TABLE capacity_metric IS '容量指标(三级阈值内联, 调整历史见 threshold_history)';

CREATE TABLE threshold_history (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  metric_code  varchar(64) NOT NULL REFERENCES capacity_metric(metric_code) ON DELETE CASCADE,
  changed_at   timestamptz NOT NULL DEFAULT now(),
  operator     varchar(64) NOT NULL,
  baseline_old numeric(20,6), baseline_new numeric(20,6),
  warn_old     numeric(20,6), warn_new     numeric(20,6),
  danger_old   numeric(20,6), danger_new   numeric(20,6),
  reason       varchar(512),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_threshold_history_metric ON threshold_history (metric_code, changed_at DESC);
COMMENT ON TABLE threshold_history IS '阈值调整历史(旧值→新值, 原因选填)';

-- ---------------------------------------------------------------------
-- 5. 数据源域
-- ---------------------------------------------------------------------
CREATE TABLE data_source (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name           varchar(128) NOT NULL,
  type           varchar(16)  NOT NULL,             -- db/log
  status         varchar(16)  NOT NULL DEFAULT 'available', -- available/abnormal/disabled
  config         jsonb        NOT NULL,
  -- db 型:  {driver: oracle|mysql, host, port, service/dbname, username,
  --          password_enc: <AES-256-GCM base64>, test_sql}
  -- log 型: {scheme: local|sftp, host?, port?, username?, password_enc?,
  --          path_template, encoding, glob_template}
  health_status  varchar(16)  NOT NULL DEFAULT 'unknown',
  last_success_at timestamptz,
  last_error     varchar(1024),
  created_by     varchar(64),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_data_source_name UNIQUE (name)
);
COMMENT ON TABLE data_source IS '数据源(凭据加密存储, config.password_enc 禁止明文)';

-- ---------------------------------------------------------------------
-- 6. 采集流程域
-- ---------------------------------------------------------------------
CREATE TABLE python_script (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name         varchar(128) NOT NULL,
  script_type  varchar(16)  NOT NULL,               -- collect/clean
  description  varchar(512),
  status       varchar(16)  NOT NULL DEFAULT 'active',
  created_by   varchar(64),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_python_script_name UNIQUE (name, script_type)
);
COMMENT ON TABLE python_script IS 'Python 脚本登记(版本管理见 python_script_version)';

CREATE TABLE python_script_version (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  script_id    bigint NOT NULL REFERENCES python_script(id) ON DELETE CASCADE,
  version      int    NOT NULL,
  content      text   NOT NULL,
  checksum     char(64) NOT NULL,                   -- sha256
  active       boolean NOT NULL DEFAULT false,
  file_ref     varchar(256),                        -- MinIO 备份引用(可选)
  published_by varchar(64),
  published_at timestamptz,
  remark       varchar(512),
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_script_version UNIQUE (script_id, version)
);
COMMENT ON TABLE python_script_version IS '脚本版本(生效版本唯一: 按 script_id 部分唯一索引)';
CREATE UNIQUE INDEX uk_script_active_version ON python_script_version (script_id)
  WHERE active;

CREATE TABLE collect_flow (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  flow_code      varchar(64) NOT NULL,
  name           varchar(128) NOT NULL,
  metric_code    varchar(64) NOT NULL REFERENCES capacity_metric(metric_code) ON DELETE RESTRICT,
  data_source_id bigint NOT NULL REFERENCES data_source(id) ON DELETE RESTRICT,
  collect_type   varchar(16) NOT NULL,              -- sql/logparse/python
  collect_config jsonb NOT NULL,
  -- sql:       {sql_template, column_mapping{指标列}, aggregate: single|sum|avg|max|min}
  -- logparse:  {mode: regex|delimited|json, pattern|delimiters|fields,
  --             file_glob, time_filter{from,to}, value_field, unit_field?}
  -- python:    {script_id, version}
  clean_config   jsonb NOT NULL DEFAULT '{}'::jsonb,
  -- {transforms:[{type:unit|linear|round, ...}], anomaly_rules:[...], dedup_take: latest|first|avg}
  schedule_mode  varchar(16) NOT NULL DEFAULT 'normal', -- normal/trading_day
  cron_expr      varchar(64),                        -- normal 模式必填, 如 0 0 2 * * ?
  trading_market varchar(16),                        -- trading_day 模式: SH/SZ/OVERSEAS
  window_time    varchar(8),                         -- trading_day 模式收市后窗口, 如 17:30
  biz_date_mode  varchar(16) NOT NULL DEFAULT 'natural', -- natural/calendar 汇总口径
  version        int NOT NULL DEFAULT 1,
  status         varchar(16) NOT NULL DEFAULT 'created', -- created/active/disabled/archived
  enabled        boolean NOT NULL DEFAULT true,
  remark         varchar(512),
  created_by     varchar(64),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_collect_flow_code UNIQUE (flow_code),
  CONSTRAINT uk_collect_flow_metric UNIQUE (metric_code)
);
CREATE INDEX idx_collect_flow_status ON collect_flow (status, enabled);
COMMENT ON TABLE collect_flow IS '采集流程(一流程对一指标; 生命周期 created→active→disabled→archived)';

CREATE TABLE collect_task_log (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  flow_id        bigint NOT NULL REFERENCES collect_flow(id) ON DELETE CASCADE,
  flow_code      varchar(64) NOT NULL,
  metric_code    varchar(64) NOT NULL,
  triggered_at   timestamptz NOT NULL,
  started_at     timestamptz,
  finished_at    timestamptz,
  status         varchar(16) NOT NULL DEFAULT 'running', -- running/success/failed/timeout
  duration_ms    bigint,
  rows_collected int,
  parse_fail_count int DEFAULT 0,
  parse_fail_rate numeric(7,4),
  anomaly_count  int DEFAULT 0,
  error_msg      varchar(2048),
  retry_count    int NOT NULL DEFAULT 0,
  source         varchar(16) NOT NULL DEFAULT 'auto',  -- auto/manual/backfill
  operator       varchar(64),
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_tasklog_flow_time ON collect_task_log (flow_id, triggered_at DESC);
CREATE INDEX idx_tasklog_status_time ON collect_task_log (status, triggered_at DESC);
COMMENT ON TABLE collect_task_log IS '采集执行日志(任务监控)';

CREATE TABLE cleaning_trace (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  task_log_id    bigint NOT NULL REFERENCES collect_task_log(id) ON DELETE CASCADE,
  metric_code    varchar(64) NOT NULL,
  raw_value      varchar(256),
  clean_value    numeric(24,8),
  rules_applied  jsonb,
  is_anomaly     boolean NOT NULL DEFAULT false,
  anomaly_reason varchar(256),
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_cleaning_trace_task ON cleaning_trace (task_log_id);
COMMENT ON TABLE cleaning_trace IS '清洗留痕(原始值→清洗值/规则/异常原因, 采样保留, 默认全量)';

-- ---------------------------------------------------------------------
-- 7. 数据落地域(分区表)
-- ---------------------------------------------------------------------
CREATE TABLE metric_value_rt (
  metric_code   varchar(64)   NOT NULL,
  ts            timestamptz   NOT NULL,
  raw_value     numeric(24,8),
  clean_value   numeric(24,8),
  is_anomaly    boolean       NOT NULL DEFAULT false,
  anomaly_reason varchar(256),
  judge_level   varchar(16)   NOT NULL DEFAULT 'normal', -- normal/warn/danger
  source        varchar(16)   NOT NULL DEFAULT 'collect', -- collect/backfill/manual
  created_at    timestamptz   NOT NULL DEFAULT now(),
  PRIMARY KEY (metric_code, ts)
) PARTITION BY RANGE (ts);
CREATE INDEX idx_metric_value_rt_ts ON metric_value_rt (ts DESC);
COMMENT ON TABLE metric_value_rt IS '日内数据点(按月分区, 指标+时间戳唯一=去重依据)';

CREATE TABLE metric_value_day (
  metric_code   varchar(64)  NOT NULL,
  biz_date      date         NOT NULL,
  day_max       numeric(24,8),
  day_min       numeric(24,8),
  day_avg       numeric(24,8),
  day_last      numeric(24,8),
  stat_value    numeric(24,8),        -- 按指标统计方式取值(日报主值)
  water_level   numeric(9,4),         -- 当日水位(0-1, 相对基线)
  level         varchar(16)  NOT NULL DEFAULT 'normal', -- normal/warn/danger/missing
  point_count   int          NOT NULL DEFAULT 0,
  source        varchar(16)  NOT NULL DEFAULT 'collect', -- collect/backfill
  corrected     boolean      NOT NULL DEFAULT false,
  correct_reason varchar(512),
  corrected_by  varchar(64),
  corrected_at  timestamptz,
  original_snapshot jsonb,            -- 修正前快照
  created_at    timestamptz  NOT NULL DEFAULT now(),
  updated_at    timestamptz  NOT NULL DEFAULT now(),
  PRIMARY KEY (metric_code, biz_date)
) PARTITION BY RANGE (biz_date);
CREATE INDEX idx_metric_value_day_date ON metric_value_day (biz_date DESC, level);
COMMENT ON TABLE metric_value_day IS '每日落地数据(按月分区, 指标+业务日期唯一, 在线3年)';

CREATE TABLE daily_summary_log (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  biz_date    date NOT NULL,
  started_at  timestamptz,
  finished_at timestamptz,
  status      varchar(16) NOT NULL DEFAULT 'running', -- running/success/failed/partial
  metrics_total int,
  metrics_done  int,
  metrics_missing int DEFAULT 0,
  error_msg   varchar(2048),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uk_daily_summary_log ON daily_summary_log (biz_date);
COMMENT ON TABLE daily_summary_log IS '每日汇总执行日志(幂等重跑)';

CREATE TABLE data_correction (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  metric_code  varchar(64) NOT NULL,
  biz_date     date NOT NULL,
  field        varchar(32) NOT NULL,     -- stat_value/water_level
  old_value    numeric(24,8),
  new_value    numeric(24,8),
  reason       varchar(512) NOT NULL,
  operator     varchar(64) NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  refresh_hint varchar(256)              -- 受影响报表/告警刷新提示
);
CREATE INDEX idx_correction_metric ON data_correction (metric_code, biz_date DESC);
COMMENT ON TABLE data_correction IS '手工修正单(原因必填, 原值保留)';

-- ---------------------------------------------------------------------
-- 8. 告警域
-- ---------------------------------------------------------------------
CREATE TABLE alert_record (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  alert_no     varchar(32)  NOT NULL,
  metric_code  varchar(64)  NOT NULL,
  system_code  varchar(64)  NOT NULL,
  level        varchar(8)   NOT NULL,       -- p0/p1/p2
  alert_type   varchar(16)  NOT NULL,       -- threshold/collect_fail/missing/anomaly
  status       varchar(16)  NOT NULL DEFAULT 'active', -- active/processing/recovered/closed
  trigger_at   timestamptz  NOT NULL,
  last_value_at timestamptz,
  first_value  numeric(24,8),
  last_value   numeric(24,8),
  threshold    numeric(24,8),
  water_level  numeric(9,4),
  duration_min int,
  recovered_at timestamptz,
  claimed_by   varchar(64),
  claimed_at   timestamptz,
  handle_note  varchar(1024),
  closed_by    varchar(64),
  closed_at    timestamptz,
  close_result varchar(1024),
  silent       boolean NOT NULL DEFAULT false,  -- 静默期内抑制(完整记录)
  detail       jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_alert_no UNIQUE (alert_no)
);
CREATE INDEX idx_alert_active ON alert_record (status) WHERE status IN ('active', 'processing');
CREATE INDEX idx_alert_metric ON alert_record (metric_code, trigger_at DESC);
CREATE INDEX idx_alert_system_time ON alert_record (system_code, trigger_at DESC);
CREATE INDEX idx_alert_level_time ON alert_record (level, trigger_at DESC);
COMMENT ON TABLE alert_record IS '告警记录(状态机: active→processing→recovered/closed; 关闭后仅可备注)';

-- ---------------------------------------------------------------------
-- 9. 报表与 AI 域
-- ---------------------------------------------------------------------
CREATE TABLE report_result (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  report_type  varchar(32) NOT NULL,       -- daily/weekly/monthly
  biz_date     date NOT NULL,
  status       varchar(16) NOT NULL DEFAULT 'running', -- running/success/failed
  file_ref     varchar(256),               -- MinIO 对象键
  metrics_total int,
  metrics_over  int,
  error_msg    varchar(1024),
  generated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_report_result UNIQUE (report_type, biz_date)
);
COMMENT ON TABLE report_result IS '报表生成结果(导出文件引用)';

CREATE TABLE ai_call_log (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  feature      varchar(32) NOT NULL,       -- flow_gen/clean_gen/assistant
  user_id      varchar(64),
  model        varchar(128),
  input_digest varchar(512),               -- 脱敏后输入摘要
  output_digest varchar(1024),             -- 输出摘要
  latency_ms   int,
  status       varchar(16) NOT NULL,       -- success/failed/degraded
  error_msg    varchar(1024),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_call_time ON ai_call_log (created_at DESC);
COMMENT ON TABLE ai_call_log IS 'AI 调用审计(输入输出摘要, 脱敏)';

-- ---------------------------------------------------------------------
-- 10. 触发器(updated_at)
-- ---------------------------------------------------------------------
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'sys_user','business_system','business_system_resource','trading_day_calendar',
    'capacity_metric','data_source','collect_flow','python_script',
    'metric_value_day','alert_record'
  ] LOOP
    EXECUTE format(
      'CREATE TRIGGER trg_%s_updated BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()',
      t, t
    );
  END LOOP;
END;
$$;

-- ---------------------------------------------------------------------
-- 11. 初始分区(rt 当月+下月; day 当月+下月) 与保留策略占位
-- ---------------------------------------------------------------------
SELECT ensure_partitions('metric_value_rt', date_trunc('month', current_date)::date, (date_trunc('month', current_date) + interval '1 month')::date);
SELECT ensure_partitions('metric_value_day', date_trunc('month', current_date)::date, (date_trunc('month', current_date) + interval '1 month')::date);

COMMIT;