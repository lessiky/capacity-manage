#!/bin/sh
echo '--- df ---'
df -h / /dev/shm /var/lib/postgresql 2>/dev/null
echo '--- big du ---'
du -sh /var/lib/postgresql/* 2>/dev/null
