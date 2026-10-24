-- 日报自动生成时间此前仅存在于调度器默认值中，补齐为可热配置的系统参数。
BEGIN;

INSERT INTO sys_param (key, value, description, category) VALUES
  ('daily_report_time', '00:30', '每日日报自动生成时间(HH:MM, 平台时区)', 'schedule')
ON CONFLICT (key) DO NOTHING;

COMMIT;
