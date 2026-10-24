#!/bin/sh
echo vacuum2-start >> /tmp/clean_progress.log
psql -U capacity -d capacity -v ON_ERROR_STOP=1 -f /tmp/vacuum_serial.sql >> /tmp/clean_progress.log 2>&1
echo vacuum2-end >> /tmp/clean_progress.log
