CREATE TABLE IF NOT EXISTS forecast_run (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_date         date NOT NULL,
  model_version    varchar(32) NOT NULL,
  data_end_date    date NOT NULL,
  status           varchar(16) NOT NULL DEFAULT 'running',
  metrics_total    int NOT NULL DEFAULT 0,
  metrics_done     int NOT NULL DEFAULT 0,
  metrics_skipped  int NOT NULL DEFAULT 0,
  ai_enabled       boolean NOT NULL DEFAULT false,
  started_at       timestamptz,
  finished_at      timestamptz,
  error_msg        varchar(2048) NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_forecast_run_date UNIQUE (run_date)
);

CREATE INDEX IF NOT EXISTS idx_forecast_run_status ON forecast_run (status, run_date DESC);

CREATE TABLE IF NOT EXISTS forecast_result (
  id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_id               bigint NOT NULL REFERENCES forecast_run(id) ON DELETE CASCADE,
  metric_code          varchar(64) NOT NULL,
  horizon_days         int NOT NULL,
  model_type           varchar(32) NOT NULL,
  status               varchar(24) NOT NULL,
  eta_warn_date        date,
  eta_danger_date      date,
  risk_score           numeric(6,2),
  confidence           numeric(5,4),
  slope                numeric(24,8),
  intercept            numeric(24,8),
  residual_stddev      numeric(24,8),
  data_quality         numeric(5,4),
  event_factor_version varchar(32) NOT NULL DEFAULT '',
  explanation          varchar(2048) NOT NULL DEFAULT '',
  curve                jsonb NOT NULL DEFAULT '[]'::jsonb,
  input_digest         varchar(128) NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_forecast_result UNIQUE (run_id, metric_code, horizon_days)
);

CREATE INDEX IF NOT EXISTS idx_forecast_result_risk ON forecast_result (run_id, risk_score DESC);
CREATE INDEX IF NOT EXISTS idx_forecast_result_eta ON forecast_result (eta_danger_date, status);
CREATE INDEX IF NOT EXISTS idx_forecast_result_metric ON forecast_result (metric_code, horizon_days, created_at DESC);

CREATE TABLE IF NOT EXISTS forecast_event (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name              varchar(128) NOT NULL,
  event_type        varchar(32) NOT NULL,
  scope_type        varchar(16) NOT NULL,
  scope_value       varchar(128) NOT NULL DEFAULT '',
  start_date        date NOT NULL,
  end_date          date,
  impact_direction  varchar(16) NOT NULL,
  impact_pct        numeric(8,4),
  confidence        numeric(5,4),
  status            varchar(16) NOT NULL DEFAULT 'draft',
  ai_suggestion     jsonb,
  description       varchar(1024) NOT NULL DEFAULT '',
  created_by        varchar(64) NOT NULL,
  confirmed_by      varchar(64) NOT NULL DEFAULT '',
  confirmed_at      timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_forecast_event_scope ON forecast_event (scope_type, scope_value, start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_forecast_event_status ON forecast_event (status, start_date);
