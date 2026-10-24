-- =====================================================================
-- 二期 R2.1: 告警级别与累积升级
-- 规则: 首次阈值命中 P3；预警线累积升级 P2；危险线累积升级 P1；
--       数据质量类告警 P4。级别顺序 P1 > P2 > P3 > P4。
-- =====================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS alert_escalation_policy (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name             varchar(128) NOT NULL,
  source_line      varchar(16)  NOT NULL,
  target_level     varchar(8)   NOT NULL,
  mode             varchar(16)  NOT NULL,
  consecutive_days int,
  window_days      int,
  hit_days         int,
  day_basis        varchar(16)  NOT NULL DEFAULT 'natural',
  scope_type       varchar(16)  NOT NULL DEFAULT 'global',
  scope_value      varchar(128),
  enabled          boolean      NOT NULL DEFAULT true,
  priority         int          NOT NULL DEFAULT 100,
  created_by       varchar(64),
  created_at       timestamptz  NOT NULL DEFAULT now(),
  updated_at       timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uk_alert_escalation_policy_name UNIQUE (name),
  CONSTRAINT chk_escalation_shape CHECK (
    (mode = 'consecutive' AND consecutive_days > 0)
    OR
    (mode = 'window_count' AND window_days > 0 AND hit_days > 0 AND hit_days <= window_days)
  ),
  CONSTRAINT chk_escalation_target CHECK (target_level IN ('p1', 'p2')),
  CONSTRAINT chk_escalation_line CHECK (source_line IN ('warn', 'danger')),
  CONSTRAINT chk_escalation_scope CHECK (scope_type IN ('global', 'system', 'metric'))
);
CREATE INDEX IF NOT EXISTS idx_alert_escalation_scope
  ON alert_escalation_policy (enabled, scope_type, scope_value);

CREATE TABLE IF NOT EXISTS alert_daily_hit (
  metric_code varchar(64) NOT NULL,
  biz_date    date        NOT NULL,
  system_code varchar(64) NOT NULL,
  warn_hit    boolean     NOT NULL DEFAULT false,
  danger_hit  boolean     NOT NULL DEFAULT false,
  data_state  varchar(16) NOT NULL,
  source      varchar(16) NOT NULL DEFAULT 'collect',
  computed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (metric_code, biz_date),
  CONSTRAINT chk_alert_daily_state CHECK (data_state IN ('valid', 'missing', 'anomaly'))
);
CREATE INDEX IF NOT EXISTS idx_alert_daily_hit_date
  ON alert_daily_hit (biz_date DESC, system_code);

CREATE TABLE IF NOT EXISTS alert_level_history (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  alert_id    bigint NOT NULL REFERENCES alert_record(id) ON DELETE CASCADE,
  from_level  varchar(8),
  to_level    varchar(8) NOT NULL,
  action      varchar(24) NOT NULL DEFAULT 'escalate',
  reason      varchar(256) NOT NULL,
  evidence    jsonb NOT NULL DEFAULT '{}'::jsonb,
  policy_id   bigint REFERENCES alert_escalation_policy(id),
  operator    varchar(64) NOT NULL DEFAULT 'system',
  changed_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_alert_level_history_alert
  ON alert_level_history (alert_id, changed_at);

ALTER TABLE alert_level_history
  ADD COLUMN IF NOT EXISTS action varchar(24) NOT NULL DEFAULT 'escalate';

ALTER TABLE alert_record
  ADD COLUMN IF NOT EXISTS source_line varchar(16),
  ADD COLUMN IF NOT EXISTS escalation_policy_id bigint,
  ADD COLUMN IF NOT EXISTS escalated_at timestamptz,
  ADD COLUMN IF NOT EXISTS escalation_count int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS evidence_window jsonb;

-- 兼容一期级别: 阈值 P0/P1 → P3（后续累积升级）；数据质量类 P2 → P4。
UPDATE alert_record
SET level = CASE
  WHEN alert_type = 'threshold' AND level IN ('p0', 'p1') THEN 'p3'
  WHEN alert_type IN ('collect_fail', 'missing', 'anomaly', 'ds_error') AND level = 'p2' THEN 'p4'
  ELSE level
END,
source_line = CASE
  WHEN alert_type = 'threshold' AND level = 'p0' THEN 'danger'
  WHEN alert_type = 'threshold' AND level = 'p1' THEN COALESCE(source_line, 'warn')
  ELSE source_line
END
WHERE (alert_type = 'threshold' AND level IN ('p0', 'p1'))
   OR (alert_type IN ('collect_fail', 'missing', 'anomaly', 'ds_error') AND level = 'p2');

INSERT INTO sys_param (key, value, description, category) VALUES
  ('recover_days', '2', '连续低于预警线N个自然日后告警恢复', 'alert'),
  ('sla_claim_p1_minutes', '30', 'P1 响应时限(分钟)', 'alert'),
  ('sla_resolve_p1_minutes', '240', 'P1 闭环时限(分钟)', 'alert'),
  ('sla_claim_p2_minutes', '120', 'P2 响应时限(分钟)', 'alert'),
  ('sla_resolve_p2_minutes', '1440', 'P2 闭环时限(分钟)', 'alert'),
  ('sla_claim_p3_minutes', '240', 'P3 响应时限(分钟)', 'alert'),
  ('sla_resolve_p3_minutes', '1440', 'P3 闭环时限(分钟)', 'alert'),
  ('sla_claim_p4_minutes', '1440', 'P4 响应时限(分钟)', 'alert'),
  ('sla_resolve_p4_minutes', '7200', 'P4 闭环时限(分钟)', 'alert')
ON CONFLICT (key) DO NOTHING;

INSERT INTO alert_escalation_policy
  (name, source_line, target_level, mode, consecutive_days, window_days, hit_days, priority, created_by)
VALUES
  ('默认-预警连续3天升级P2', 'warn', 'p2', 'consecutive', 3, NULL, NULL, 100, 'system'),
  ('默认-预警7天3次升级P2', 'warn', 'p2', 'window_count', NULL, 7, 3, 90, 'system'),
  ('默认-危险连续2天升级P1', 'danger', 'p1', 'consecutive', 2, NULL, NULL, 100, 'system'),
  ('默认-危险5天2次升级P1', 'danger', 'p1', 'window_count', NULL, 5, 2, 90, 'system')
ON CONFLICT (name) DO NOTHING;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger WHERE tgname = 'trg_alert_escalation_policy_updated'
  ) THEN
    CREATE TRIGGER trg_alert_escalation_policy_updated
      BEFORE UPDATE ON alert_escalation_policy
      FOR EACH ROW EXECUTE FUNCTION set_updated_at();
  END IF;
END;
$$;

COMMIT;
