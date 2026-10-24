SELECT (SELECT count(*) FROM capacity_metric) AS metrics,
       (SELECT count(*) FROM capacity_metric WHERE metric_code LIKE 'PERF_%') AS perf_metrics,
       (SELECT count(*) FROM business_system) AS systems,
       (SELECT count(*) FROM business_system WHERE system_code LIKE 'PERF_%') AS perf_systems,
       (SELECT count(*) FROM collect_flow) AS flows,
       (SELECT count(*) FROM metric_value_day) AS day_rows,
       (SELECT count(*) FROM metric_value_rt) AS rt_rows,
       (SELECT count(*) FROM alert_record WHERE metric_code LIKE 'PERF_%') AS perf_alerts,
       (SELECT count(*) FROM report_result) AS reports;
SELECT metric_code, name, system_code FROM capacity_metric ORDER BY metric_code;
