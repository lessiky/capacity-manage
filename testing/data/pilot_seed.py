# -*- coding: utf-8 -*-
"""M5 试点 UAT 环境初始化 (WBS 5-4)

按《开发计划》§7 试点要求播种 3 个试点业务系统:
- PILOT_TRADE  核心级(core): DB 数据源(MariaDB pilot_db) + SQL 采集; 百分比阈值 + 绝对值阈值
- PILOT_RISK   一般级: 日志数据源(本地文件) + 日志解析采集; 百分比阈值
- PILOT_REPORT 一般级: 日志 + Python 脚本采集; 绝对值阈值

覆盖: 数据库+日志两类数据源, 百分比+绝对值两种阈值表达, >=1 个核心级系统。
流程采用 trading_day 调度模式(交易日感知), 供 UAT 期间真实调度运行。
幂等可重复执行。运行: python testing/data/pilot_seed.py
"""
import json, subprocess, time, urllib.request, urllib.error

try:
    import sys
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

BASE = "http://127.0.0.1:8080/api/v1"
BACKEND_CTR = "capacity-manage-backend-1"
MYSQL_CTR = "capacity-mysql"
TODAY = time.strftime("%Y-%m-%d", time.gmtime())
TOKEN = None

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
    if out.get("code") != 0:
        raise AssertionError(f"api error {out}")
    return out.get("data")

def sh(cmd, check=True):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"cmd fail: {cmd}\n{r.stderr[:400]}")
    return r

def ensure_mysql():
    # Windows cmd 下 ^ 是转义符, 会破坏 --filter 值, 故不用 ^...$ 锚定
    r = sh(f'docker ps -a --filter name={MYSQL_CTR} --format {{{{.State}}}}', check=False)
    state = r.stdout.strip().splitlines()[0].strip() if r.stdout.strip() else ""
    if state != "running":
        if state:
            sh(f"docker rm -f {MYSQL_CTR}", check=False)
        print("0) 启动 capacity-mysql ...")
        sh(f"docker run -d --name {MYSQL_CTR} -e MYSQL_ROOT_PASSWORD=m5root "
           f"-e MYSQL_DATABASE=m5e2e -e MYSQL_USER=m5user -e MYSQL_PASSWORD=m5pass "
           f"-p 13306:3306 mariadb:11")
        for _ in range(60):
            time.sleep(2)
            r = sh(f"docker exec {MYSQL_CTR} mariadb-admin -um5user -pm5pass ping", check=False)
            if r.returncode == 0:
                break
        else:
            raise RuntimeError("mariadb 未就绪")
        time.sleep(2)
    sh(f"docker network connect capacity-manage_default {MYSQL_CTR}", check=False)
    ddl_ok = False
    for _ in range(45):
        r = sh(f'docker exec {MYSQL_CTR} mariadb -um5user -pm5pass -e "SELECT 1;"', check=False)
        if r.returncode == 0:
            ddl_ok = True
            break
        time.sleep(2)
    assert ddl_ok, "m5user not ready"
    # 建库/授权用 root: 普通用户 m5user 无 CREATE DATABASE 权限 (MariaDB)
    sh(f"docker exec {MYSQL_CTR} mariadb -uroot -pm5root -e \"CREATE DATABASE IF NOT EXISTS pilot_db; GRANT ALL ON pilot_db.* TO 'm5user'@'%'; FLUSH PRIVILEGES;\"")
    sh(f"docker exec {MYSQL_CTR} mariadb -um5user -pm5pass pilot_db -e "
       '"CREATE TABLE IF NOT EXISTS trade_capacity ('
       "  id INT PRIMARY KEY, "
       "  db_total_mb DECIMAL(12,2) NOT NULL, "
       "  db_used_mb  DECIMAL(12,2) NOT NULL, "
       "  msg_queue_depth INT NOT NULL"
       "); "
       "INSERT INTO trade_capacity (id, db_total_mb, db_used_mb, msg_queue_depth) "
       "VALUES (1, 1024000, 745000, 32000) "
       "ON DUPLICATE KEY UPDATE db_total_mb=1024000, db_used_mb=745000, msg_queue_depth=32000;")
    print("0) pilot_db 就绪: trade_capacity(1, 总1024000MB, 已用745000MB, 队列32000)")

