SELECT metric_code, biz_date, level, point_count, updated_at, now()-updated_at AS age FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%' ORDER BY updated_at DESC LIMIT 15;
