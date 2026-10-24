CREATE TABLE IF NOT EXISTS report_definition (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name              varchar(128) NOT NULL,
  report_type       varchar(32) NOT NULL,
  system_scope      jsonb NOT NULL DEFAULT '[]'::jsonb,
  metric_scope      jsonb NOT NULL DEFAULT '[]'::jsonb,
  group_dimension   varchar(32) NOT NULL DEFAULT '',
  sort_by           varchar(32) NOT NULL DEFAULT '',
  sort_desc         boolean NOT NULL DEFAULT true,
  include_forecast  boolean NOT NULL DEFAULT false,
  period_window     int NOT NULL DEFAULT 30,
  chart_options     jsonb NOT NULL DEFAULT '{}'::jsonb,
  enabled           boolean NOT NULL DEFAULT true,
  created_by        varchar(64) NOT NULL DEFAULT '',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_report_definition_name UNIQUE (name)
);

CREATE INDEX IF NOT EXISTS idx_report_definition_type ON report_definition (report_type, enabled);

CREATE TABLE IF NOT EXISTS report_run (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  definition_id  bigint NOT NULL REFERENCES report_definition(id) ON DELETE CASCADE,
  run_date       date NOT NULL,
  period_start   date NOT NULL,
  period_end     date NOT NULL,
  status         varchar(16) NOT NULL DEFAULT 'running',
  metrics_total  int NOT NULL DEFAULT 0,
  files          jsonb NOT NULL DEFAULT '{}'::jsonb,
  error_msg      varchar(2048) NOT NULL DEFAULT '',
  generated_by   varchar(64) NOT NULL DEFAULT '',
  started_at     timestamptz,
  finished_at    timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_report_run_definition_date ON report_run (definition_id, run_date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_report_run_status ON report_run (status, created_at DESC);

CREATE TABLE IF NOT EXISTS report_subscription (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name           varchar(128) NOT NULL,
  definition_id  bigint NOT NULL REFERENCES report_definition(id) ON DELETE CASCADE,
  recipients     jsonb NOT NULL DEFAULT '[]'::jsonb,
  channel_ids    jsonb NOT NULL DEFAULT '[]'::jsonb,
  schedule_type  varchar(16) NOT NULL DEFAULT 'daily',
  schedule_time  varchar(8) NOT NULL DEFAULT '08:30',
  weekday        int NOT NULL DEFAULT 1,
  day_of_month   int NOT NULL DEFAULT 1,
  timezone       varchar(64) NOT NULL DEFAULT 'Asia/Shanghai',
  enabled        boolean NOT NULL DEFAULT true,
  last_run_at    timestamptz,
  next_run_at    timestamptz,
  created_by     varchar(64) NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_report_subscription_schedule CHECK (
    (schedule_type = 'daily')
    OR (schedule_type = 'weekly' AND weekday BETWEEN 1 AND 7)
    OR (schedule_type = 'monthly' AND day_of_month BETWEEN 1 AND 28)
  )
);

CREATE INDEX IF NOT EXISTS idx_report_subscription_due ON report_subscription (enabled, next_run_at);

INSERT INTO report_definition
  (name, report_type, group_dimension, sort_by, sort_desc, include_forecast, period_window)
VALUES
  ('容量周报', 'weekly', 'system', 'growthRate', true, true, 7),
  ('容量月报', 'monthly', 'system', 'growthRate', true, true, 30),
  ('容量使用分析', 'capacity_analysis', 'system', 'waterLevel', true, false, 30),
  ('告警汇总报表', 'alert_summary', 'system', 'alertCount', true, false, 30),
  ('容量预测报表', 'forecast', 'system', 'riskScore', true, true, 90),
  ('容量日报', 'daily', 'system', 'waterLevel', true, true, 1)
ON CONFLICT (name) DO NOTHING;
