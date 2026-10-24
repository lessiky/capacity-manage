#!/bin/sh
rm -f /tmp/perf_clean_day.sh /tmp/clean_day.sh /tmp/clean_day2.sh /tmp/run_vacuum.sh /tmp/run_vacuum2.sh /tmp/vacuum.sql /tmp/vacuum_serial.sql /tmp/clean_small.sql /tmp/clean_left_day.sql /tmp/q_*.sql /tmp/clean_progress.log /tmp/clean_files.sh
rm -rf /tmp/perfbackup
echo '--- /tmp after ---'
ls /tmp/
