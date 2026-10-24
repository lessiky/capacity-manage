#!/usr/bin/env bash
# 参考脚本: 远程脚本采集(collectType=script) — Linux 目标机指定挂载点/设备的空间利用率
#
# 平台约定(见 docs/10-design/05-接口契约.md §1.4):
# - 脚本与 params.json 由平台经 SFTP 下发到目标机同一临时目录, 执行结束后目录被删除;
# - 入参: 读取与脚本同目录的 params.json(平台内置变量 + 流程配置的自定义参数);
# - 出参: stdout 最后一行输出 JSON **数组** [{"metric","value","unit"}];
# - 不要读 stdin(本地沙箱模式下 stdin 是脚本源)。
#
# 入参(params.json):
#   mount   挂载点或设备, 如 "/"、"/data"、"/dev/sdb1"(默认 "/")
#   metric  上送的指标名(可选)。留空则取平台内置的 metric_code(即当前流程绑定的指标),
#           再兜底为 DISK_USAGE_PCT。优先级: metric > metric_code > 默认值,
#           因此同一脚本可复用到不同设备/不同指标, 无需改脚本。
set -euo pipefail

# 远程执行时工作目录不是临时目录, 必须用脚本自身路径定位同目录 params.json
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PARAMS_JSON="${SCRIPT_DIR}/params.json"

# 读取字符串型参数: 依次尝试 jq → python3 → sed。
# 平台写入的是扁平 string->string JSON; 前两者是真正的 JSON 解析(jq/python3),
# 末位 sed 仅解析简单值(值里含转义引号/反斜杠时建议确保目标机有 jq 或 python3)。
get_param() {
  local key="$1" default="$2"
  [ -f "$PARAMS_JSON" ] || { printf '%s' "$default"; return; }
  if command -v jq >/dev/null 2>&1; then
    jq -r --arg k "$key" '.[$k] // empty' "$PARAMS_JSON" 2>/dev/null || true
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import json,sys
try:
    v = json.load(open(sys.argv[1], encoding="utf-8")).get(sys.argv[2], "")
except Exception:
    v = ""
print(v if v is not None else "")' "$PARAMS_JSON" "$key" 2>/dev/null || true
    return
  fi
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$PARAMS_JSON" | head -n1
}

TARGET="$(get_param mount '/')"
[ -n "$TARGET" ] || TARGET="/"

if [ ! -e "$TARGET" ]; then
  echo "目标路径不存在: $TARGET" >&2
  exit 2
fi

# df -Pk: POSIX 输出、1024 字节块; 取数据行(第 2 行)的第 2/3/4 字段, 避免挂载点含空格被截断
DF_LINE="$(df -Pk "$TARGET" | awk 'NR==2')"
TOTAL="$(printf '%s' "$DF_LINE" | awk '{print $2}')"
USED="$(printf '%s' "$DF_LINE" | awk '{print $3}')"

if [ -z "${TOTAL:-}" ] || [ "${TOTAL:-0}" -le 0 ] 2>/dev/null; then
  echo "无法解析 df 输出: $TARGET" >&2
  exit 3
fi

PCT="$(awk -v u="$USED" -v t="$TOTAL" 'BEGIN{printf "%.2f", (t>0 ? u*100.0/t : 0)}')"

# 指标名: 显式入参 > 流程内置变量 metric_code > 默认值
METRIC="$(get_param metric '')"
[ -n "$METRIC" ] || METRIC="$(get_param metric_code '')"
[ -n "$METRIC" ] || METRIC="DISK_USAGE_PCT"
# 拼 JSON 前转义(指标名含 " 或 \ 时不破坏结构)
METRIC_JSON="$(printf '%s' "$METRIC" | sed 's/\\/\\\\/g; s/"/\\"/g')"

# 只上送利用率; 需要剩余空间时追加一项 {"metric":"DISK_FREE_GB","value":...,"unit":"GB"}
printf '[{"metric":"%s","value":%s,"unit":"%%"}]\n' "$METRIC_JSON" "$PCT"
