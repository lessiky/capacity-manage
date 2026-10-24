-- 平台品牌可在“系统管理 → 系统参数”中热配置；图标保存为 Data URL。
BEGIN;

INSERT INTO sys_param (key, value, description, category) VALUES
  ('platform_name', '容量管理平台', '平台显示名称', 'general'),
  ('platform_icon', '', '平台图标(上传图片后自动保存 Data URL)', 'general')
ON CONFLICT (key) DO NOTHING;

COMMIT;
