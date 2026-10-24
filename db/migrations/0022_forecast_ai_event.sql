-- M8 Task 9: AI 辅助预测与事件因子
--
-- 1) forecast_event 增加版本追踪与过期字段（事件变更生成新版本，过期转 expired）
-- 2) forecast_ai_adjustment 保存 AI 调节前后结果，便于对比与回滚
--
-- 事件因子仍复用 0021 已建的 forecast_event 表，本迁移只做向前兼容的加列。

ALTER TABLE forecast_event
  ADD COLUMN IF NOT EXISTS version        int NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS root_id        bigint,
  ADD COLUMN IF NOT EXISTS superseded_by  bigint,
  ADD COLUMN IF NOT EXISTS expires_at     timestamptz,
  ADD COLUMN IF NOT EXISTS ai_model       varchar(128) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS ai_generated_at timestamptz;

COMMENT ON COLUMN forecast_event.version IS '事件版本号, 每次变更 +1';
COMMENT ON COLUMN forecast_event.root_id IS '同一事件的首版 id, 首版等于自身';
COMMENT ON COLUMN forecast_event.superseded_by IS '被哪一版取代, 非空表示历史版本';
COMMENT ON COLUMN forecast_event.expires_at IS '过期时刻, 到期后由调度或读取时置 expired';

CREATE INDEX IF NOT EXISTS idx_forecast_event_root ON forecast_event (root_id, version DESC);
CREATE INDEX IF NOT EXISTS idx_forecast_event_expiry ON forecast_event (status, end_date);

-- AI 调节前后结果并存，便于对比与回滚（FR-AI-05）
CREATE TABLE IF NOT EXISTS forecast_ai_adjustment (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  result_id         bigint NOT NULL REFERENCES forecast_result(id) ON DELETE CASCADE,
  run_id            bigint NOT NULL,
  metric_code       varchar(64) NOT NULL,
  horizon_days      int NOT NULL,
  ai_enabled        boolean NOT NULL DEFAULT true,
  status            varchar(24) NOT NULL,
  model             varchar(128) NOT NULL DEFAULT '',
  slope_multiplier  numeric(8,4),
  intercept_adj_pct numeric(8,4),
  confidence_adj    numeric(6,4),
  rationale         varchar(1024) NOT NULL DEFAULT '',
  base_snapshot     jsonb NOT NULL DEFAULT '{}'::jsonb,
  adjusted_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
  eta_shift_days    int,
  advisory_only     boolean NOT NULL DEFAULT false,
  latency_ms        int,
  error_msg         varchar(1024) NOT NULL DEFAULT '',
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_forecast_ai_adjustment UNIQUE (result_id)
);

CREATE INDEX IF NOT EXISTS idx_forecast_ai_adjustment_run ON forecast_ai_adjustment (run_id, metric_code);
CREATE INDEX IF NOT EXISTS idx_forecast_ai_adjustment_status ON forecast_ai_adjustment (status, created_at DESC);
COMMENT ON TABLE forecast_ai_adjustment IS 'AI 辅助修正前后对比(FR-AI-05), 超幅调整仅作建议';

-- 新增权限点：事件因子管理（FR-AI-06）。授权平台管理员/运维/系统负责人，
-- 查看沿用既有 metric:view，避免为只读角色放开写入口。
INSERT INTO sys_permission (code, name, type, path)
VALUES ('forecast:event:manage', '事件因子管理', 'button', '')
ON CONFLICT (code) DO NOTHING;

INSERT INTO sys_role_permission (role_id, permission_id)
SELECT r.id, p.id
FROM sys_role r
JOIN sys_permission p ON p.code = 'forecast:event:manage'
WHERE r.code IN ('platform_admin', 'ops', 'system_owner')
ON CONFLICT DO NOTHING;
