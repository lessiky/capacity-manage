-- 0010: 指标管理增加"汇总指标树": 支持 汇总指标(aggregate) -> 子指标分组(group) -> 统计模块(normal) 三层结构
-- metric_kind: normal=普通指标(有采集数据) / group=子指标分组节点(无数据, 仅归类) / aggregate=汇总指标(值由子指标聚合计算)
-- parent_code: 树形父节点 (aggregate 的父必须是 aggregate 或无; group 的父必须是 aggregate; normal 的父可为 aggregate/group/无)
-- stat_module: 叶子指标所属的统计模块标识(如 count/max_latency/avg_latency), 汇总指标按该标识跨子指标聚合同模块叶子
-- 名称唯一约束调整: 同一系统内 (name, parent_code) 唯一, 允许不同子指标下存在同名统计模块(如两个"请求量")

ALTER TABLE capacity_metric ADD COLUMN IF NOT EXISTS metric_kind varchar(16) NOT NULL DEFAULT 'normal';
ALTER TABLE capacity_metric ADD COLUMN IF NOT EXISTS parent_code varchar(64) REFERENCES capacity_metric(metric_code) ON DELETE RESTRICT;
ALTER TABLE capacity_metric ADD COLUMN IF NOT EXISTS stat_module varchar(32);
COMMENT ON COLUMN capacity_metric.metric_kind IS 'normal=普通指标 group=子指标分组 aggregate=汇总指标';
COMMENT ON COLUMN capacity_metric.parent_code IS '树形父指标编码(汇总->子指标->统计模块)';
COMMENT ON COLUMN capacity_metric.stat_module IS '统计模块标识(汇总指标跨子指标聚合同一模块的叶子指标)';

-- 虚拟节点(group/aggregate)无采集, 放宽数据源类型/采集方式必填
ALTER TABLE capacity_metric ALTER COLUMN ds_type DROP NOT NULL;
ALTER TABLE capacity_metric ALTER COLUMN collect_method DROP NOT NULL;

-- 唯一约束: 同系统内 (name, parent_code) 唯一(parent 为 NULL 归一为空串)
ALTER TABLE capacity_metric DROP CONSTRAINT IF EXISTS uk_metric_name_system;
CREATE UNIQUE INDEX uk_metric_name_system_parent ON capacity_metric (system_code, name, (COALESCE(parent_code, '')));

CREATE INDEX idx_metric_parent ON capacity_metric (parent_code);
CREATE INDEX idx_metric_kind ON capacity_metric (metric_kind) WHERE metric_kind <> 'normal';
