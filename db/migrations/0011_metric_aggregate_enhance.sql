-- 0011: 汇总指标增强: 主统计模块(阈值告警/日值口径) + 权重指标(加权均值)
-- main_module: 汇总指标的"主统计模块"标识(如 count/max_latency/avg_latency), 阈值告警与日值/水位按该模块的聚合值计算; 空=取首个模块
-- weight_metric_code: 叶子指标的权重指标(如"平均耗时"按"请求量"加权), 汇总时按 加权均值=sum(v*w)/sum(w) 计算; 空=简单均值
ALTER TABLE capacity_metric ADD COLUMN IF NOT EXISTS main_module varchar(32);
COMMENT ON COLUMN capacity_metric.main_module IS '汇总指标主统计模块(阈值告警/日值/水位口径); 空=首个模块';
ALTER TABLE capacity_metric ADD COLUMN IF NOT EXISTS weight_metric_code varchar(64) REFERENCES capacity_metric(metric_code) ON DELETE SET NULL;
CREATE INDEX idx_metric_weight ON capacity_metric (weight_metric_code) WHERE weight_metric_code IS NOT NULL;
COMMENT ON COLUMN capacity_metric.weight_metric_code IS '权重指标(加权均值: 如平均耗时按请求量加权); 空=简单均值';
