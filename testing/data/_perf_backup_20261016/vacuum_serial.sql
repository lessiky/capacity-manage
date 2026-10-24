SET max_parallel_maintenance_workers=0;
VACUUM (ANALYZE) metric_value_day;
VACUUM (ANALYZE) metric_value_rt;
VACUUM (ANALYZE) capacity_metric;
