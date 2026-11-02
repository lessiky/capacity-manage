-- 0038: 清理旧版自动日汇总在非交易日写入的伪缺数。
-- calendar/natural 的交易日调度只会在交易日产生业务日；历史代码曾按自然日 anchor
-- 落过 level=missing 的空行，这些行会污染数据管理/日报。仅删除无值、未人工修正、
-- 对应流程仍为交易日调度且日历明确为非交易日的行；prev_day 与真实缺数全部保留。
BEGIN;

DELETE FROM metric_value_day
WHERE level = 'missing'
  AND stat_value IS NULL
  AND point_count = 0
  AND corrected = false
  AND EXISTS (
    SELECT 1
    FROM collect_flow f
    WHERE f.metric_code = metric_value_day.metric_code
      AND f.schedule_mode = 'trading_day'
      AND f.biz_date_mode IN ('calendar', 'natural')
      AND EXISTS (
        SELECT 1
        FROM trading_day_calendar c
        WHERE c.market = COALESCE(NULLIF(f.trading_market, ''), 'SH')
          AND c.cal_date = metric_value_day.biz_date
          AND c.is_trading = false
      )
  );

COMMIT;
