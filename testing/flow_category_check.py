# -*- coding: utf-8 -*-
"""采集流程数据源分类 API 验证

覆盖:
- 绑定互斥: 流程的数据源与数据源分类二选一(两者/均未选均拒绝)
- 分类仅 logparse 可用; 空分类拒绝; section 段落聚合可与分类组合(跨多数据源合并)
- 分类采集: 分类下全部日志数据源共用同一采集/清洗配置, 原始行合并后按 statMode 聚合
- section + 分类: 各数据源按同一段落逻辑聚合, 再跨数据源合并(次数求和/最快取小/最慢取大/平均加权)
- 采集预览返回数据日期 bizDate
- 编辑重绑定: 分类 <-> 单数据源互转(三态: 缺省沿用 / null 清空 / 数字设置)
- 容错: 单个数据源读取失败不中断整体; 实际执行后失败数据源健康标记 error
运行: python testing/flow_category_check.py   (需后端 :8080 已启动 + docker 可写容器日志)
"""
import json, time, subprocess, urllib.request, urllib.error

BASE = "http://127.0.0.1:8080/api/v1"
TOKEN = None
BACKEND_CTR = "capacity-manage-backend-1"
SYS = "FLC_%04d" % (int(time.time()) % 10000)
NEVER_CRON = "0 3 30 2 *"  # 2月30日 永不触发

def sh(cmd):
    subprocess.run(cmd, shell=True, check=True, capture_output=True)

def api(method, path, body=None, expect=None):
    global TOKEN
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", "Bearer " + TOKEN)
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            out = json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        out = json.loads(e.read().decode())
        if expect and expect in str(out.get("message", "")):
            return out
        raise
    if expect and out.get("code") != 0:
        assert expect in str(out.get("message", "")), "expect %r in %s" % (expect, out)
    if out.get("code") != 0:
        raise AssertionError("api error %s" % out)
    return out.get("data")

# 1) 登录
d = api("POST", "/auth/login", {"username": "admin", "password": "Admin@123"})
TOKEN = d["accessToken"] if "accessToken" in d else d.get("token")
print("1) login ok")

# 容器时区为 UTC, biz_date 与文件名以容器当天为准
TODAY = subprocess.run(["docker", "exec", BACKEND_CTR, "date", "+%F"],
                       capture_output=True, text=True, check=True).stdout.strip()
print("1) 容器数据日期(UTC):", TODAY)

# 2) 系统 + 分类
try:
    api("POST", "/systems", {"systemCode": SYS, "name": SYS + "系统", "owner": "e2e", "level": "general"})
    print("2) system created:", SYS)
except AssertionError:
    print("2) system exists")
CAT1 = api("POST", "/data-source-categories", {"systemCode": SYS, "name": "分类采集", "sort": 1})
CAT_FT = api("POST", "/data-source-categories", {"systemCode": SYS, "name": "容错分类", "sort": 2})
CAT_EMPTY = api("POST", "/data-source-categories", {"systemCode": SYS, "name": "空分类", "sort": 3})
print("2) categories:", CAT1["id"], CAT_FT["id"], CAT_EMPTY["id"])

# 3) 容器日志文件 (每个数据源一个文件, 值不同用于验证合并聚合)
files = {
    "flc_a": "size=100.0 unit=GB",
    "flc_b": "size=200.0 unit=GB",
    "flc_ft": "size=200.0 unit=GB",
}
for tag, content in files.items():
    sh("docker exec %s sh -c \"printf '%s' > /app/data/files/%s_%s.log\"" % (BACKEND_CTR, content, tag, TODAY))
print("3) 容器日志已写: flc_a/flc_b/flc_ft (%s)" % TODAY)

# 4) 数据源: 分类 CAT1 下两个日志数据源, 容错分类下一个正常+一个坏路径
def mk_ds(name, cat_id, path_tpl):
    d = api("POST", "/data-sources", {
        "name": name, "type": "log", "systemCode": SYS, "categoryId": cat_id,
        "config": {"scheme": "local", "pathTemplate": path_tpl, "encoding": "utf-8"},
    })
    return d["id"]

BD = "${biz_date}"
DS_A = mk_ds(SYS + "_A", CAT1["id"], "/app/data/files/flc_a_" + BD + ".log")
DS_B = mk_ds(SYS + "_B", CAT1["id"], "/app/data/files/flc_b_" + BD + ".log")
DS_FT_OK = mk_ds(SYS + "_FTOK", CAT_FT["id"], "/app/data/files/flc_ft_" + BD + ".log")
DS_FT_BAD = mk_ds(SYS + "_FTBAD", CAT_FT["id"], "/app/data/files/flc_missing_" + BD + ".log")
print("4) datasources:", DS_A, DS_B, DS_FT_OK, DS_FT_BAD)

LOGCFG = {"mode": "regex",
          "pattern": r"size=(?P<value>\d+(?:\.\d+)?)\s*unit=(?P<unit>\w+)",
          "valueField": "value", "unitField": "unit"}

