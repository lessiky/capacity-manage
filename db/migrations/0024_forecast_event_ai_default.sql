-- M8 补丁: forecast_event.ai_suggestion 为 jsonb, 早期插入未带默认值会写空串,
-- 触发 "invalid input syntax for type json"。改为库级默认空对象, 与应用层一并兜底。

ALTER TABLE forecast_event
  ALTER COLUMN ai_suggestion SET DEFAULT '{}'::jsonb;

UPDATE forecast_event
   SET ai_suggestion = '{}'::jsonb
 WHERE ai_suggestion IS NULL;
