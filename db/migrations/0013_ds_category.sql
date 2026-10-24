-- 0013: 数据源分类(data_source_category) + 数据源归属(系统/分类)
-- 需求: 数据源分类管理(分类增删改查, 分类必须归属业务系统);
--       每个数据源必须选择对应系统及该系统下的分类。

CREATE TABLE IF NOT EXISTS data_source_category (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  system_code  varchar(64)  NOT NULL REFERENCES business_system(system_code) ON DELETE RESTRICT,
  name         varchar(128) NOT NULL,
  remark       varchar(512),
  sort         int          NOT NULL DEFAULT 0,
  created_by   varchar(64),
  created_at   timestamptz  NOT NULL DEFAULT now(),
  updated_at   timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uk_data_source_category UNIQUE (system_code, name)
);
CREATE INDEX IF NOT EXISTS idx_data_source_category_system ON data_source_category (system_code);
COMMENT ON TABLE data_source_category IS '数据源分类(归属业务系统, 系统内名称唯一)';

ALTER TABLE data_source ADD COLUMN IF NOT EXISTS system_code varchar(64);
ALTER TABLE data_source ADD COLUMN IF NOT EXISTS category_id bigint;
ALTER TABLE data_source DROP CONSTRAINT IF EXISTS fk_data_source_system;
ALTER TABLE data_source DROP CONSTRAINT IF EXISTS fk_data_source_category;
ALTER TABLE data_source ADD CONSTRAINT fk_data_source_system FOREIGN KEY (system_code) REFERENCES business_system(system_code) ON DELETE RESTRICT;
ALTER TABLE data_source ADD CONSTRAINT fk_data_source_category FOREIGN KEY (category_id) REFERENCES data_source_category(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS idx_data_source_system ON data_source (system_code);
CREATE INDEX IF NOT EXISTS idx_data_source_category ON data_source (category_id);
COMMENT ON COLUMN data_source.system_code IS '归属业务系统(必填)';
COMMENT ON COLUMN data_source.category_id IS '归属分类(须为该系统下的分类, 必填)';