def mk_metric(code, stat_mode="avg"):
    return api("POST", "/metrics", {
        "metricCode": code, "name": code, "systemCode": SYS,
        "category": "storage", "metricType": "absolute", "unit": "GB",
        "statMode": stat_mode, "judgeDirection": "higher_worse",
        "thresholdMode": "absolute", "baseline": 100000.0, "warnLine": 200000.0, "dangerLine": 300000.0,
    })

# 5) 负向: 绑定互斥与分类约束
m1 = mk_metric(SYS + "_NEGSQL"); m2 = mk_metric(SYS + "_NEGBoth"); m3 = mk_metric(SYS + "_NEGNone")
m4 = mk_metric(SYS + "_NEGSECT"); m5 = mk_metric(SYS + "_NEGEMPTY")
api("POST", "/flows", {"flowCode": SYS + "_F_SQLCAT", "name": "n", "metricCode": m1["metricCode"],
    "dataSourceCategoryId": CAT1["id"], "collectType": "sql",
    "collectConfig": {"sqlTemplate": "SELECT 1"}, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON}, expect="数据源分类仅日志解析采集可用")
api("POST", "/flows", {"flowCode": SYS + "_F_BOTH", "name": "n", "metricCode": m2["metricCode"],
    "dataSourceId": DS_A, "dataSourceCategoryId": CAT1["id"], "collectType": "logparse",
    "collectConfig": LOGCFG, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON}, expect="须且仅可选择一项")
api("POST", "/flows", {"flowCode": SYS + "_F_NONE", "name": "n", "metricCode": m3["metricCode"],
    "collectType": "logparse", "collectConfig": LOGCFG, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON}, expect="须且仅可选择一项")
# section + 分类: 允许(执行时按分类下全部数据源同一段落逻辑聚合后合并)
sect = dict(LOGCFG); sect.update({"mode": "section", "startMarker": "a", "linePattern": "(?P<func>\\S+)"})
fsect = api("POST", "/flows", {"flowCode": SYS + "_F_SECT", "name": "n", "metricCode": m4["metricCode"],
    "dataSourceCategoryId": CAT1["id"], "collectType": "logparse",
    "collectConfig": sect, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON})
assert fsect["dataSourceCategoryId"] == CAT1["id"], fsect
api("POST", "/flows", {"flowCode": SYS + "_F_EMPTY", "name": "n", "metricCode": m5["metricCode"],
    "dataSourceCategoryId": CAT_EMPTY["id"], "collectType": "logparse",
    "collectConfig": LOGCFG, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON}, expect="暂无可用日志数据源")
print("5) 校验 ok (sql+分类拒绝 / 两者拒绝 / 均未选拒绝 / section+分类允许 / 空分类拒绝)")

# 6) 正向: 分类采集 + 预览显示数据日期
m = mk_metric(SYS + "_AVG")
fl = api("POST", "/flows", {"flowCode": SYS + "_F_CAT", "name": "分类采集验证", "metricCode": m["metricCode"],
    "dataSourceCategoryId": CAT1["id"], "collectType": "logparse",
    "collectConfig": LOGCFG, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON})
FID = fl["id"]
p = api("POST", "/flows/%d/preview" % FID)
assert p["status"] == "success", p
assert p.get("bizDate") == TODAY, "bizDate=%r want %r" % (p.get("bizDate"), TODAY)
assert p["rowsCollected"] == 2, p
assert abs(p["cleanValue"] - 150.0) < 1e-6, p
assert set(p.get("dsOk", [])) == {DS_A, DS_B}, p
assert not p.get("dsFailed"), p
assert len(p.get("rawPreview", [])) == 2, p
print("6) 分类采集预览 ok: bizDate=%s rows=%d cleanValue=%s dsOk=%s" %
      (p["bizDate"], p["rowsCollected"], p["cleanValue"], sorted(p["dsOk"])))

# 7) 编辑重绑定: 分类 -> 单数据源 -> 分类
api("PUT", "/flows/%d" % FID, {"dataSourceId": DS_A, "dataSourceCategoryId": None})
g = api("GET", "/flows/%d" % FID)
assert g["dataSourceId"] == DS_A and g["dataSourceCategoryId"] is None, g
assert g["status"] == "created" and g["version"] == 2, g
api("PUT", "/flows/%d" % FID, {"dataSourceId": None, "dataSourceCategoryId": CAT1["id"]})
g = api("GET", "/flows/%d" % FID)
assert g["dataSourceId"] is None and g["dataSourceCategoryId"] == CAT1["id"], g
p2 = api("POST", "/flows/%d/preview" % FID)
assert p2["status"] == "success" and abs(p2["cleanValue"] - 150.0) < 1e-6, p2
print("7) 重绑定 分类<->单数据源 ok (version=%d)" % g["version"])

# 8) 容错: 坏路径数据源不中断整体
mft = mk_metric(SYS + "_FT")
flft = api("POST", "/flows", {"flowCode": SYS + "_F_FT", "name": "容错验证", "metricCode": mft["metricCode"],
    "dataSourceCategoryId": CAT_FT["id"], "collectType": "logparse",
    "collectConfig": LOGCFG, "cleanConfig": {},
    "scheduleMode": "normal", "cronExpr": NEVER_CRON})
