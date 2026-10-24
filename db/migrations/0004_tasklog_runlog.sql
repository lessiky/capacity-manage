-- 0004: M2 采集引擎 - 执行日志/异常堆栈全量记录 (FR-COL-19)
ALTER TABLE collect_task_log ADD COLUMN IF NOT EXISTS run_log text;
COMMENT ON COLUMN collect_task_log.run_log IS 'Python 脚本执行日志(stdout/stderr/异常类型, 截断保存)';
