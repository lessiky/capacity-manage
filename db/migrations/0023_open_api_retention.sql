-- M8 Task 10: 开放 API、归档与二期扩展项
--
-- 1) api_key: API Key 管理、scope、限流与调用审计（FR-DAY-07）
-- 2) metric_archive_run / metric_archive_index: 超期分区压缩归档与按需恢复（FR-DAY-06）
-- 3) derived_metric / threshold_template / metric_import_run / conversion_rule（FR-MET-04/13、FR-COL-16/23）
-- 4) calendar_check_run: 交易日历完整性检查记录（FR-TD-05）

CREATE TABLE IF NOT EXISTS api_key (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name          varchar(128) NOT NULL,
  key_id        varchar(32) NOT NULL UNIQUE,          -- 明文前缀, 用于定位与日志
  key_hash      varchar(128) NOT NULL,                -- sha256(secret), 不落明文
  scopes        jsonb NOT NULL DEFAULT '[]'::jsonb,   -- ["data:read","forecast:read","alert:webhook"]
  system_scope  jsonb NOT NULL DEFAULT '[]'::jsonb,   -- 空数组=不限系统
  rate_limit    int NOT NULL DEFAULT 300,             -- 每分钟请求上限
  status        varchar(16) NOT NULL DEFAULT 'active',-- active/disabled
  remark        varchar(512) NOT NULL DEFAULT '',
  created_by    varchar(64) NOT NULL DEFAULT '',
  last_used_at  timestamptz,
  expires_at    timestamptz,
  call_count    bigint NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_api_key_status ON api_key (status, created_at DESC);
COMMENT ON TABLE api_key IS '开放 API 凭证(仅存 secret 摘要, 明文只在创建时返回一次)';

CREATE TABLE IF NOT EXISTS api_call_log (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  key_id        bigint REFERENCES api_key(id) ON DELETE SET NULL,
  key_name      varchar(128) NOT NULL DEFAULT '',
  method        varchar(8) NOT NULL,
  path          varchar(256) NOT NULL,
  scope         varchar(64) NOT NULL DEFAULT '',
  status        int NOT NULL,
  allowed       boolean NOT NULL DEFAULT true,
  reason        varchar(128) NOT NULL DEFAULT '',
  ip            varchar(64) NOT NULL DEFAULT '',
  latency_ms    int,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_api_call_log_key ON api_call_log (key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_call_log_time ON api_call_log (created_at DESC);

-- 归档运行与索引：按分区为单位压缩归档，支持按指标+日期区间按需恢复
CREATE TABLE IF NOT EXISTS metric_archive_run (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  table_name    varchar(64) NOT NULL,                 -- metric_value_rt / metric_value_day
  partition     varchar(128) NOT NULL,                -- 分区名
  from_date     date NOT NULL,
  to_date       date NOT NULL,
  row_count     bigint NOT NULL DEFAULT 0,
  file_ref      varchar(512) NOT NULL DEFAULT '',     -- 压缩归档文件（store key）
  bytes         bigint NOT NULL DEFAULT 0,
  status        varchar(16) NOT NULL DEFAULT 'running',-- running/success/failed/restored
  error_msg     varchar(1024) NOT NULL DEFAULT '',
  started_at    timestamptz,
  finished_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_metric_archive_partition UNIQUE (table_name, partition)
);
CREATE INDEX IF NOT EXISTS idx_metric_archive_status ON metric_archive_run (status, created_at DESC);
COMMENT ON TABLE metric_archive_run IS '超期分区压缩归档运行(FR-DAY-06)';

CREATE TABLE IF NOT EXISTS metric_archive_index (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  archive_id    bigint NOT NULL REFERENCES metric_archive_run(id) ON DELETE CASCADE,
  metric_code   varchar(64) NOT NULL,
  biz_date      date NOT NULL,
  file_ref      varchar(512) NOT NULL DEFAULT '',
  byte_offset   bigint NOT NULL DEFAULT 0,
  row_bytes     int NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_metric_archive_index_lookup
  ON metric_archive_index (metric_code, biz_date);
COMMENT ON TABLE metric_archive_index IS '归档索引, 支持按指标+日期按需恢复';

-- 衍生指标：基于表达式或聚合源指标计算（FR-MET-04）
CREATE TABLE IF NOT EXISTS derived_metric (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  metric_code    varchar(64) NOT NULL UNIQUE,
  name           varchar(128) NOT NULL,
  system_code    varchar(64) NOT NULL DEFAULT '',
  expression     text NOT NULL,                       -- 例如 (A5_CPU + A5_MEM) / 2
  source_metrics jsonb NOT NULL DEFAULT '[]'::jsonb,
  unit           varchar(32) NOT NULL DEFAULT '',
  status         varchar(16) NOT NULL DEFAULT 'active',
  created_by     varchar(64) NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE derived_metric IS '衍生指标定义(FR-MET-04)';

-- 阈值模板：批量套用到指标（FR-MET-13）
CREATE TABLE IF NOT EXISTS threshold_template (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name           varchar(128) NOT NULL,
  judge_direction varchar(16) NOT NULL,
  warn_line      numeric(24,8),
  danger_line    numeric(24,8),
  warn_ratio     numeric(6,4),
  danger_ratio   numeric(6,4),
  unit           varchar(32) NOT NULL DEFAULT '',
  status         varchar(16) NOT NULL DEFAULT 'active',
  remark         varchar(512) NOT NULL DEFAULT '',
  created_by     varchar(64) NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE threshold_template IS '阈值模板(FR-MET-13)';

-- 批量导入运行（FR-COL-16）
CREATE TABLE IF NOT EXISTS metric_import_run (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  filename      varchar(256) NOT NULL,
  total_rows    int NOT NULL DEFAULT 0,
  success_rows  int NOT NULL DEFAULT 0,
  failed_rows   int NOT NULL DEFAULT 0,
  status        varchar(16) NOT NULL DEFAULT 'running',
  error_report  jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_by    varchar(64) NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz
);
COMMENT ON TABLE metric_import_run IS '指标批量导入运行(FR-COL-16)';

-- 自定义转换规则（FR-COL-23）
CREATE TABLE IF NOT EXISTS conversion_rule (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name          varchar(128) NOT NULL,
  scope_type    varchar(16) NOT NULL DEFAULT 'metric',
  scope_value   varchar(128) NOT NULL DEFAULT '',
  rule_type     varchar(32) NOT NULL,                 -- scale/offset/clamp/regex/expr
  expression    text NOT NULL DEFAULT '',
  params        jsonb NOT NULL DEFAULT '{}'::jsonb,
  priority      int NOT NULL DEFAULT 100,
  status        varchar(16) NOT NULL DEFAULT 'active',
  created_by    varchar(64) NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_conversion_rule_scope
  ON conversion_rule (scope_type, scope_value, priority);
COMMENT ON TABLE conversion_rule IS '自定义转换规则(FR-COL-23)';

-- 日历完整性检查记录（FR-TD-05）
CREATE TABLE IF NOT EXISTS calendar_check_run (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  year          int NOT NULL,
  expected_days int NOT NULL DEFAULT 0,
  actual_days   int NOT NULL DEFAULT 0,
  missing_dates jsonb NOT NULL DEFAULT '[]'::jsonb,
  status        varchar(16) NOT NULL DEFAULT 'success',
  message       varchar(1024) NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_calendar_check_year ON calendar_check_run (year, created_at DESC);
