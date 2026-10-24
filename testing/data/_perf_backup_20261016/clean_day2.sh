#!/bin/sh
echo "start $(date) remaining_before=$(psql -U capacity -d capacity -tA -c "SELECT count(*) FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%'")" >> /tmp/clean_progress.log
for i in $(seq 1 30); do
  rem=$(psql -U capacity -d capacity -tA -c "SELECT count(*) FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%'" 2>/dev/null)
  if [ -z "$rem" ] || [ "$rem" = "0" ]; then echo "done: no PERF day rows left" >> /tmp/clean_progress.log; break; fi
  out=$(psql -U capacity -d capacity -tA -q -c "SET synchronous_commit=off; DELETE FROM metric_value_day WHERE ctid IN (SELECT ctid FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%' LIMIT 500000)" 2>&1 | head -1)
  echo "batch $i -> $out (rem=$rem)" >> /tmp/clean_progress.log
done
psql -U capacity -d capacity -tA -c "SELECT count(*) FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%'" >> /tmp/clean_progress.log 2>&1
echo "end $(date)" >> /tmp/clean_progress.log
