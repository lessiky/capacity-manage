#!/usr/bin/env bash
# 参考脚本: 远程脚本采集(collectType=script) — Linux 目标机 CPU/内存利用率
#
# 平台约定(见 docs/10-design/05-接口契约.md §1.4):
# - 脚本与 params.json 由平台经 SFTP 下发到目标机同一临时目录, 执行结束后目录被删除;
# - 入参: 读取与脚本同目录的 params.json(平台内置变量 + 流程配置的自定义参数);
# - 出参: stdout 最后一行输出 JSON **数组** [{"metric","value","unit"}];
# - 不要读 stdin(本地沙箱模式下 stdin 是脚本源)。
#
# 入参(params.json):
#   cpuMetric     CPU 指标编码(默认 CPU_UTIL_PCT)
#   memoryMetric  内存指标编码(默认 MEM_UTIL_PCT)
#
# 平台仍是一流程对一指标: 同一个脚本可被两个流程复用, 两个流程的
# metric_code 分别设为 CPU_UTIL_PCT / MEM_UTIL_PCT, 平台会筛选各自数据。
set -euo pipefail

export LC_ALL=C

# 远程执行时工作目录不是临时目录, 必须用脚本自身路径定位同目录 params.json
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PARAMS_JSON="${SCRIPT_DIR}/params.json"

# 读取字符串型参数: 依次尝试 jq → python3 → sed。
get_param() {
  local key="$1" default="$2"
  [ -f "$PARAMS_JSON" ] || { printf '%s' "$default"; return; }
  if command -v jq >/dev/null 2>&1; then
    jq -r --arg k "$key" '.[$k] // empty' "$PARAMS_JSON" 2>/dev/null || true
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    local value
    value="$(python3 -c 'import json,sys
try:
    v = json.load(open(sys.argv[1], encoding="utf-8")).get(sys.argv[2], "")
except Exception:
    v = ""
print(v if v is not None else "")' "$PARAMS_JSON" "$key" 2>/dev/null || true)"
    if [ -n "$value" ]; then
      printf '%s' "$value"
      return
    fi
  fi
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$PARAMS_JSON" | head -n1
}

read_cpu_stat() {
  awk '
    $1 == "cpu" {
      total = $2 + $3 + $4 + $5 + $6 + $7 + $8 + $9
      idle = $5 + $6
      printf "%d %d\n", total, idle
      found = 1
      exit
    }
    END { if (!found) exit 2 }
  ' /proc/stat
}

read_memory_pct() {
  awk '
    /^MemTotal:/       { total = $2 }
    /^MemAvailable:/   { available = $2; has_available = 1 }
    /^MemFree:/        { free = $2 }
    /^Buffers:/        { buffers = $2 }
    /^Cached:/         { cached = $2 }
    /^SReclaimable:/   { reclaimable = $2 }
    /^Shmem:/          { shmem = $2 }
    END {
      if (total <= 0) exit 2
      if (!has_available) available = free + buffers + cached + reclaimable - shmem
      if (available < 0) available = 0
      if (available > total) available = total
      printf "%.2f", (total - available) * 100.0 / total
    }
  ' /proc/meminfo
}

if [ ! -r /proc/stat ] || [ ! -r /proc/meminfo ]; then
  echo "该脚本仅支持可读取 /proc/stat 和 /proc/meminfo 的 Linux 系统" >&2
  exit 2
fi

# CPU: 间隔 1 秒采样两次, 以总时间片和空闲时间片增量计算利用率
CPU_STAT_1="$(read_cpu_stat)" || {
  echo "无法解析 /proc/stat 的 cpu 汇总行" >&2
  exit 3
}
read -r TOTAL_1 IDLE_1 <<< "$CPU_STAT_1"

sleep 1

CPU_STAT_2="$(read_cpu_stat)" || {
  echo "无法解析 /proc/stat 的 cpu 汇总行" >&2
  exit 3
}
read -r TOTAL_2 IDLE_2 <<< "$CPU_STAT_2"

TOTAL_DELTA=$((TOTAL_2 - TOTAL_1))
IDLE_DELTA=$((IDLE_2 - IDLE_1))

if [ "$TOTAL_DELTA" -le 0 ]; then
  echo "CPU 时间片增量无效: ${TOTAL_DELTA}" >&2
  exit 4
fi
if [ "$IDLE_DELTA" -lt 0 ]; then
  IDLE_DELTA=0
fi
if [ "$IDLE_DELTA" -gt "$TOTAL_DELTA" ]; then
  IDLE_DELTA="$TOTAL_DELTA"
fi

CPU_PCT="$(awk -v total="$TOTAL_DELTA" -v idle="$IDLE_DELTA" \
  'BEGIN { printf "%.2f", (total > 0 ? (total - idle) * 100.0 / total : 0) }')"

# 内存: MemAvailable 优先; 老内核回退到 MemFree + Buffers + Cached + SReclaimable - Shmem
MEM_PCT="$(read_memory_pct)" || {
  echo "无法解析 /proc/meminfo" >&2
  exit 5
}

CPU_METRIC="$(get_param cpuMetric 'CPU_UTIL_PCT')"
[ -n "$CPU_METRIC" ] || CPU_METRIC="CPU_UTIL_PCT"
MEMORY_METRIC="$(get_param memoryMetric 'MEM_UTIL_PCT')"
[ -n "$MEMORY_METRIC" ] || MEMORY_METRIC="MEM_UTIL_PCT"

# 拼 JSON 前转义(指标名含 " 或 \ 时不破坏结构)
CPU_METRIC_JSON="$(printf '%s' "$CPU_METRIC" | sed 's/\\/\\\\/g; s/"/\\"/g')"
MEMORY_METRIC_JSON="$(printf '%s' "$MEMORY_METRIC" | sed 's/\\/\\\\/g; s/"/\\"/g')"

printf '[{"metric":"%s","value":%s,"unit":"%%"},{"metric":"%s","value":%s,"unit":"%%"}]\n' \
  "$CPU_METRIC_JSON" "$CPU_PCT" "$MEMORY_METRIC_JSON" "$MEM_PCT"
