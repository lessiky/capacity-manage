-- 0003: sys_operate_log.detail 改 text（应用层以纯字符串写入, jsonb 导致 INSERT 类型不匹配而静默丢失）
ALTER TABLE sys_operate_log ALTER COLUMN detail TYPE text USING detail::text;
COMMENT ON COLUMN sys_operate_log.detail IS '操作详情（纯文本描述）';