FFID = flft["id"]
pf = api("POST", "/flows/%d/preview" % FFID)
assert pf["status"] == "success", pf
assert pf["rowsCollected"] == 1 and abs(pf["cleanValue"] - 200.0) < 1e-6, pf
assert pf.get("dsOk") == [DS_FT_OK] and pf.get("dsFailed") == [DS_FT_BAD], pf
assert "读取失败" in pf.get("runLog", ""), pf
print("8) 容错预览 ok: 成功源=%s 失败源=%s runLog=%s" % (pf["dsOk"], pf["dsFailed"], pf["runLog"].replace("\n", " | ")))

# 9) 实际执行: 失败数据源健康标记 error, 成功源 ok
api("POST", "/flows/%d/activate" % FFID)
api("POST", "/flows/%d/trigger" % FFID)
task = None
for _ in range(45):
    time.sleep(2)
    items = api("GET", "/tasks?flowId=%d&pageSize=5" % FFID).get("items", [])
    if items and items[0]["status"] in ("success", "failed"):
        task = items[0]
        break
assert task, "任务未产生"
assert task["status"] == "success", task
src = {x["id"]: x for x in api("GET", "/data-sources")}
assert src[DS_FT_BAD]["healthStatus"] == "error", src[DS_FT_BAD]
assert src[DS_FT_OK]["healthStatus"] == "ok", src[DS_FT_OK]
print("9) 执行后健康标记 ok: 坏路径=error 正常=ok")

# 10) section + 分类: 各数据源同一段落逻辑聚合 → 跨数据源合并
CAT_SEC = api("POST", "/data-source-categories", {"systemCode": SYS, "name": "段落聚合分类", "sort": 4})
SEC_FILES = {
    "flc_sec_a": "20261016 22:00:00.020 DT0101 100, 5000.00, 10.00, 90.00, 50.00, x source=[],rule=[]",
    "flc_sec_b": "20261016 22:00:00.020 DT0102 50, 2500.00, 8.00, 60.00, 50.00, y source=[],rule=[]",
}
for tag, body in SEC_FILES.items():
    text = ("20261016 22:00:00.010 SECTSTART\\n" + body +
            "\\n20261016 22:00:00.040 1个服务被调用.\\n")
    sh("docker exec %s sh -c \"printf '%s' >> /app/data/files/%s_%s.log\"" % (BACKEND_CTR, text, tag, TODAY))
DS_SEC_A = mk_ds(SYS + "_SECA", CAT_SEC["id"], "/app/data/files/flc_sec_a_" + BD + ".log")
DS_SEC_B = mk_ds(SYS + "_SECB", CAT_SEC["id"], "/app/data/files/flc_sec_b_" + BD + ".log")
SEC_CFG = {
    "mode": "section", "startMarker": "SECTSTART", "endMarker": "个服务被调用",
    "linePattern": r"^\S+ \S+ (?P<func>\S+)\s+(?P<count>\d+),\s*(?P<total>\d+(?:\.\d+)?),"
                   r"\s*(?P<fastest>\d+(?:\.\d+)?),\s*(?P<slowest>\d+(?:\.\d+)?),\s*(?P<avg>\d+(?:\.\d+)?)",
}

def mk_sect_flow(suffix, metric_code, output):
    return api("POST", "/flows", {"flowCode": SYS + suffix, "name": "段落聚合分类", "metricCode": metric_code,
        "dataSourceCategoryId": CAT_SEC["id"], "collectType": "logparse",
        "collectConfig": {**SEC_CFG, "prefixes": ["DT01"], "output": output}, "cleanConfig": {},
        "scheduleMode": "normal", "cronExpr": NEVER_CRON})

msec = mk_metric(SYS + "_SECAVG")
fsec = mk_sect_flow("_F_SECAVG", msec["metricCode"], "avg")
ps = api("POST", "/flows/%d/preview" % fsec["id"])
assert ps["status"] == "success", ps
# (5000+2500)/(100+50) = 50 → 跨数据源按调用次数加权平均
assert abs(ps["cleanValue"] - 50.0) < 1e-6, ps
assert set(ps.get("dsOk", [])) == {DS_SEC_A, DS_SEC_B}, ps
assert not ps.get("dsFailed"), ps
print("10) section+分类 avg ok: 跨数据源加权平均=%s (2 个源, 次数 100/50)" % ps["cleanValue"])

msec2 = mk_metric(SYS + "_SECMAX", stat_mode="max")
fsec2 = mk_sect_flow("_F_SECMAX", msec2["metricCode"], "slowest")
ps2 = api("POST", "/flows/%d/preview" % fsec2["id"])
assert ps2["status"] == "success" and abs(ps2["cleanValue"] - 90.0) < 1e-6, ps2
print("10) section+分类 slowest ok: 跨数据源最慢取最大=%s" % ps2["cleanValue"])

print()
print("ALL CHECKS PASSED (SYS=%s)" % SYS)
