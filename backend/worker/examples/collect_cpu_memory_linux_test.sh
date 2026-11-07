#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${SCRIPT_DIR}/collect_cpu_memory_linux.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

extract_pct() {
  local metric="$1" line="$2"
  printf '%s' "$line" | sed -n \
    "s/.*\"metric\":\"${metric}\",\"value\":\([0-9][0-9]*\.[0-9][0-9]\),\"unit\":\"%\".*/\1/p"
}

assert_pct() {
  local name="$1" value="$2"
  [ -n "$value" ] || fail "${name} 未出现在输出中"
  awk -v v="$value" 'BEGIN { exit !(v >= 0 && v <= 100) }' ||
    fail "${name} 取值不在 0..100: ${value}"
}

assert_output() {
  local line="$1" cpu_metric="$2" mem_metric="$3"
  [[ "$line" == \[*\] ]] || fail "最后一行不是 JSON 数组: ${line}"

  local cpu_value mem_value
  cpu_value="$(extract_pct "$cpu_metric" "$line")"
  mem_value="$(extract_pct "$mem_metric" "$line")"
  assert_pct "$cpu_metric" "$cpu_value"
  assert_pct "$mem_metric" "$mem_value"
}

[ -f "$TARGET" ] || fail "待测脚本不存在: $TARGET"

default_line="$("$TARGET" | tail -n 1)"
assert_output "$default_line" "CPU_UTIL_PCT" "MEM_UTIL_PCT"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
cp "$TARGET" "${tmp_dir}/collect.sh"
printf '%s\n' '{"cpuMetric":"CPU_CUSTOM","memoryMetric":"MEM_CUSTOM"}' > "${tmp_dir}/params.json"

custom_line="$("${tmp_dir}/collect.sh" | tail -n 1)"
assert_output "$custom_line" "CPU_CUSTOM" "MEM_CUSTOM"

echo "PASS: collect_cpu_memory_linux.sh"
