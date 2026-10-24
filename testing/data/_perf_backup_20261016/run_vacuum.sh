#!/bin/sh
echo vacuum-start >> /tmp/clean_progress.log
psql -U capacity -d capacity -v ON_ERROR_STOP=1 -f /tmp/vacuum.sql >> /tmp/clean_progress.log 2>&1
echo vacuum-end >> /tmp/clean_progress.log
