-- M8 归档下线与按需恢复（FR-DAY-06）
--
-- 归档语义从"仅复制"升级为"归档成功后下线在线分区":
-- 1) online_dropped/dropped_at 记录在线分区是否已 DETACH+DROP;
-- 2) restored_at 记录最近一次从归档回填到在线的时间;
-- 3) payload_version 标记归档文件载荷版本, v1 为旧的仅日数据格式,
--    v2 补齐日内数据 ts 与日数据修正字段, 避免"只剩归档"后丢字段。

ALTER TABLE metric_archive_run
  ADD COLUMN IF NOT EXISTS online_dropped boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS dropped_at timestamptz,
  ADD COLUMN IF NOT EXISTS restored_at timestamptz,
  ADD COLUMN IF NOT EXISTS payload_version int NOT NULL DEFAULT 1;

COMMENT ON COLUMN metric_archive_run.online_dropped IS '归档成功后在线分区是否已下线(DETACH+DROP)';
COMMENT ON COLUMN metric_archive_run.dropped_at IS '在线分区下线时间';
COMMENT ON COLUMN metric_archive_run.restored_at IS '最近一次从归档恢复到在线的时间';
COMMENT ON COLUMN metric_archive_run.payload_version IS '归档文件载荷版本: 1=旧版仅日数据, 2=含 ts 与修正字段';

CREATE INDEX IF NOT EXISTS idx_metric_archive_drop_state
  ON metric_archive_run (table_name, status, online_dropped);
