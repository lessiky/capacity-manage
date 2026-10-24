-- =====================================================================
-- 二期 R2.1: 通知渠道、模板、订阅、静默与发送日志
-- =====================================================================

BEGIN;

ALTER TABLE alert_level_history
  ADD COLUMN IF NOT EXISTS action varchar(24) NOT NULL DEFAULT 'escalate';

INSERT INTO sys_permission (code, name, type, path, sort)
VALUES
  ('alert:policy:manage', '告警升级策略管理', 'button', '', 410),
  ('notification:view', '通知配置查看', 'menu', '/notifications', 420),
  ('notification:manage', '通知配置管理', 'button', '', 421)
ON CONFLICT (code) DO NOTHING;

INSERT INTO sys_role_permission (role_id, permission_id)
SELECT r.id, p.id
FROM sys_role r
JOIN sys_permission p ON p.code IN ('alert:policy:manage','notification:view','notification:manage')
WHERE r.code IN ('platform_admin','ops','system_owner')
ON CONFLICT DO NOTHING;

INSERT INTO sys_role_permission (role_id, permission_id)
SELECT r.id, p.id
FROM sys_role r
JOIN sys_permission p ON p.code IN ('notification:view')
WHERE r.code IN ('biz_owner','viewer')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS notification_channel (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name          varchar(128) NOT NULL UNIQUE,
  type          varchar(24) NOT NULL,
  config        text NOT NULL DEFAULT '',
  enabled       boolean NOT NULL DEFAULT true,
  health_status varchar(16) NOT NULL DEFAULT 'unknown',
  last_error    varchar(1024) NOT NULL DEFAULT '',
  last_test_at  timestamptz,
  created_by    varchar(64),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_notification_channel_type CHECK (type IN ('smtp','wecom','dingtalk','feishu','webhook'))
);

CREATE TABLE IF NOT EXISTS notification_template (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name         varchar(128) NOT NULL UNIQUE,
  event        varchar(24) NOT NULL,
  channel_type varchar(24) NOT NULL,
  format       varchar(16) NOT NULL DEFAULT 'text',
  subject      varchar(256) NOT NULL DEFAULT '',
  body         text NOT NULL,
  variables    text NOT NULL DEFAULT '[]',
  version      int NOT NULL DEFAULT 1,
  enabled      boolean NOT NULL DEFAULT true,
  created_by   varchar(64),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_notification_template_event UNIQUE (event, channel_type),
  CONSTRAINT chk_notification_template_event CHECK (event IN ('alert','recovered','escalate','report')),
  CONSTRAINT chk_notification_template_format CHECK (format IN ('text','markdown','card'))
);

CREATE TABLE IF NOT EXISTS notification_subscription (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name             varchar(128) NOT NULL UNIQUE,
  scope_type       varchar(16) NOT NULL DEFAULT 'global',
  scope_value      varchar(128) NOT NULL DEFAULT '',
  levels           varchar(32) NOT NULL DEFAULT 'p1,p2,p3,p4',
  alert_types      varchar(256) NOT NULL DEFAULT '*',
  recipients       text NOT NULL DEFAULT '[]',
  channel_ids      text NOT NULL DEFAULT '[]',
  silent_start     varchar(8) NOT NULL DEFAULT '',
  silent_end       varchar(8) NOT NULL DEFAULT '',
  dedup_window_min int NOT NULL DEFAULT 60,
  enabled          boolean NOT NULL DEFAULT true,
  created_by       varchar(64),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_notification_scope CHECK (scope_type IN ('global','system','metric')),
  CONSTRAINT chk_notification_dedup CHECK (dedup_window_min >= 0)
);
CREATE INDEX IF NOT EXISTS idx_notification_subscription_scope
  ON notification_subscription (enabled, scope_type, scope_value);

CREATE TABLE IF NOT EXISTS notification_silence (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  scope_type  varchar(16) NOT NULL,
  scope_value varchar(128) NOT NULL DEFAULT '',
  start_at    timestamptz NOT NULL,
  end_at      timestamptz NOT NULL,
  reason      varchar(512) NOT NULL DEFAULT '',
  created_by  varchar(64),
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_notification_silence_scope CHECK (scope_type IN ('global','system','metric')),
  CONSTRAINT chk_notification_silence_period CHECK (end_at > start_at)
);
CREATE INDEX IF NOT EXISTS idx_notification_silence_active
  ON notification_silence (scope_type, scope_value, start_at, end_at);

CREATE TABLE IF NOT EXISTS alert_notification_log (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  alert_id        bigint NOT NULL,
  alert_no        varchar(32) NOT NULL,
  channel_id      bigint NOT NULL REFERENCES notification_channel(id),
  channel_type    varchar(24) NOT NULL,
  target          varchar(1024) NOT NULL DEFAULT '',
  level           varchar(8) NOT NULL,
  event           varchar(24) NOT NULL,
  template_id     bigint REFERENCES notification_template(id),
  template_ver    int NOT NULL DEFAULT 0,
  dedupe_key      varchar(256) NOT NULL UNIQUE,
  status          varchar(16) NOT NULL,
  attempts        int NOT NULL DEFAULT 0,
  max_attempts    int NOT NULL DEFAULT 4,
  next_retry_at   timestamptz,
  payload         text NOT NULL DEFAULT '{}',
  error           varchar(1024) NOT NULL DEFAULT '',
  sent_at         timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_notification_log_status CHECK (status IN ('queued','sent','retrying','failed','suppressed'))
);
CREATE INDEX IF NOT EXISTS idx_alert_notification_log_retry
  ON alert_notification_log (status, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_alert_notification_log_alert
  ON alert_notification_log (alert_no, level, created_at DESC);

INSERT INTO notification_template
  (name, event, channel_type, format, subject, body, variables, version, created_by)
VALUES
  ('默认-告警-通用', 'alert', '*', 'markdown', '容量告警 ${level} ${metricCode}',
   '**${systemCode} / ${metricCode}**\n级别: **${level}**\n当前值: ${value}\n阈值: ${threshold}\n时间: ${triggerAt}',
   '["systemCode","metricCode","level","value","threshold","triggerAt","link"]', 1, 'system'),
  ('默认-升级-通用', 'escalate', '*', 'markdown', '容量告警升级 ${level} ${metricCode}',
   '**${systemCode} / ${metricCode}** 已升级为 **${level}**\n证据: ${evidenceWindow}\n时间: ${triggerAt}',
   '["systemCode","metricCode","level","evidenceWindow","triggerAt","link"]', 1, 'system'),
  ('默认-恢复-通用', 'recovered', '*', 'markdown', '容量告警恢复 ${metricCode}',
   '**${systemCode} / ${metricCode}** 已恢复\n时间: ${recoveredAt}',
   '["systemCode","metricCode","recoveredAt","link"]', 1, 'system')
ON CONFLICT (event, channel_type) DO NOTHING;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_notification_channel_updated') THEN
    CREATE TRIGGER trg_notification_channel_updated BEFORE UPDATE ON notification_channel
      FOR EACH ROW EXECUTE FUNCTION set_updated_at();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_notification_template_updated') THEN
    CREATE TRIGGER trg_notification_template_updated BEFORE UPDATE ON notification_template
      FOR EACH ROW EXECUTE FUNCTION set_updated_at();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_notification_subscription_updated') THEN
    CREATE TRIGGER trg_notification_subscription_updated BEFORE UPDATE ON notification_subscription
      FOR EACH ROW EXECUTE FUNCTION set_updated_at();
  END IF;
END;
$$;

COMMIT;