ensure_mysql()

d = api("POST", "/auth/login", {"username": "admin", "password": "Admin@123"})
TOKEN = d["accessToken"] if "accessToken" in d else d.get("token")
print("1) admin login ok")

# 2) 业务系统
for code, name, level, owner in [
    ("PILOT_TRADE", "试点-交易系统", "core", "trade-team"),
    ("PILOT_RISK", "试点-风控系统", "general", "risk-team"),
    ("PILOT_REPORT", "试点-报表系统", "general", "report-team"),
]:
    try:
        api("POST", "/systems", {"systemCode": code, "name": name, "owner": owner, "level": level})
        print(f"2) system {code} created ({level})")
    except AssertionError:
        print(f"2) system {code} exists")

# 3) 指标 (百分比 + 绝对值)
METRICS = [
    # PILOT_TRADE: SQL 采集
    ("TRADE_DB_USAGE", "交易库使用率", "PILOT_TRADE", "usage", "%", "latest", "higher_worse", "percent", 100.0, 70.0, 85.0),
    ("TRADE_MSG_QUEUE", "交易消息队列深度", "PILOT_TRADE", "absolute", "条", "latest", "higher_worse", "absolute", 50000.0, 30000.0, 40000.0),
    # PILOT_RISK: 日志采集
    ("RISK_CPU", "风控主机CPU使用率", "PILOT_RISK", "usage", "%", "latest", "higher_worse", "percent", 100.0, 75.0, 90.0),
    ("RISK_MEM_GB", "风控主机内存用量", "PILOT_RISK", "absolute", "GB", "max", "higher_worse", "absolute", 256.0, 190.0, 230.0),
    # PILOT_REPORT: 日志 + Python
    ("RPT_LOG_SIZE", "报表日志体积", "PILOT_REPORT", "absolute", "GB", "latest", "higher_worse", "absolute", 500.0, 350.0, 450.0),
    ("RPT_JOB_DUR", "报表作业耗时", "PILOT_REPORT", "absolute", "s", "latest", "higher_worse", "absolute", 600.0, 400.0, 550.0),
]
for code, name, syscode, mtype, unit, stat, direction, tmode, base, warn, danger in METRICS:
    try:
        api("POST", "/metrics", {
            "metricCode": code, "name": name, "systemCode": syscode,
            "category": "storage", "metricType": mtype, "unit": unit,
            "statMode": stat, "judgeDirection": direction, "thresholdMode": tmode,
            "baseline": base, "warnLine": warn, "dangerLine": danger,
        })
        print(f"3) metric {code} created")
    except AssertionError:
        print(f"3) metric {code} exists")

# 4) 数据源 (归属试点系统及该系统下的分类)
def ensure_cat(sys_code, name):
    try:
        return api("POST", "/data-source-categories", {"systemCode": sys_code, "name": name})["id"]
    except AssertionError:
        lst = api("GET", "/data-source-categories?systemCode=" + sys_code)
        return [x for x in lst if x["name"] == name][0]["id"]

CAT_TRADE = ensure_cat("PILOT_TRADE", "数据库")
CAT_RISK = ensure_cat("PILOT_RISK", "日志")
print("4) categories ready: PILOT_TRADE/数据库, PILOT_RISK/日志")

def ds_get(name):
    lst = api("GET", "/data-sources")
    hit = [x for x in lst if x["name"] == name]
    return hit[0]["id"] if hit else None

