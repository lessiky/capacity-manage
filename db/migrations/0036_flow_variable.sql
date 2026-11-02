-- 0036: 采集流程公共自定义变量。变量值以字面量参与 SQL/InfluxQL/日志 glob/脚本参数渲染。
CREATE TABLE IF NOT EXISTS flow_variable (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name        varchar(64) NOT NULL,
  description varchar(512),
  value       text NOT NULL DEFAULT '',
  created_by  varchar(64),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_flow_variable_name UNIQUE (name),
  CONSTRAINT ck_flow_variable_name_format CHECK (name ~ '^[A-Za-z0-9_]+$'),
  CONSTRAINT ck_flow_variable_name_not_builtin CHECK (
    name NOT IN (
      'biz_date', 'prev_trading_day', 'next_trading_day', 'trading_day_offset',
      'system_code', 'flow_code', 'metric_code', 'hour'
    )
  )
);
COMMENT ON TABLE flow_variable IS '采集流程公共自定义变量';
COMMENT ON COLUMN flow_variable.name IS '模板变量名; 仅大小写字母/数字/下划线, 不得与内置变量重名';
COMMENT ON COLUMN flow_variable.description IS '变量说明';
COMMENT ON COLUMN flow_variable.value IS '变量值, 按字面量替换';

DROP TRIGGER IF EXISTS trg_flow_variable_updated ON flow_variable;
CREATE TRIGGER trg_flow_variable_updated
  BEFORE UPDATE ON flow_variable
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
