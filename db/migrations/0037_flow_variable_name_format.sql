-- 0037: 放宽公共自定义变量名格式，支持大小写字母、数字和下划线。
ALTER TABLE flow_variable DROP CONSTRAINT IF EXISTS ck_flow_variable_name_format;
ALTER TABLE flow_variable
  ADD CONSTRAINT ck_flow_variable_name_format CHECK (name ~ '^[A-Za-z0-9_]+$');

COMMENT ON COLUMN flow_variable.name IS '模板变量名; 仅大小写字母/数字/下划线, 不得与内置变量重名';
