-- 0007: 采集流程配置调整（用户反馈）
-- 1) 采集配置支持"数据日期"(biz_date_mode: natural=当日 / calendar=上一交易日)
--    存量 trading_day 模式流程保持"上一交易日"口径, 行为不变
-- 2) 非交易时间由范围改为单时点(15:00 至次日 09:00, 15 分钟间隔); 存量范围值保留触发推导兼容

UPDATE collect_flow
   SET biz_date_mode = 'calendar'
 WHERE schedule_mode = 'trading_day'
   AND biz_date_mode <> 'calendar';

COMMENT ON COLUMN collect_flow.biz_date_mode IS '数据日期: natural=当日 / calendar=上一交易日(决定 ${biz_date} 变量与数据落地业务日)';
COMMENT ON COLUMN collect_flow.window_time IS 'trading_day 模式非交易时点: 单 "HH:MM"(15:00 至次日 09:00, 15 分钟间隔); 存量范围 "HH:MM-HH:MM" 仅兼容触发推导';