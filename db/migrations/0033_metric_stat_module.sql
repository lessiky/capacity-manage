-- 0033: 统计模块级汇总方式。普通指标 stat_mode 继续负责日内取值,
-- 汇总指标跨子指标按 (aggregate_code, module_key) 独立配置 sum/max/min/avg/weighted_avg。
CREATE TABLE IF NOT EXISTS metric_stat_module (
  aggregate_code varchar(64) NOT NULL REFERENCES capacity_metric(metric_code) ON DELETE CASCADE,
  module_key     varchar(32) NOT NULL,
  agg_mode       varchar(16) NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (aggregate_code, module_key),
  CONSTRAINT ck_metric_stat_module_agg_mode
    CHECK (agg_mode IN ('sum', 'max', 'min', 'avg', 'weighted_avg'))
);
COMMENT ON TABLE metric_stat_module IS '汇总指标统计模块配置(模块级跨子指标汇总方式)';
COMMENT ON COLUMN metric_stat_module.agg_mode IS 'sum/max/min/avg/weighted_avg';

DROP TRIGGER IF EXISTS trg_metric_stat_module_updated ON metric_stat_module;
CREATE TRIGGER trg_metric_stat_module_updated
  BEFORE UPDATE ON metric_stat_module
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
