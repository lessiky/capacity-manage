#!/usr/bin/env python3
"""示例: 远程脚本采集(collectType=script) — 目标机磁盘利用率

平台约定(远程执行, 见 docs/10-design/05-接口契约.md §1.4):
- 脚本与 params.json 由平台经 SFTP 下发到目标机临时目录, 二者同目录;
- 入参: 读取同目录 params.json(含平台内置变量 + 流程配置的自定义参数);
- 出参: stdout 最后一行 JSON 数组 [{metric, value, unit}];
- 执行结束后平台删除该临时目录, 脚本无需自行清理。

入参(params.json):
  mount   挂载点或设备(默认 "/")
  metric  上送的指标名(可选)。留空则取平台内置的 metric_code(当前流程绑定的指标),
          再兜底为 DISK_USAGE_PCT。优先级 metric > metric_code > 默认值,
          因此同一脚本可复用到不同设备/不同指标。

注意:
- 不要读 stdin(在本地沙箱模式下 stdin 是脚本源);
- 依赖 __file__ 定位同目录 params.json 时, 远程执行模式下 __file__ 是脚本真实路径, 可用。
"""
import json
import os
import sys


def load_params():
    here = os.path.dirname(os.path.abspath(__file__))
    path = os.path.join(here, "params.json")
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except OSError:
        # 本地沙箱模式回退到环境变量
        return json.loads(os.environ.get("CAP_PARAMS", "{}"))


def main():
    params = load_params()
    mount = params.get("mount") or "/"
    # 指标名: 显式入参 > 流程内置变量 metric_code > 默认值
    metric = params.get("metric") or params.get("metric_code") or "DISK_USAGE_PCT"
    st = os.statvfs(mount)
    block = st.f_frsize or st.f_bsize
    total = st.f_blocks * block
    avail = st.f_bavail * block          # 非 root 可用块(排除预留)
    used = total - avail
    pct = round(used * 100.0 / total, 2) if total else 0.0
    print(json.dumps(
        [{"metric": metric, "value": pct, "unit": "%"}],
        ensure_ascii=False,
    ))
    return 0


if __name__ == "__main__":
    sys.exit(main())
