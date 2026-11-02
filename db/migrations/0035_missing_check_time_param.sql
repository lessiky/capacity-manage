-- 缺数检测执行时间此前硬编码在调度器中，补齐为可热配置的系统参数。
BEGIN;

INSERT INTO sys_param (key, value, description, category) VALUES
  ('missing_check_time', '09:00', '每日缺数检测执行时间(HH:MM, 平台时区)', 'schedule')
ON CONFLICT (key) DO NOTHING;

COMMIT;
