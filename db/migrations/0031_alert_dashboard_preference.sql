-- Alert center custom dashboard preferences, one row per user.
CREATE TABLE IF NOT EXISTS alert_dashboard_preference (
  user_id      bigint PRIMARY KEY REFERENCES sys_user(id) ON DELETE CASCADE,
  levels       jsonb NOT NULL DEFAULT '["p1","p2","p3","p4"]',
  statuses     jsonb NOT NULL DEFAULT '["active","processing","recovered","closed"]',
  alert_types  jsonb NOT NULL DEFAULT '["threshold","missing","collect_fail","anomaly","ds_error"]',
  system_codes jsonb NOT NULL DEFAULT '[]',
  date_mode    varchar(16) NOT NULL DEFAULT 'today',
  recent_days  int NOT NULL DEFAULT 7,
  sort_rules   jsonb NOT NULL DEFAULT '[{"field":"triggerAt","direction":"desc"}]',
  created_at   timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at   timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT chk_alert_dashboard_date_mode CHECK (date_mode IN ('today', 'recent_days')),
  CONSTRAINT chk_alert_dashboard_recent_days CHECK (recent_days BETWEEN 1 AND 365)
);

CREATE INDEX IF NOT EXISTS idx_alert_dashboard_updated_at
  ON alert_dashboard_preference (updated_at DESC);
