-- 0002: 数据源连续健康检查失败计数（FR-COL-04 连续异常通知）
ALTER TABLE data_source ADD COLUMN IF NOT EXISTS health_fail_streak int NOT NULL DEFAULT 0;
COMMENT ON COLUMN data_source.health_fail_streak IS '连续健康检查失败次数（成功清零; 达到 ds_health_alert_streak 阈值时生成平台内 P2 告警）';
