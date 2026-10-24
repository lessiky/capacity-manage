#!/bin/sh
echo '--- shm contents ---'
ls -la /dev/shm/
rm -f /dev/shm/PostgreSQL.* 2>/dev/null
echo '--- after cleanup ---'
ls -la /dev/shm/
