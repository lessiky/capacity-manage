# -*- coding: utf-8 -*-
"""A5 后台请求数容量指标配置脚本（幂等, 通过 REST API 落地）。

在容量管理平台配置（指标树"后台请求数" A5_HOUTAI_QINGQIU 口径）:
  1) 10 台 A5 服务器 run 日志数据源(SSH/SFTP aboss, GBK 编码):
     /data/aboss/log/${biz_date:YYYYMMDD}/*run_*${biz_date:YYYYMMDD}.txt
     → 归入业务系统 TEST_SYS01(A5) 的数据源分类"后台"(A5-后台)
  2) 6 条 logparse 采集流程(解析模式 section 段落聚合, 分类采集: 分类下全部服务器
     使用同一处理逻辑, 跨服务器合并后写一个指标):
       后台交易请求   A5_JIAOYIQINGQIU_BS/_AVG/_MAX     (功能号前缀 DT01/DT02/GT01/GT02/JT01/JT02)
       后台非交易请求 A5_FEIJIAOYIQINGQIU_BS/_AVG/_MAX  (排除以上前缀的其余功能号)
     平均耗时按调用次数加权 = Σ总用时 / Σ次数; 耗时日志单位毫秒, 指标单位秒 → 线性换算 0.001。

用法:
  python setup_a5_stats.py --base http://127.0.0.1:8080 --user admin --password Admin@123
  python setup_a5_stats.py --dry-run    # 只打印将创建/更新的对象, 不写库
  python setup_a5_stats.py --preview 12 # 全部创建后, 试跑 flow id=12 验证(真实 SFTP)
"""
import argparse
import json
import sys
import urllib.error
import urllib.request

SERVERS = [
    "128.6.120.32", "128.6.120.33", "128.6.120.34", "128.6.120.38", "128.6.120.39",
    "128.6.120.40", "128.6.120.41", "128.6.120.42", "128.6.120.43", "128.6.120.44",
]
SSH_USER = "aboss"
SSH_PASS = "aboss123"
SSH_PORT = 22
LOG_ENCODING = "gbk"
PATH_TEMPLATE = "/data/aboss/log/${biz_date:YYYYMMDD}/"
GLOB_TEMPLATE = "/data/aboss/log/${biz_date:YYYYMMDD}/*run_*${biz_date:YYYYMMDD}.txt"

SYSTEM_CODE = "TEST_SYS01"   # A5 业务系统(平台既有)
CATEGORY_NAME = "后台"        # A5-后台 数据源分类(平台既有)

START_MARKER = "功能号,次数,总用时,最快,最慢,平均,名称"
END_MARKER = "个服务被调用"
LINE_PATTERN = (
    # 功能号形态不固定: S01703 / 310005 / DT01SHZ1001 / DT06****001 → func 取第 3 个非空字段
    r"^\S+ \S+ (?P<func>\S+)\s+(?P<count>\d+),\s*"
    r"(?P<total>\d+(?:\.\d+)?),\s*(?P<fastest>\d+(?:\.\d+)?),\s*"
    r"(?P<slowest>\d+(?:\.\d+)?),\s*(?P<avg>\d+(?:\.\d+)?)"
)
WINDOW_TIME = "22:30"  # 交易日 22:30 触发(业务统计约 22:01 落盘)

TRADE_PREFIXES = ["DT01", "DT02", "GT01", "GT02", "JT01", "JT02"]

# (流程编码, 流程名, 指标编码, 输出值, 前缀包含, 前缀排除, 清洗配置)
FLOWS = [
    ("A5_HOUTAI_JYQ_BS", "后台交易请求笔数", "A5_JIAOYIQINGQIU_BS", "count",
     TRADE_PREFIXES, [], {}),
    ("A5_HOUTAI_JYQ_AVG", "后台交易请求平均耗时", "A5_JIAOYIQINGQIU_AVG", "avg",
     TRADE_PREFIXES, [], {"transforms": [{"type": "linear", "a": 0.001, "b": 0}]}),
    ("A5_HOUTAI_JYQ_MAX", "后台交易请求最大耗时", "A5_JIAOYIQINGQIU_MAX", "slowest",
     TRADE_PREFIXES, [], {"transforms": [{"type": "linear", "a": 0.001, "b": 0}]}),
    ("A5_HOUTAI_FJYQ_BS", "后台非交易请求笔数", "A5_FEIJIAOYIQINGQIU_BS", "count",
     [], TRADE_PREFIXES, {}),
    ("A5_HOUTAI_FJYQ_AVG", "后台非交易请求平均耗时", "A5_FEIJIAOYIQINGQIU_AVG", "avg",
     [], TRADE_PREFIXES, {"transforms": [{"type": "linear", "a": 0.001, "b": 0}]}),
    ("A5_HOUTAI_FJYQ_MAX", "后台非交易请求最大耗时", "A5_FEIJIAOYIQINGQIU_MAX", "slowest",
     [], TRADE_PREFIXES, {"transforms": [{"type": "linear", "a": 0.001, "b": 0}]}),
]


