-- 0039: 清理交易日调度指标在非交易日产生的伪质量评分。
-- calendar/natural 的业务日必须是交易日；旧版质量服务按自然日评分，曾把周末
-- 无数据写成 0 分。只删除明确非交易日的评分行，prev_day 与普通调度保持不变。
BEGIN;

DELETE FROM metric_quality_day
WHERE EXISTS (
  SELECT 1
  FROM collect_flow f
  WHERE f.metric_code = metric_quality_day.metric_code
    AND f.schedule_mode = 'trading_day'
    AND f.biz_date_mode IN ('calendar', 'natural')
    AND EXISTS (
      SELECT 1
      FROM trading_day_calendar c
      WHERE c.market = COALESCE(NULLIF(f.trading_market, ''), 'SH')
        AND c.cal_date = metric_quality_day.biz_date
        AND c.is_trading = false
    )
);

COMMIT;
