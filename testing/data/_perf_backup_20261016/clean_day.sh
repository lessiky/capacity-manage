#!/bin/sh
echo "== batch delete metric_value_day PERF (500k/batch) =="
for i in $(seq 1 25); do
  out=$(psql -U capacity -d capacity -tA -c "DELETE FROM metric_value_day WHERE ctid IN (SELECT ctid FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%' LIMIT 500000)" 2>&1 | head -1)
  echo "batch $i -> $out"
done
echo "== remaining day rows: =="
psql -U capacity -d capacity -tA -c "SELECT count(*) FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%'"
