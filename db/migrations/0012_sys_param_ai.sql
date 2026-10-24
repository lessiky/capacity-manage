-- 0012: AI 服务配置热参数（系统管理 → AI 配置, 页面保存后免重启生效）
-- 约定: value 为空 = 跟随环境变量(AI_ENABLED/AI_BASE_URL/AI_MODEL/AI_API_KEY/AI_TIMEOUT_SEC)
INSERT INTO sys_param (key, value, description, category) VALUES
  ('ai.enabled',    '', 'AI 功能开关(true/false, 空=跟随环境变量 AI_ENABLED)', 'ai'),
  ('ai.base_url',   '', 'OpenAI 兼容服务 Base URL(空=跟随环境变量 AI_BASE_URL)', 'ai'),
  ('ai.model',      '', '模型名称(空=跟随环境变量 AI_MODEL)', 'ai'),
  ('ai.api_key',    '', 'API Key(空=跟随环境变量 AI_API_KEY)', 'ai'),
  ('ai.timeout_sec','', '请求超时秒数(空=跟随环境变量 AI_TIMEOUT_SEC)', 'ai')
ON CONFLICT (key) DO NOTHING;