ds_sql = ds_get("PILOT_MYSQL")
if ds_sql is None:
    d = api("POST", "/data-sources", {
        "name": "PILOT_MYSQL", "type": "db",
        "systemCode": "PILOT_TRADE", "categoryId": CAT_TRADE,
        "config": {"driver": "mysql", "host": "capacity-mysql", "port": 3306,
                   "username": "m5user", "password": "m5pass", "database": "pilot_db"}})
    ds_sql = d["id"]
    print("4) datasource PILOT_MYSQL created id=", ds_sql)
else:
    api("PUT", f"/data-sources/{ds_sql}", {
        "name": "PILOT_MYSQL", "type": "db",
        "systemCode": "PILOT_TRADE", "categoryId": CAT_TRADE,
        "config": {"driver": "mysql", "host": "capacity-mysql", "port": 3306,
                   "username": "m5user", "password": "m5pass", "database": "pilot_db"}})
    print("4) datasource PILOT_MYSQL updated id=", ds_sql)
t = api("POST", f"/data-sources/{ds_sql}/test")
assert t.get("ok") is True, f"PILOT_MYSQL 连接失败: {t}"
print("4) PILOT_MYSQL 连接测试 ok")

ds_log = ds_get("PILOT_LOG")
if ds_log is None:
    d = api("POST", "/data-sources", {
        "name": "PILOT_LOG", "type": "log",
        "systemCode": "PILOT_RISK", "categoryId": CAT_RISK,
        "config": {"scheme": "local", "pathTemplate": "/app/data/files/pilot_${biz_date}.log", "encoding": "utf-8"}})
    ds_log = d["id"]
    print("4) datasource PILOT_LOG created id=", ds_log)
else:
    print("4) datasource PILOT_LOG exists id=", ds_log)

# 试点日志文件(当日): CPU/MEM(RISK) + 日志体积(REPORT)
subprocess.run(["docker", "exec", BACKEND_CTR, "sh", "-c",
                f"printf 'risk cpu_pct=82.4\\nrisk mem_gb=205.5\\nrpt log_size_gb=362.8\\n' "
                f"> /app/data/files/pilot_{TODAY}.log"], check=True)
print(f"4) 试点日志已写: pilot_{TODAY}.log (cpu=82.4 mem=205.5 log=362.8)")

# 5) Python 采集脚本 (报表作业耗时)
PYSCRIPT = (
    'import json\nimport os\n\n\n'
    'def main():\n'
    '    params = json.loads(os.environ.get("CAP_PARAMS", "{}"))\n'
    '    # 试点模拟: 作业耗时取 428.6s(>400 预警线, 演示 warn 告警)\n'
    '    print(json.dumps([{"metric": "RPT_JOB_DUR", "value": 428.6, "unit": "s"}], ensure_ascii=False))\n\n\n'
    'if __name__ == "__main__":\n    main()\n'
)
try:
    d = api("POST", "/scripts", {"name": "pilot_rpt_dur", "scriptType": "collect", "description": "试点-报表作业耗时"})
    SID = d["id"]
    api("POST", f"/scripts/{SID}/versions", {"content": PYSCRIPT, "remark": "v1"})
    print("5) script pilot_rpt_dur created id=", SID)
except AssertionError:
    lst = api("GET", "/scripts")
    items = lst["items"] if isinstance(lst, dict) else lst
    SID = [x for x in items if x["name"] == "pilot_rpt_dur"][0]["id"]
    print("5) script pilot_rpt_dur exists id=", SID)

# 6) 流程 (trading_day 模式, 交易日感知; 供 UAT 真实调度)
def mk_flow(code, ds_id, ctype, cconfig, cconfig_clean, market="SH", window="00:20"):
    try:
        d = api("POST", "/flows", {
            "flowCode": "PILOT_FLOW_" + code, "name": "试点" + code, "metricCode": code,
            "dataSourceId": ds_id, "collectType": ctype,
            "collectConfig": cconfig, "cleanConfig": cconfig_clean,
            "scheduleMode": "trading_day", "tradingMarket": market, "windowTime": window,
        })
        print(f"6) flow PILOT_FLOW_{code} created id={d['id']}")
    except AssertionError:
        print(f"6) flow PILOT_FLOW_{code} exists")

