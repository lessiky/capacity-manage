-- 0009: 指标详情"历史峰值"落地表（用户反馈: 峰值须持久化, 历史数据删除后仍准确）
-- 口径与日粒度趋势一致(日数据 stat_value 序列); 日汇总/手工修正后自动维护, 存量指标首读惰性初始化

CREATE TABLE metric_peak_stat (
  metric_code  varchar(64) NOT NULL PRIMARY KEY,
  max_value    numeric(24,8),
  max_biz_date date,
  min_value    numeric(24,8),
  min_biz_date date,
  updated_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE metric_peak_stat IS '指标历史峰值/谷值记录(自动落地, 日数据删除后记录保留)';
COMMENT ON COLUMN metric_peak_stat.max_biz_date IS '历史峰值出现的最早日期';
COMMENT ON COLUMN metric_peak_stat.min_biz_date IS '历史谷值出现的最早日期';