class Api:
    def __init__(self, base, token=None):
        self.base = base.rstrip("/")
        self.token = token

    def call(self, method, path, body=None, expect_ok=True):
        url = self.base + path
        data = json.dumps(body).encode("utf-8") if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.token:
            req.add_header("Authorization", "Bearer " + self.token)
        try:
            with urllib.request.urlopen(req) as r:
                payload = json.loads(r.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            try:
                payload = json.loads(e.read().decode("utf-8"))
            except Exception:
                payload = {"code": e.code, "message": str(e)}
            if expect_ok:
                raise RuntimeError("%s %s 失败: %s" % (method, path, payload))
            return payload
        if expect_ok and payload.get("code") != 0:
            raise RuntimeError("%s %s 失败: %s" % (method, path, payload))
        return payload

    def get404(self, path):
        p = self.call("GET", path, expect_ok=False)
        if p.get("code") == 40400:
            return None
        if p.get("code") != 0:
            raise RuntimeError("GET %s 失败: %s" % (path, p))
        return p.get("data")

    def get_page(self, path):
        # 兼容两种列表返回: 分页对象 {items,total} 与纯数组
        p = self.call("GET", path + ("&" if "?" in path else "?") + "page=1&pageSize=200")
        d = p["data"]
        if isinstance(d, list):
            return d
        items = []
        page, sep = 1, "&" if "?" in path else "?"
        while True:
            p = self.call("GET", "%s%spage=%d&pageSize=200" % (path, sep, page))
            d = p["data"] or {}
            items.extend(d.get("items") or [])
            if len(items) >= (d.get("total") or 0) or not d.get("items"):
                return items
            page += 1


def login(base, user, password):
    p = Api(base).call("POST", "/api/v1/auth/login", {"username": user, "password": password})
    return Api(base, token=p["data"]["accessToken"])


def server_name(ip):
    return "A5后台run日志-%s" % ip


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8080")
    ap.add_argument("--user", default="admin")
    ap.add_argument("--password", default="Admin@123")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--preview", type=int, default=0, help="创建完成后试跑指定 flow id")
    args = ap.parse_args()

    api = login(args.base, args.user, args.password)
    dry = args.dry_run
    made = {"datasource_created": 0, "datasource_updated": 0, "flow_created": 0, "flow_updated": 0}

    # 0) 业务系统 + 数据源分类(须已存在: 分类 id 由系统+名称解析, 不新建)
    if api.get404("/api/v1/systems/%s" % SYSTEM_CODE) is None:
        raise RuntimeError("业务系统不存在: %s" % SYSTEM_CODE)
    cats = api.get_page("/api/v1/data-source-categories")
    cat = next((c for c in cats if c.get("name") == CATEGORY_NAME and c.get("systemCode") == SYSTEM_CODE), None)
    if cat is None:
        raise RuntimeError("数据源分类不存在: %s/%s (请先在数据源分类页创建)" % (SYSTEM_CODE, CATEGORY_NAME))
    cat_id = cat["id"]
    print("[category] %s-%s id=%d" % (SYSTEM_CODE, CATEGORY_NAME, cat_id))

    # 1) 数据源: 按 host 复用已有 log 数据源(如已存在的 A5_run日志), 否则新建; 统一归入分类
    log_ds = api.get_page("/api/v1/data-sources?type=log")
    by_host = {}
    for d in log_ds:
        cfg = d.get("config") or {}
        if isinstance(cfg, str):
            try:
                cfg = json.loads(cfg)
            except Exception:
                cfg = {}
        host = cfg.get("host")
        if host and host not in by_host:
            by_host[host] = d
    ds_id = {}
    for ip in SERVERS:
        want = {
            "type": "log", "scheme": "sftp", "host": ip, "port": SSH_PORT,
            "username": SSH_USER, "password": SSH_PASS, "encoding": LOG_ENCODING,
            "pathTemplate": PATH_TEMPLATE, "globTemplate": GLOB_TEMPLATE,
        }
        exist = by_host.get(ip)
        if exist:
            need = (exist.get("name") != server_name(ip) or exist.get("systemCode") != SYSTEM_CODE
                    or exist.get("categoryId") != cat_id)
            print("[datasource] 复用 #%d %s (%s)%s" % (exist["id"], exist.get("name"), ip,
                                                       " → 更新名称/归属分类" if need else ""))
            if not dry:
                api.call("PUT", "/api/v1/data-sources/%d" % exist["id"], {
                    "name": server_name(ip), "type": "log",
                    "systemCode": SYSTEM_CODE, "categoryId": cat_id,
                    "config": {**want, "password": ""},  # 空密码=沿用已存密文
                })
            ds_id[ip] = exist["id"]
            if need:
                made["datasource_updated"] += 1
            continue
        print("[datasource] 创建 %s (%s)" % (server_name(ip), ip))
        if not dry:
            p = api.call("POST", "/api/v1/data-sources", {
                "name": server_name(ip), "type": "log",
                "systemCode": SYSTEM_CODE, "categoryId": cat_id, "config": want,
            })
            ds_id[ip] = p["data"]["id"]
        made["datasource_created"] += 1

    # 2) 指标校验: 指标树"后台请求数"须已由用户设计好(缺失则报错, 不擅自新建)
    for _, _, metric_code, _, _, _, _ in FLOWS:
        d = api.get404("/api/v1/metrics/%s" % metric_code)
        m = (d or {}).get("metric") if isinstance(d, dict) else None
        if m is None:
            raise RuntimeError("指标不存在: %s (请先在指标管理创建/启用)" % metric_code)
        if m.get("status") != "active":
            raise RuntimeError("指标未启用: %s" % metric_code)
        print("[metric] %s %s (%s, %s)" % (metric_code, m.get("name"), m.get("statMode"), m.get("unit")))

    # 3) 采集流程(一指标一流程; 按 flowCode 幂等, 已存在则更新配置)
    flows = {f["flowCode"]: f for f in api.get_page("/api/v1/flows")}
    for flow_code, name, metric_code, output, prefixes, excludes, clean in FLOWS:
        collect_config = {
            "mode": "section",
            "startMarker": START_MARKER,
            "endMarker": END_MARKER,
            "linePattern": LINE_PATTERN,
            "prefixes": prefixes,
            "excludePrefixes": excludes,
            "output": output,
        }
        body = {
            "flowCode": flow_code, "name": name, "metricCode": metric_code,
            "dataSourceCategoryId": cat_id,
            "collectType": "logparse", "collectConfig": collect_config,
            "cleanConfig": clean,
            "scheduleMode": "trading_day", "tradingMarket": "SH",
            "windowTime": WINDOW_TIME, "bizDateMode": "natural",
            "remark": "A5 后台请求数: %s(分类 %s 下全部服务器 section 段落聚合)" % (name, CATEGORY_NAME),
        }
        exist = flows.get(flow_code)
        if exist:
            print("[flow] 更新 #%d %s -> %s" % (exist["id"], flow_code, metric_code))
            if not dry:
                upd = {k: v for k, v in body.items()
                       if k not in ("flowCode", "metricCode", "collectType", "scheduleMode")}
                api.call("PUT", "/api/v1/flows/%d" % exist["id"], upd)
                # 编辑后流程回到 created(需重新生效), 此处直接激活恢复调度
                api.call("POST", "/api/v1/flows/%d/activate" % exist["id"], expect_ok=False)
            made["flow_updated"] += 1
            continue
        print("[flow] 创建 %s -> %s (输出 %s)" % (flow_code, metric_code, output))
        if not dry:
            p = api.call("POST", "/api/v1/flows", body)
            api.call("POST", "/api/v1/flows/%d/activate" % p["data"]["id"])
        made["flow_created"] += 1

    # 4) 可选试跑
    if args.preview and not dry:
        print("[preview] 试跑 flow id=%d ..." % args.preview)
        try:
            p = api.call("POST", "/api/v1/flows/%d/preview" % args.preview)
            print("[preview] 结果: %s" % json.dumps(p.get("data"), ensure_ascii=False)[:2000])
        except Exception as e:
            print("[preview] 失败(不影响已创建配置): %s" % e)

    print("完成: %s" % json.dumps(made, ensure_ascii=False))
    if dry:
        print("(dry-run 模式, 未写库)")


if __name__ == "__main__":
    sys.exit(main())
