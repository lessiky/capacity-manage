-- 0006: M3 告警闭环 - SLA 响应时限字段（一期仅记录语义, 不产生升级通知）
ALTER TABLE alert_record ADD COLUMN IF NOT EXISTS deadline_at timestamptz;
COMMENT ON COLUMN alert_record.deadline_at IS 'SLA 响应时限 = trigger_at + 级别响应目标(P0 30min/P1 2h/P2 4h, 可配); 一期仅记录, 超时升级属二期';
