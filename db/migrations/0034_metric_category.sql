-- 0034: 指标分类按业务系统预定义维护。capacity_metric.category 继续保存分类名称,
-- 以兼容报表与历史数据; 新写入由服务端校验必须来自本表。
CREATE TABLE IF NOT EXISTS metric_category (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  system_code varchar(64) NOT NULL REFERENCES business_system(system_code) ON DELETE CASCADE,
  name        varchar(32) NOT NULL,
  remark      varchar(512),
  sort        integer NOT NULL DEFAULT 0,
  created_by  varchar(64),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_metric_category_system_name UNIQUE (system_code, name)
);
CREATE INDEX IF NOT EXISTS idx_metric_category_system ON metric_category (system_code, sort, id);
COMMENT ON TABLE metric_category IS '指标分类(按业务系统维护, 系统内名称唯一)';

-- 保留已有非空分类, 并为全部业务系统补齐默认分类。
INSERT INTO metric_category (system_code, name)
SELECT DISTINCT system_code, btrim(category)
FROM capacity_metric
WHERE category IS NOT NULL AND btrim(category) <> ''
ON CONFLICT (system_code, name) DO NOTHING;

INSERT INTO metric_category (system_code, name)
SELECT system_code, '默认分类'
FROM business_system
ON CONFLICT (system_code, name) DO NOTHING;

-- 历史树节点的分类以顶层汇总/独立指标的归属为准, 避免父子分到不同分类。
WITH RECURSIVE metric_category_tree AS (
  SELECT metric_code, parent_code,
         COALESCE(NULLIF(btrim(category), ''), '默认分类') AS root_category
  FROM capacity_metric
  WHERE parent_code IS NULL
  UNION ALL
  SELECT child.metric_code, child.parent_code, parent.root_category
  FROM capacity_metric child
  JOIN metric_category_tree parent ON child.parent_code = parent.metric_code
)
UPDATE capacity_metric metric
SET category = tree.root_category
FROM metric_category_tree tree
WHERE metric.metric_code = tree.metric_code
  AND metric.category IS DISTINCT FROM tree.root_category;

CREATE INDEX IF NOT EXISTS idx_metric_category_value ON capacity_metric (system_code, category);

DROP TRIGGER IF EXISTS trg_metric_category_updated ON metric_category;
CREATE TRIGGER trg_metric_category_updated
  BEFORE UPDATE ON metric_category
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
