-- Cleanup for data created by persistence.seedDemoAggregate.
--
-- Scope:
--   * SRV_REQ and its descendants
--   * metric_value_rt / metric_value_day
--   * alert, quality, backfill, forecast and collection records linked to those metrics
--   * DEMO_CORE only when it has no remaining data sources, resources or other business data
--
-- Run with psql and ON_ERROR_STOP=1. The whole cleanup is one transaction.

\set ON_ERROR_STOP on

BEGIN;
SET LOCAL lock_timeout = '15s';

CREATE TEMP TABLE demo_metric_codes (
  code varchar(64) PRIMARY KEY
) ON COMMIT DROP;

-- Include descendants of a partially removed demo metric tree, not only the six seed leaves.
WITH RECURSIVE metric_tree AS (
  SELECT metric_code
  FROM capacity_metric
  WHERE metric_code IN (
    'SRV_REQ',
    'SRV_REQ.ORD', 'SRV_REQ.PAY',
    'SRV_REQ.ORD.COUNT', 'SRV_REQ.ORD.MAX', 'SRV_REQ.ORD.AVG',
    'SRV_REQ.PAY.COUNT', 'SRV_REQ.PAY.MAX', 'SRV_REQ.PAY.AVG'
  )
  UNION
  SELECT child.metric_code
  FROM capacity_metric child
  JOIN metric_tree parent ON child.parent_code = parent.metric_code
)
INSERT INTO demo_metric_codes (code)
SELECT metric_code FROM metric_tree
ON CONFLICT DO NOTHING;

CREATE TEMP TABLE demo_alerts (
  id       bigint PRIMARY KEY,
  alert_no varchar(32) NOT NULL
) ON COMMIT DROP;

INSERT INTO demo_alerts (id, alert_no)
SELECT id, alert_no
FROM alert_record
WHERE metric_code IN (SELECT code FROM demo_metric_codes)
ON CONFLICT DO NOTHING;

-- Alert-related rows first. alert_notification_log has no FK to alert_record,
-- so it must be removed explicitly before alert_record.
DELETE FROM alert_notification_log
WHERE alert_id IN (SELECT id FROM demo_alerts)
   OR alert_no IN (SELECT alert_no FROM demo_alerts);

DELETE FROM alert_level_history
WHERE alert_id IN (SELECT id FROM demo_alerts);

DELETE FROM alert_record
WHERE id IN (SELECT id FROM demo_alerts);

DELETE FROM alert_daily_hit
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM metric_quality_day
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM data_backfill_item
WHERE run_id IN (
  SELECT id
  FROM data_backfill_run
  WHERE metric_code IN (SELECT code FROM demo_metric_codes)
);

DELETE FROM data_backfill_run
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

CREATE TEMP TABLE demo_forecast_results (
  id bigint PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO demo_forecast_results (id)
SELECT id
FROM forecast_result
WHERE metric_code IN (SELECT code FROM demo_metric_codes)
ON CONFLICT DO NOTHING;

DELETE FROM forecast_ai_adjustment
WHERE result_id IN (SELECT id FROM demo_forecast_results)
   OR metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM forecast_result
WHERE id IN (SELECT id FROM demo_forecast_results);

DELETE FROM metric_archive_index
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM data_correction
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM metric_peak_stat
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM metric_value_rt
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM metric_value_day
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

-- collect_task_log cascades to cleaning_trace.
DELETE FROM collect_task_log
WHERE flow_id IN (
  SELECT id
  FROM collect_flow
  WHERE metric_code IN (SELECT code FROM demo_metric_codes)
);

DELETE FROM collect_flow
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DELETE FROM threshold_history
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

-- capacity_metric.parent_code uses ON DELETE RESTRICT, so remove leaves first.
DO $$
DECLARE
  affected int;
BEGIN
  LOOP
    DELETE FROM capacity_metric metric
    WHERE metric.metric_code IN (SELECT code FROM demo_metric_codes)
      AND NOT EXISTS (
        SELECT 1
        FROM capacity_metric child
        WHERE child.parent_code = metric.metric_code
      );

    GET DIAGNOSTICS affected = ROW_COUNT;
    EXIT WHEN affected = 0;
  END LOOP;

  IF EXISTS (
    SELECT 1
    FROM capacity_metric metric
    JOIN demo_metric_codes demo ON demo.code = metric.metric_code
  ) THEN
    RAISE EXCEPTION
      'some demo metrics are still referenced by child metrics; resolve those references and rerun';
  END IF;
END
$$;

-- Remove DEMO_CORE only when after metric cleanup no business-owned records remain.
CREATE TEMP TABLE demo_system_for_delete (
  system_code varchar(64) PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO demo_system_for_delete (system_code)
SELECT system.system_code
FROM business_system system
WHERE system.system_code = 'DEMO_CORE'
  AND NOT EXISTS (
    SELECT 1 FROM business_system_resource resource
    WHERE resource.system_code = system.system_code
  )
  AND NOT EXISTS (
    SELECT 1 FROM data_source_category category
    WHERE category.system_code = system.system_code
  )
  AND NOT EXISTS (
    SELECT 1 FROM data_source source
    WHERE source.system_code = system.system_code
  )
  AND NOT EXISTS (
    SELECT 1 FROM derived_metric metric
    WHERE metric.system_code = system.system_code
  )
ON CONFLICT DO NOTHING;

DELETE FROM sys_data_scope
WHERE system_code IN (SELECT system_code FROM demo_system_for_delete);

DELETE FROM business_system
WHERE system_code IN (SELECT system_code FROM demo_system_for_delete);

-- These checks must all return zero before COMMIT.
SELECT 'remaining demo metrics' AS check_name, count(*) AS remaining
FROM capacity_metric
WHERE metric_code IN (SELECT code FROM demo_metric_codes)
UNION ALL
SELECT 'remaining demo rt values', count(*)
FROM metric_value_rt
WHERE metric_code IN (SELECT code FROM demo_metric_codes)
UNION ALL
SELECT 'remaining demo day values', count(*)
FROM metric_value_day
WHERE metric_code IN (SELECT code FROM demo_metric_codes)
UNION ALL
SELECT 'remaining demo alerts', count(*)
FROM alert_record
WHERE metric_code IN (SELECT code FROM demo_metric_codes);

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM capacity_metric
    WHERE metric_code IN (SELECT code FROM demo_metric_codes)
  ) OR EXISTS (
    SELECT 1
    FROM metric_value_rt
    WHERE metric_code IN (SELECT code FROM demo_metric_codes)
  ) OR EXISTS (
    SELECT 1
    FROM metric_value_day
    WHERE metric_code IN (SELECT code FROM demo_metric_codes)
  ) OR EXISTS (
    SELECT 1
    FROM alert_record
    WHERE metric_code IN (SELECT code FROM demo_metric_codes)
  ) THEN
    RAISE EXCEPTION 'demo cleanup is incomplete; transaction rolled back';
  END IF;
END
$$;

COMMIT;
