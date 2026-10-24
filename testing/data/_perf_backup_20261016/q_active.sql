SELECT now() AS now_ts;
SELECT count(*) AS active_writers FROM pg_stat_activity WHERE datname='capacity' AND state='active' AND query ILIKE '%metric_value%' AND pid<>pg_backend_pid();
SELECT id, biz_date, started_at, finished_at, status FROM daily_summary_log ORDER BY created_at DESC LIMIT 2;
