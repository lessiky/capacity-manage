#!/usr/bin/env python3
"""示例采集脚本 (collect): 读取 CAP_PARAMS, 输出 JSON 到 stdout 最后一行

平台约定:
- 入参: 环境变量 CAP_PARAMS = {"biz_date":..., "prev_trading_day":..., "system_code":..., ...}
- 出参: stdout 最后一行 JSON 数组 [{"metric":"指标编码","value":数值,"unit":"单位"}]
白名单模块: json/math/re/datetime/decimal/collections 等
"""
import json
import os


def main():
    params = json.loads(os.environ.get("CAP_PARAMS", "{}"))
    prev_day = params.get("prev_trading_day", "")
    system_code = params.get("system_code", "")
    # ... 此处为示例: 实际场景可解析本地工作目录内的临时文件 / 多源聚合
    result = [
        {"metric": "demo_metric", "value": 12.5, "unit": "GB"},
    ]
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()