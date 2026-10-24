-- =====================================================================
-- 二期 R2.1: 告警升级策略作用域支持多选
-- scope_value 由单值 text 改为 JSON 数组(jsonb):
--   global -> []           （空数组, 规则全局生效）
--   system -> ["SYS_A","SYS_B"]
--   metric -> ["SYS_A.MET_1","SYS_B.MET_2"]
-- 历史单值自动包成单元素数组；作用域匹配改由应用层完成（policyMatchesScope）。
-- =====================================================================

BEGIN;

-- 旧索引建立在 text 列上, 先删除再随列类型重建
DROP INDEX IF EXISTS idx_alert_escalation_scope;

ALTER TABLE alert_escalation_policy
  ALTER COLUMN scope_value TYPE jsonb
  USING (
    CASE
      WHEN scope_value IS NULL OR btrim(scope_value) = '' THEN '[]'::jsonb
      ELSE jsonb_build_array(scope_value)
    END
  );

ALTER TABLE alert_escalation_policy ALTER COLUMN scope_value SET DEFAULT '[]'::jsonb;

CREATE INDEX IF NOT EXISTS idx_alert_escalation_scope
  ON alert_escalation_policy (enabled, scope_type, scope_value);

COMMIT;
