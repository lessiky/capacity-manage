# -*- coding: utf-8 -*-
"""M5 安全测试 (WBS 5-3): 凭据脱敏 / 权限越权 / 注入 / 会话安全 / AI 调用审计

对应《测试计划》§4.5: 无高危漏洞; 中危漏洞有整改计划。
运行: python testing/security/sec_test.py  (dev 栈需已启动; 建议在 e2e_m5 之后运行)
结果: 控制台输出 + testing/security/results.json
"""
import base64
import hashlib
import hmac
import http.server
import io
import json
import os
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "http://127.0.0.1:8080/api/v1"
MARKER = "Secret@9xKq7"          # 凭据特征串 (TC-SEC-001)
AI_MARK = "password=Secret@9xKq7"  # AI 送模前须被 SanitizeForAI 脱敏
JWT_SECRET = "dev-jwt-secret-change-me-0123456789"  # dev.env (仅测试环境已知)
TODAY = time.strftime("%Y-%m-%d", time.gmtime())  # 业务日 = 容器 UTC 日 (与 e2e_m5 一致)

RESULTS = []
TOKEN = None
WEBHOOK_CAPTURE = []


class SecWebhookHandler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        size = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(size)
        WEBHOOK_CAPTURE.append((dict(self.headers.items()), body))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"code":0}')

    def log_message(self, _format, *_args):
        pass

def check(case_id, title, cond, detail=""):
    RESULTS.append({"id": case_id, "title": title, "passed": bool(cond), "detail": str(detail)[:300]})
    print(f"  [{'PASS' if cond else 'FAIL'}] {case_id} {title}" + (f" | {str(detail)[:200]}" if detail else ""))
    return bool(cond)

def api(method, path, body=None, token=None, timeout=60):
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            b = r.read()
            try:
                return r.status, json.loads(b.decode())
            except Exception:
                return r.status, {"raw": b.decode(errors="replace")[:300]}
    except urllib.error.HTTPError as e:
        b = e.read()
        try:
            return e.code, json.loads(b.decode())
        except Exception:
            return e.code, {"raw": b.decode(errors="replace")[:300]}

def login(u, p):
    s, out = api("POST", "/auth/login", {"username": u, "password": p})
    if s == 200 and out.get("code") == 0:
        d = out.get("data") or {}
        return d.get("accessToken") or d.get("token")
    return None

