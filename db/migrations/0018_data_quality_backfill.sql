-- =====================================================================
-- 二期 R2.1: 数据质量评分与历史补采
-- =====================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS metric_quality_day (
  metric_code    varchar(64) NOT NULL,
  biz_date       date        NOT NULL,
  system_code    varchar(64) NOT NULL,
  score          numeric(6,4) NOT NULL,
  completeness   numeric(6,4) NOT NULL,
  timeliness     numeric(6,4) NOT NULL,
  anomaly_rate   numeric(6,4) NOT NULL,
  duplicate_rate numeric(6,4) NOT NULL,
  freshness      numeric(6,4) NOT NULL,
  valid_points   int NOT NULL DEFAULT 0,
  total_points   int NOT NULL DEFAULT 0,
  flags          text NOT NULL DEFAULT '[]',
  evaluated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (metric_code, biz_date)
);
CREATE INDEX IF NOT EXISTS idx_metric_quality_day_score
  ON metric_quality_day (biz_date DESC, score);

CREATE TABLE IF NOT EXISTS data_backfill_run (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  flow_id      bigint NOT NULL,
  metric_code  varchar(64) NOT NULL,
  from_date    date NOT NULL,
  to_date      date NOT NULL,
  source       varchar(16) NOT NULL DEFAULT 'backfill',
  status       varchar(16) NOT NULL DEFAULT 'running',
  total_days   int NOT NULL DEFAULT 0,
  success_days int NOT NULL DEFAULT 0,
  failed_days  int NOT NULL DEFAULT 0,
  error        varchar(1024) NOT NULL DEFAULT '',
  created_by   varchar(64),
  started_at   timestamptz,
  finished_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_backfill_range CHECK (to_date >= from_date)
);
CREATE INDEX IF NOT EXISTS idx_data_backfill_run_metric
  ON data_backfill_run (metric_code, from_date DESC);

CREATE TABLE IF NOT EXISTS data_backfill_item (
  id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_id              bigint NOT NULL REFERENCES data_backfill_run(id) ON DELETE CASCADE,
  biz_date            date NOT NULL,
  status              varchar(16) NOT NULL,
  error               varchar(1024) NOT NULL DEFAULT '',
  summary_refreshed   boolean NOT NULL DEFAULT false,
  quality_refreshed   boolean NOT NULL DEFAULT false,
  alert_replayed      boolean NOT NULL DEFAULT false,
  report_invalidated  boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_data_backfill_item_run
  ON data_backfill_item (run_id, biz_date);

INSERT INTO sys_param (key, value, description, category) VALUES
  ('quality_score_threshold', '0.8', '质量分低于该值标记为不足', 'data'),
  ('quality_timely_hours', '24', '日数据在该小时内落库视为及时', 'data')
ON CONFLICT (key) DO NOTHING;

COMMIT;