mk_flow("TRADE_DB_USAGE", ds_sql, "sql",
        {"sqlTemplate": "SELECT ROUND(db_used_mb * 100.0 / db_total_mb, 2) FROM trade_capacity WHERE id = 1"},
        {})
mk_flow("TRADE_MSG_QUEUE", ds_sql, "sql",
        {"sqlTemplate": "SELECT msg_queue_depth FROM trade_capacity WHERE id = 1"},
        {})
mk_flow("RISK_CPU", ds_log, "logparse",
        {"mode": "regex", "pattern": r"risk cpu_pct=(?P<value>\d+(?:\.\d+)?)",
         "valueField": "value", "fileGlob": "/app/data/files/pilot_*.log"},
        {})
mk_flow("RISK_MEM_GB", ds_log, "logparse",
        {"mode": "regex", "pattern": r"risk mem_gb=(?P<value>\d+(?:\.\d+)?)",
         "valueField": "value", "unitField": "", "fileGlob": "/app/data/files/pilot_*.log"},
        {})
mk_flow("RPT_LOG_SIZE", ds_log, "logparse",
        {"mode": "regex", "pattern": r"rpt log_size_gb=(?P<value>\d+(?:\.\d+)?)",
         "valueField": "value", "fileGlob": "/app/data/files/pilot_*.log"},
        {})
mk_flow("RPT_JOB_DUR", ds_log, "python", {"scriptId": SID, "version": 1}, {})

# 7) 激活 + 手工触发一次冒烟(验证可采集), 随后交由交易日调度
import urllib.request as _ur
def wait_task(flow_id, tries=90):
    for _ in range(tries):
        time.sleep(1)
        items = api("GET", f"/tasks?flowId={flow_id}&pageSize=5")["items"]
        if items and items[0]["status"] in ("success", "failed", "timeout"):
            return items[0]
    raise AssertionError("task not finished")

flows = api("GET", "/flows")["items"]
ok_all = 0
for code in ["TRADE_DB_USAGE", "TRADE_MSG_QUEUE", "RISK_CPU", "RISK_MEM_GB", "RPT_LOG_SIZE", "RPT_JOB_DUR"]:
    fid = [x for x in flows if x["flowCode"] == "PILOT_FLOW_" + code][0]["id"]
    try:
        api("POST", f"/flows/{fid}/activate")
    except AssertionError:
        pass
    api("POST", f"/flows/{fid}/trigger")
    t = wait_task(fid)
    assert t["status"] == "success", f"PILOT_FLOW_{code} 触发失败: {t['status']} {str(t.get('errorMsg'))[:100]}"
    ok_all += 1
    print(f"7) PILOT_FLOW_{code} 冒烟 success (rows={t['rowsCollected']})")
print(f"7) 6/6 试点流程冒烟通过; trading_day 调度已激活({ok_all} 流程)")

print(f"""
== 试点 UAT 环境就绪 ({TODAY}) ==
业务系统: PILOT_TRADE(core, SQL/百分比+绝对值) PILOT_RISK(日志/百分比+绝对值) PILOT_REPORT(日志+脚本/绝对值)
UAT 每日检查项:
  1) 交易日调度是否按时采集:  任务页(交易日 00:20 窗口) / GET /api/v1/tasks?date=<biz_date>
  2) 日汇总是否落地:           /api/v1/data/day?metricCodes=TRADE_DB_USAGE&from=<date>&to=<date+1>
  3) 告警是否生成/闭环:        /api/v1/alerts?status=active (预期: TRADE_DB_USAGE warn 73.73% / RISK_CPU warn 82.4% / RPT_LOG_SIZE warn / RPT_JOB_DUR warn)
  4) 日报生成与导出:           /api/v1/reports/daily/<date> + export?format=xlsx|pdf
  5) 平台自检:                 /api/v1/admin/health
""")
print("PILOT SEED OK")