def make_jwt(payload):
    def b64(b):
        return base64.urlsafe_b64encode(b).rstrip(b"=").decode()
    h = b64(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
    p = b64(json.dumps(payload).encode())
    sig = b64(hmac.new(JWT_SECRET.encode(), f"{h}.{p}".encode(), hashlib.sha256).digest())
    return f"{h}.{p}.{sig}"

def psql(sql):
    r = subprocess.run(
        ["docker", "exec", "capacity-manage-postgres-1", "psql", "-U", "capacity", "-d", "capacity", "-t", "-A", "-c", sql],
        capture_output=True, text=True)
    return r.stdout.strip()

print("== SEC 安全测试 ==")

# ---------- admin ----------
TOKEN = login("admin", "Admin@123")
assert TOKEN, "admin 登录失败"
print("0) admin login ok")

# ---------- SEC-1 凭据脱敏 (TC-SEC-001, FR-COL-02) ----------
print("\n-- SEC-1 凭据脱敏 --")
_, cats = api("GET", "/data-source-categories?systemCode=TEST_SYS01", token=TOKEN)
cat_rows = cats.get("data") or []
if isinstance(cat_rows, dict):
    cat_rows = cat_rows.get("items") or []
if cat_rows:
    cat_id = cat_rows[0]["id"]
else:
    s, out = api("POST", "/data-source-categories", {"systemCode": "TEST_SYS01", "name": "SEC测试分类"}, token=TOKEN)
    assert s == 200 and out.get("code") == 0, out
    cat_id = out["data"]["id"]
s, out = api("POST", "/data-sources", {
    "name": "SEC_MYSQL_TEST", "type": "db",
    "systemCode": "TEST_SYS01", "categoryId": cat_id,
    "config": {"driver": "mysql", "host": "10.99.99.99", "port": 3306,
               "username": "secuser", "password": "x" + MARKER, "database": "secdb"}}, token=TOKEN)
ds_id = (out.get("data") or {}).get("id")
if not ds_id:  # 幂等: 已存在同名数据源则复用
    _, lst0 = api("GET", "/data-sources", token=TOKEN)
    rows0 = lst0.get("data") or []
    if isinstance(rows0, dict):
        rows0 = rows0.get("items") or []
    ds_id = next((x["id"] for x in rows0 if x["name"] == "SEC_MYSQL_TEST"), None)
    assert ds_id, f"SEC 数据源创建失败且未找到已有记录: {out}"
    print("1) SEC 数据源 exists id=", ds_id)
else:
    print("1) SEC 数据源 created id=", ds_id)

lst_s, lst = api("GET", "/data-sources", token=TOKEN)
lst_json = json.dumps(lst, ensure_ascii=False)
check("TC-SEC-001a", "数据源列表不含明文凭据", MARKER not in lst_json)
det_s, det = api("GET", f"/data-sources/{ds_id}", token=TOKEN)
det_json = json.dumps(det, ensure_ascii=False)
check("TC-SEC-001b", "数据源详情不含明文凭据(已脱敏)", MARKER not in det_json and ("******" in det_json or "***" in det_json), det.get("data", {}).get("config", "")[:80])
dbcfg = psql(f"SELECT config FROM data_source WHERE id = {ds_id}")
check("TC-SEC-001c", "DB 中 config 为密文(无明文特征串)", MARKER not in dbcfg and len(dbcfg) > 10, dbcfg[:60])

# 运行日志不得出现明文凭据: 触发一次连接测试后查 operate-log
api("POST", f"/data-sources/{ds_id}/test", token=TOKEN)
time.sleep(0.3)
s, out = api("GET", "/admin/operate-logs?pageSize=20", token=TOKEN)
logs_json = json.dumps(out, ensure_ascii=False)
check("TC-SEC-001d", "操作日志不含明文凭据", MARKER not in logs_json)

# ---------- SEC-2 权限越权 ----------
print("\n-- SEC-2 权限越权 --")
# 垂直越权: 仅 dashboard:view 的角色
try:
    api("POST", "/admin/roles", {"code": "SEC_VIEWER", "name": "SEC越权测试", "remark": "e2e 可重复",
        "permissions": ["dashboard:view"]}, token=TOKEN)
    print("2) role SEC_VIEWER created")
except Exception:
    print("2) role SEC_VIEWER exists")
try:
    api("POST", "/admin/users", {"username": "secviewer", "realName": "SEC查看者",
        "password": "Sec@Viewer1", "roleCodes": ["SEC_VIEWER"]}, token=TOKEN)
    print("2) user secviewer created")
except Exception:
    print("2) user secviewer exists")
vtok = login("secviewer", "Sec@Viewer1")
assert vtok, "secviewer 登录失败"

s, out = api("POST", "/systems", {"systemCode": "SEC_HACK_SYS", "name": "越权", "owner": "x", "level": "general"}, token=vtok)
check("TC-SEC-010a", "垂直越权: 创建系统被拒(403)", s == 403 and out.get("code") == 40300, f"HTTP {s} code={out.get('code')}")
s, out = api("PUT", "/metrics/M5_SQL", {"name": "hack"}, token=vtok)
check("TC-SEC-010b", "垂直越权: 修改指标被拒(403)", s == 403, f"HTTP {s} code={out.get('code')}")
s, out = api("GET", "/admin/operate-logs?pageSize=5", token=vtok)
check("TC-SEC-010c", "垂直越权: 查看操作日志被拒(403)", s == 403, f"HTTP {s} code={out.get('code')}")
s, out = api("POST", f"/reports/daily/{TODAY}/generate", token=vtok)
check("TC-SEC-010d", "垂直越权: 生成日报被拒(403)", s == 403, f"HTTP {s} code={out.get('code')}")
s, out = api("GET", "/data-sources", token=vtok)
check("TC-SEC-010e", "垂直越权: 查看数据源被拒(403)", s == 403, f"HTTP {s} code={out.get('code')}")

# 水平越权: 数据权限 (m5scoped 仅授权 M5_SYS, 由 e2e_m5 创建)
# 兼容旧角色: 确保 M5_SCOPE_ROLE 具备数据面查看权限
_, roles_out = api("GET", "/admin/roles", token=TOKEN)
for r in (roles_out.get("data") or []):
    if r.get("code") == "M5_SCOPE_ROLE":
        have = set(r.get("permissions") or [])
        need = {"metric:view", "alert:view", "flow:view", "task:view"}
        if not need <= have:
            api("PUT", f"/admin/roles/{r['id']}", {"name": r.get("name", "M5数据权限E2E"),
                "permissions": sorted(have | need)}, token=TOKEN)
            print("2) M5_SCOPE_ROLE permissions patched")
        break
time.sleep(0.3)
stok = login("m5scoped", "M5@Scope123")
if stok:
    s, out = api("GET", "/systems/TEST_SYS01", token=stok)
    check("TC-SEC-011a", "水平越权: 越权系统详情 403", s == 403 and out.get("code") == 40301, f"HTTP {s} code={out.get('code')}")
    s, out = api("GET", "/metrics/M2_LOGSIZE", token=stok)
    check("TC-SEC-011b", "水平越权: 越权指标详情 403", s == 403, f"HTTP {s} code={out.get('code')}")
    s, out = api("GET", "/metrics/M2_LOGSIZE/trend?days=30", token=stok)
    check("TC-SEC-011c", "水平越权: 越权指标趋势 403", s == 403, f"HTTP {s} code={out.get('code')}")
    s, out = api("GET", "/data/rt?metricCodes=M2_LOGSIZE&from=2026-01-01&to=2027-01-01", token=stok)
    check("TC-SEC-011d", "水平越权: 越权日内数据 403", s == 403, f"HTTP {s} code={out.get('code')}")
    s, out = api("GET", "/data/day?metricCodes=M2_LOGSIZE&from=2026-01-01&to=2027-01-01", token=stok)
    check("TC-SEC-011e", "水平越权: 越权日数据 403", s == 403, f"HTTP {s} code={out.get('code')}")
    # 授权系统内数据可见
    s, out = api("GET", "/data/rt?metricCodes=M5_SQL&from=2026-01-01&to=2027-01-01", token=stok)
    ok_in = s == 200 and out.get("code") == 0
    check("TC-SEC-011f", "授权范围内数据正常可见(M5_SQL)", ok_in, f"HTTP {s}")
    s, out = api("GET", "/data/rt?from=2026-01-01&to=2027-01-01&limit=100", token=stok)
    codes = {p.get("metricCode") for p in (out.get("data") or [])} if ok_in else set()
    _, am0 = api("GET", "/metrics?systemCode=M5_SYS&pageSize=500", token=TOKEN)
    m5set = {m["metricCode"] for m in (am0.get("data") or {}).get("items", [])}
    check("TC-SEC-011g", "不限定指标时仅返回授权系统指标", s == 200 and out.get("code") == 0
          and codes and codes <= m5set,
          f"codes={sorted(codes)[:6]} authorized={sorted(m5set)}")
    s, out = api("GET", "/metrics?pageSize=200", token=stok)
    m_codes = {m.get("systemCode") for m in (out.get("data") or {}).get("items", [])} if s == 200 else set()
    check("TC-SEC-011h", "指标列表仅含授权系统", s == 200 and out.get("code") == 0 and m_codes == {"M5_SYS"}, f"systems={m_codes}")
    s, out = api("GET", "/alerts?pageSize=200", token=stok)
    a_codes = {a.get("systemCode") for a in (out.get("data") or {}).get("items", [])} if s == 200 else set()
    check("TC-SEC-011i", "告警列表仅含授权系统", s == 200 and out.get("code") == 0 and (a_codes <= {"M5_SYS"}), f"systems={a_codes}")
    _, am = api("GET", "/metrics?systemCode=M5_SYS&pageSize=500", token=TOKEN)
    exp_metrics = len((am.get("data") or {}).get("items", []))
    s, out = api("GET", "/dashboard/overview", token=stok)
    ov = out.get("data") or {}
    check("TC-SEC-011j", "大盘 KPI 按数据权限收敛", s == 200 and out.get("code") == 0
          and ov.get("systemCount", 99) == 1 and ov.get("metricCount", 99) == exp_metrics and exp_metrics >= 4,
          f"systemCount={ov.get('systemCount')} metricCount={ov.get('metricCount')} expected={exp_metrics}")
    s, out = api("GET", "/flows?pageSize=200", token=stok)
    f_codes = {f.get("metricCode") for f in (out.get("data") or {}).get("items", [])} if s == 200 else set()
    check("TC-SEC-011k", "流程列表仅含授权系统指标", s == 200 and out.get("code") == 0
          and f_codes and f_codes <= m5set and f_codes & {"M5_SQL", "M5_LOG", "M5_PY", "M5_CLEAN"}, f"metrics={f_codes}")
    s, out = api("GET", "/tasks?pageSize=200", token=stok)
    t_codes = {t.get("metricCode") for t in (out.get("data") or {}).get("items", [])} if s == 200 else set()
    check("TC-SEC-011l", "任务列表仅含授权系统指标", s == 200 and out.get("code") == 0
          and t_codes and t_codes <= m5set and t_codes & {"M5_SQL", "M5_LOG", "M5_PY", "M5_CLEAN"}, f"metrics={t_codes}")
else:
    check("TC-SEC-011", "m5scoped 用户可用(需先运行 e2e_m5)", False, "m5scoped 登录失败, 请先运行 e2e_m5.py")

# ---------- SEC-3 注入 ----------
print("\n-- SEC-3 注入 --")
s, out = api("POST", "/auth/login", {"username": "admin' OR '1'='1", "password": "x"})
check("TC-SEC-020a", "SQLi 登录: 未绕过认证", s in (400, 401) and out.get("code") in (40100, 40000), f"HTTP {s} code={out.get('code')}")
s, out = api("POST", "/auth/login", {"username": "' OR 1=1--", "password": "anything"})
check("TC-SEC-020b", "SQLi 登录(变体): 未绕过认证", s in (400, 401) and out.get("code") in (40100, 40000), f"HTTP {s} code={out.get('code')}")
s, out = api("GET", "/alerts?metricCode=" + urllib.parse.quote("M5_SQL' OR '1'='1"), token=TOKEN)
check("TC-SEC-021a", "SQLi 查询参数: 无 5xx/异常", s == 200 and out.get("code") == 0, f"HTTP {s} code={out.get('code')}")
s, out = api("GET", "/metrics?keyword=" + urllib.parse.quote("'; DROP TABLE capacity_metric;--"), token=TOKEN)
check("TC-SEC-021b", "SQLi 关键字: 无 5xx/异常", s == 200 and out.get("code") == 0, f"HTTP {s}")
# 设计即安全: format 仅在 xlsx/pdf 两个固定 key/格式间选择, 文件路径不来自用户输入
s, out = api("GET", f"/reports/daily/{TODAY}/file?format=..%2F..%2Fetc%2Fpasswd", token=TOKEN)
raw_a = str(out.get("raw", ""))
body_txt = json.dumps(out, ensure_ascii=False)
check("TC-SEC-022a", "路径穿越: 仅返回固定日报文件(xlsx)或404, 无文件内容泄漏",
      s in (200, 404) and "root:" not in body_txt and (s != 200 or raw_a.startswith("PK")), f"HTTP {s}")
s, out = api("GET", f"/reports/daily/{TODAY}/export?format=..%5c..%5cwindows%5cwin.ini", token=TOKEN)
raw_b = str(out.get("raw", ""))
check("TC-SEC-022b", "路径穿越(反斜杠编码): 仅返回固定格式(xlsx)或错误, 无文件内容泄漏",
      s in (200, 400, 404) and "root:" not in json.dumps(out, ensure_ascii=False)
      and (s != 200 or raw_b.startswith("PK")), f"HTTP {s}")
# 命令注入/特殊字符在配置字段中仅作为数据
s, out = api("POST", "/scripts", {"name": "sec_inj;$(echo PWNED) `id`", "scriptType": "collect", "description": "注入测试"}, token=TOKEN)
inj_name = (out.get("data") or {}).get("name", "")
if not inj_name:  # 幂等: 已存在则读取存储名
    _, sc_l0 = api("GET", "/scripts", token=TOKEN)
    for sc in (sc_l0.get("data") or []):
        if sc.get("name", "").startswith("sec_inj"):
            inj_name = sc["name"]
            break
check("TC-SEC-023a", "命令注入: 脚本名原样存储不执行", s in (200, 409) and inj_name == "sec_inj;$(echo PWNED) `id`", f"HTTP {s} name={inj_name!r}")
s, out = api("POST", "/metrics", {"metricCode": "SEC_XSS", "name": "<img src=x onerror=alert(1)>",
    "systemCode": "M5_SYS", "category": "storage", "metricType": "absolute", "unit": "GB",
    "statMode": "latest", "judgeDirection": "higher_worse", "thresholdMode": "absolute",
    "baseline": 100, "warnLine": 50, "dangerLine": 80}, token=TOKEN)
s2, out2 = api("GET", "/metrics/SEC_XSS", token=TOKEN)
check("TC-SEC-023b", "XSS 载荷: 仅 JSON 存储/返回, 服务端不解析", s in (200, 409) and s2 == 200
      and (out2.get("data") or {}).get("metric", {}).get("name") == "<img src=x onerror=alert(1)>", f"HTTP {s}/{s2}")
s, out = api("POST", "/auth/login", {"username": "A" * 5000, "password": "x"})
check("TC-SEC-024a", "超长用户名: 无 5xx", s in (400, 401, 413), f"HTTP {s}")

# ---------- SEC-4 会话安全 ----------
print("\n-- SEC-4 会话安全 --")
s, out = api("GET", "/systems", token=None)
check("TC-SEC-030a", "无 token: 401(40100)", s == 401 and out.get("code") == 40100, f"HTTP {s} code={out.get('code')}")
s, out = api("GET", "/systems", token="Bearer-bad-token" and "badtoken.invalid.here")
check("TC-SEC-030b", "非法 token: 401", s == 401, f"HTTP {s} code={out.get('code')}")
tampered = TOKEN[:-4] + ("AAAA" if not TOKEN.endswith("AAAA") else "BBBB")
s, out = api("GET", "/systems", token=tampered)
check("TC-SEC-030c", "篡改签名: 401", s == 401, f"HTTP {s}")
now = int(time.time())
expired = make_jwt({"uid": 1, "username": "admin", "roles": ["platform_admin"],
                    "exp": now - 3600, "iat": now - 7200, "jti": "sec-expired-test"})
s, out = api("GET", "/systems", token=expired)
check("TC-SEC-030d", "过期 token: 401", s == 401, f"HTTP {s} code={out.get('code')}")
unknown = make_jwt({"uid": 1, "username": "admin", "roles": ["platform_admin"],
                    "exp": now + 3600, "iat": now, "jti": "sec-unknown-sess"})
s, out = api("GET", "/systems", token=unknown)
check("TC-SEC-030e", "无服务端会话的 token: 401", s == 401, f"HTTP {s}")
# 登出后 token 失效
ltok = login("secviewer", "Sec@Viewer1")
api("POST", "/auth/logout", token=ltok)
time.sleep(0.3)
s, out = api("GET", "/systems", token=ltok)
check("TC-SEC-031a", "登出后 token 立即失效", s == 401, f"HTTP {s} code={out.get('code')}")
# 暴力锁定: 5 次失败锁定 15 分钟
try:
    api("POST", "/admin/users", {"username": "seclbrute", "realName": "SEC锁定测试",
        "password": "Sec@Brute1", "roleCodes": ["SEC_VIEWER"]}, token=TOKEN)
    print("4) user seclbrute created")
except Exception:
    print("4) user seclbrute exists")
# 幂等: 上一轮清理可能已停用 seclbrute, 先恢复为 active
_, u0 = api("GET", "/admin/users?pageSize=200", token=TOKEN)
for u in (u0.get("data") or {}).get("items", []):
    if u["username"] == "seclbrute" and u.get("status") == "disabled":
        api("PUT", f"/admin/users/{u['id']}", {"realName": u.get("realName", "SEC锁定测试"), "status": "active"}, token=TOKEN)
        print("4) seclbrute re-enabled")
for i in range(5):
    s, out = api("POST", "/auth/login", {"username": "seclbrute", "password": "wrong-pass-00"})
s, out = api("POST", "/auth/login", {"username": "seclbrute", "password": "Sec@Brute1"})
locked = out.get("code") == 40101
check("TC-SEC-032a", "连续 5 次失败后锁定(40101, 正确密码也拒绝)", locked, f"HTTP {s} code={out.get('code')} msg={out.get('message','')[:40]}")

# ---------- SEC-5 AI 调用审计 ----------
print("\n-- SEC-5 AI 调用审计 --")
logs_before_s, logs_before = api("GET", "/ai/logs", token=TOKEN)
n_before = len(logs_before.get("data") or [])
s, out = api("POST", "/ai/chat", {"message": "采集配置咨询 " + AI_MARK, "context": ""}, token=TOKEN, timeout=150)
ai_ok = s in (200, 503)
time.sleep(0.5)
logs_s, logs = api("GET", "/ai/logs", token=TOKEN)
items = logs.get("data") or []
new_items = items[:max(0, len(items) - n_before)]
check("TC-SEC-040a", "AI 调用产生审计记录(含降级)", ai_ok and len(new_items) >= 1 and new_items[0].get("feature") == "assistant",
      f"HTTP {s} new={len(new_items)}")
all_json = json.dumps(items, ensure_ascii=False)
check("TC-SEC-040b", "审计记录不含凭据原文(已脱敏)", AI_MARK.split("password=")[1] not in all_json,
      "raw credential absent")
s, out = api("GET", "/ai/logs", token=vtok)
check("TC-SEC-041a", "AI 审计仅限 admin:log:view", s == 403, f"HTTP {s} code={out.get('code')}")

# ---------- SEC-6 M8 开放 API、Webhook、SSRF 与事件权限 ----------
print("\n-- SEC-6 M8 开放 API、Webhook、SSRF 与事件权限 --")
s, out = api("POST", "/open-api/keys", {
    "name": "SEC_M8_API", "scopes": ["data:read"], "rateLimit": 60, "systemScope": [],
}, token=TOKEN)
check("TC-SEC-050a", "API Key 管理接口可用", s == 200 and out.get("code") == 0, f"HTTP {s} code={out.get('code')}")
key = out.get("data") or {}
key_req = urllib.request.Request(BASE + "/open/stream/day?metricCode=M5_SQL")
key_req.add_header("X-API-Key", key.get("apiKey", ""))
try:
    with urllib.request.urlopen(key_req, timeout=30) as resp:
        key_ok = resp.status == 200
except urllib.error.HTTPError as exc:
    key_ok = False
check("TC-SEC-050b", "API Key 正确 secret 可访问授权 scope", key_ok, "HTTP 200")
bad_key_req = urllib.request.Request(BASE + "/open/stream/day?metricCode=M5_SQL")
bad_key_req.add_header("X-API-Key", key.get("keyId", "") + ".wrongsecret")
try:
    urllib.request.urlopen(bad_key_req, timeout=30)
    bad_status = 200
except urllib.error.HTTPError as exc:
    bad_status = exc.code
check("TC-SEC-050c", "API Key 错误 secret 被拒", bad_status == 401, f"HTTP {bad_status}")
scope_req = urllib.request.Request(BASE + "/open/forecast?metricCode=M5_SQL&horizon=30")
scope_req.add_header("X-API-Key", key.get("apiKey", ""))
try:
    urllib.request.urlopen(scope_req, timeout=30)
    scope_status = 200
except urllib.error.HTTPError as exc:
    scope_status = exc.code
check("TC-SEC-050d", "API Key scope 越权被拒", scope_status == 403, f"HTTP {scope_status}")

webhook_srv = http.server.ThreadingHTTPServer(("0.0.0.0", 0), SecWebhookHandler)
webhook_thread = threading.Thread(target=webhook_srv.serve_forever, daemon=True)
webhook_thread.start()
webhook_id = None
try:
    webhook_port = webhook_srv.server_address[1]
    s, out = api("POST", "/notification-channels", {
        "name": "SEC_M8_WEBHOOK", "type": "webhook", "enabled": True,
        "config": {"url": f"http://host.docker.internal:{webhook_port}/sec", "secret": MARKER},
    }, token=TOKEN)
    if s == 409:
        _, ch_list = api("GET", "/notification-channels", token=TOKEN)
        old = next((x for x in (ch_list.get("data") or {}).get("items", []) if x["name"] == "SEC_M8_WEBHOOK"), None)
        if old:
            webhook_id = old["id"]
            api("PUT", f"/notification-channels/{webhook_id}", {
                "name": "SEC_M8_WEBHOOK", "type": "webhook", "enabled": True,
                "config": {"url": f"http://host.docker.internal:{webhook_port}/sec", "secret": MARKER},
            }, token=TOKEN)
            s = 200
            out = {"code": 0, "data": {"id": webhook_id}}
    else:
        webhook_id = (out.get("data") or {}).get("id")
    assert s == 200 and out.get("code") == 0 and webhook_id, out
    s, out = api("POST", f"/notification-channels/{webhook_id}/test", token=TOKEN)
    deadline = time.time() + 10
    while not WEBHOOK_CAPTURE and time.time() < deadline:
        time.sleep(0.1)
    headers, body = WEBHOOK_CAPTURE[-1] if WEBHOOK_CAPTURE else ({}, b"")
    ts = headers.get("X-Cap-Timestamp", "")
    sig = headers.get("X-Cap-Signature", "")
    want_sig = hmac.new(MARKER.encode(), ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    check("TC-SEC-051a", "Webhook HMAC-SHA256 签名与原始 body 一致",
          bool(ts and sig and hmac.compare_digest(sig, want_sig)), f"ts={bool(ts)} sig={bool(sig)}")
    check("TC-SEC-051b", "Webhook 幂等键头存在",
          headers.get("X-Cap-Idempotency-Key", "").startswith("channel-test:"),
          headers.get("X-Cap-Idempotency-Key", ""))
finally:
    if webhook_id:
        api("DELETE", f"/notification-channels/{webhook_id}", token=TOKEN)
    webhook_srv.shutdown()
    webhook_srv.server_close()

for idx, target in enumerate([
    "http://169.254.169.254/latest/meta-data",
    "http://127.0.0.1:8080/internal",
    "http://100.100.100.200/latest/meta-data",
], 1):
    s, out = api("POST", "/notification-channels", {
        "name": f"SEC_M8_SSRF_{idx}", "type": "webhook", "enabled": True,
        "config": {"url": target, "secret": "x"},
    }, token=TOKEN)
    check(f"TC-SEC-052{chr(96 + idx)}", f"SSRF 目标被拒: {target}", s == 422, f"HTTP {s} code={out.get('code')}")

s, out = api("POST", "/notification-templates", {
    "name": "SEC_M8_TEMPLATE_INJECTION", "event": "alert", "channelType": "webhook",
    "format": "text", "subject": "注入", "body": '${metric} {{printf "%s" "PWNED"}}',
}, token=TOKEN)
check("TC-SEC-053a", "通知模板拒绝可执行模板语法", s == 422, f"HTTP {s} code={out.get('code')}")
s, out = api("POST", "/forecast-events", {
    "name": "SEC M8 越权", "eventType": "peak", "scopeType": "global",
    "startDate": TODAY, "impactDirection": "increase",
}, token=vtok)
check("TC-SEC-054a", "事件因子写权限越权被拒", s == 403, f"HTTP {s} code={out.get('code')}")

# ---------- 清理 ----------
print("\n-- 清理 --")
api("DELETE", "/metrics/SEC_XSS", token=TOKEN)
_, sc_l1 = api("GET", "/scripts", token=TOKEN)
for sc in (sc_l1.get("data") or []):
    if sc.get("name", "").startswith("sec_inj"):
        api("DELETE", f"/scripts/{sc['id']}", token=TOKEN)
        print("cleaned: sec_inj 脚本已删除")
s, out = api("GET", "/admin/users?pageSize=200", token=TOKEN)
for u in (out.get("data") or {}).get("items", []):
    if u["username"] == "seclbrute":
        api("PUT", f"/admin/users/{u['id']}", {"realName": "SEC锁定测试", "status": "disabled"}, token=TOKEN)
api("POST", f"/data-sources/{ds_id}/disable", token=TOKEN)
print("cleaned: SEC_XSS 删除 / seclbrute 停用 / SEC_MYSQL_TEST 停用")

# ---------- 汇总 ----------
passed = sum(1 for r in RESULTS if r["passed"])
failed = [r for r in RESULTS if not r["passed"]]
print(f"\nSEC 结果: {passed}/{len(RESULTS)} 通过")
for r in failed:
    print(f"  FAIL {r['id']} {r['title']} | {r['detail'][:150]}")
outp = os.path.join(os.path.dirname(os.path.abspath(__file__)), "results.json")
io.open(outp, "w", encoding="utf-8").write(json.dumps(
    {"date": time.strftime("%Y-%m-%d %H:%M:%S"), "total": len(RESULTS), "passed": passed,
     "failed": len(failed), "results": RESULTS}, ensure_ascii=False, indent=2))
print(f"results -> {outp}")
if failed:
    raise SystemExit(1)
print("ALL SEC CHECKS PASSED")
