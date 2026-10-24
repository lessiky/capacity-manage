-- 0005: M2 测试反馈 - 交易日调度"收市后窗口"改为"非交易时间"
-- 支持范围 "HH:MM-HH:MM"（如 00:00-09:00, 窗口起点触发）, 兼容旧版单时刻 "HH:MM"
ALTER TABLE collect_flow ALTER COLUMN window_time TYPE varchar(16);
COMMENT ON COLUMN collect_flow.window_time IS 'trading_day 模式非交易时间: 范围 "HH:MM-HH:MM"(窗口起点触发) 或旧版单时刻 "HH:MM"';
