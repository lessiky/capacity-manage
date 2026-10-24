-- 0008: 数据日期增加"上日"(prev_day = 上一个自然日)
-- 列本身为 varchar(16) 无约束, 仅更新注释; 取值: natural=当日 / prev_day=上日 / calendar=上一交易日

COMMENT ON COLUMN collect_flow.biz_date_mode IS '数据日期: natural=当日 / prev_day=上日(前一自然日) / calendar=上一交易日(决定 ${biz_date} 变量与数据落地业务日)';