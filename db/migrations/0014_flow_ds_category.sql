-- 0014: 采集流程支持"数据源分类"归属(日志采集)
-- 需求: 日志采集配置时可选择数据源分类, 该分类下全部日志数据源的文件使用同一处理逻辑(同一采集/清洗配置);
--       流程的数据源与数据源分类二选一(存量流程仍为单数据源)。
-- 另: 采集预览返回数据日期 bizDate(应用层, 无 DDL)。

ALTER TABLE collect_flow ALTER COLUMN data_source_id DROP NOT NULL;
ALTER TABLE collect_flow ADD COLUMN IF NOT EXISTS data_source_category_id bigint;
ALTER TABLE collect_flow DROP CONSTRAINT IF EXISTS fk_collect_flow_ds_category;
ALTER TABLE collect_flow ADD CONSTRAINT fk_collect_flow_ds_category FOREIGN KEY (data_source_category_id) REFERENCES data_source_category(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS idx_collect_flow_ds_category ON collect_flow (data_source_category_id);
COMMENT ON COLUMN collect_flow.data_source_id IS '数据源(与 data_source_category_id 二选一)';
COMMENT ON COLUMN collect_flow.data_source_category_id IS '数据源分类(logparse 专用: 分类下全部日志数据源共用同一处理逻辑, 与 data_source_id 二选一)';
