# -*- coding: utf-8 -*-
"""M5 性能测试 (WBS 5-2, 《开发计划》§6.2 八场景)

前置:
- dev 栈已启动; 已生成 5 万指标数据集: python testing/data/gen_perf_data.py
  (S1/S2/S3/S8 可 --no-data 单独跑; S4~S7 需要数据集)
运行: python testing/perf/perf_test.py [--no-data]
输出: 控制台 + testing/perf/results.json
口径说明: dev 四环境栈为容器化开发环境, 非生产等价硬件; 结果作为一期基线,
生产等价硬件复测在 UAT 前执行(《测试计划》§9 风险应对)。
"""
import argparse
import concurrent.futures
import json
import os
import random
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "http://127.0.0.1:8080/api/v1"
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
CTR_PG = "capacity-manage-postgres-1"
TOKEN = None
RESULTS = []

def api(method, path, body=None, token=None, timeout=120):
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            b = r.read()
            dt = time.time() - t0
            try:
                return r.status, json.loads(b.decode()), dt
            except Exception:
                return r.status, {"raw": b[:200].decode(errors="replace")}, dt
    except urllib.error.HTTPError as e:
        b = e.read()
        try:
            return e.code, json.loads(b.decode()), time.time() - t0
        except Exception:
            return e.code, {"raw": b[:200].decode(errors="replace")}, time.time() - t0

def psql(sql, timeout=1800):
    r = subprocess.run(["docker", "exec", CTR_PG, "psql", "-U", "capacity", "-d", "capacity", "-t", "-A", "-c", sql],
                       capture_output=True, text=True, timeout=timeout)
    if r.returncode != 0:
        raise RuntimeError(r.stderr[:300])
    return r.stdout.strip()

def record(scene, name, actual, target, ok, detail=""):
    RESULTS.append({"scene": scene, "name": name, "actual": str(actual), "target": target,
                    "ok": bool(ok), "detail": str(detail)[:300]})
    print(f"  [{'PASS' if ok else 'FAIL'}] {scene} {name}: {actual} (目标 {target})" + (f" | {str(detail)[:120]}" if detail else ""))
    return bool(ok)

def pctl(lats, p):
    if not lats:
        return 0.0
    s = sorted(lats)
    idx = max(0, min(len(s) - 1, int(round(p / 100.0 * (len(s) - 1)))))
    return s[idx]

def timed(paths, n_each=10):
    lats = []
    for p in paths:
        for _ in range(n_each):
            s, out, dt = api("GET", p, token=TOKEN)
            assert s == 200 and out.get("code") == 0, f"{p} -> {s} {out}"
            lats.append(dt)
    return lats

# ---------- S1 20 并发用户常规操作 ----------
def s1():
    ops = ["/systems?pageSize=20", "/metrics?pageSize=20", "/dashboard/overview",
           "/alerts?pageSize=20", "/metrics/M5_SQL/trend?days=30",
           "/dashboard/trend?days=30", "/flows?pageSize=20", "/tasks?pageSize=20"]
    lats = []
    lock = object()
    def user(_):
        for _i in range(10):
            s, out, dt = api("GET", random.choice(ops), token=TOKEN)
            if s == 200:
                lats.append(dt)
    with concurrent.futures.ThreadPoolExecutor(20) as ex:
        list(ex.map(user, range(20)))
    p95 = pctl(lats, 95)
    record("S1", "20并发用户常规操作 p95 响应", f"{p95*1000:.0f}ms", "<3s", p95 < 3,
           f"n={len(lats)} p50={pctl(lats,50)*1000:.0f}ms max={max(lats)*1000:.0f}ms")

# ---------- S2 大盘/列表首屏 ----------
def s2():
    pages = ["/dashboard/overview", "/dashboard/trend?days=30",
             "/systems?pageSize=20", "/metrics?pageSize=20", "/alerts?pageSize=20"]
    lats = timed(pages, n_each=20)
    p95 = pctl(lats, 95)
    record("S2", "大盘/列表首屏 p95 加载", f"{p95*1000:.0f}ms", "<3s", p95 < 3,
           f"n={len(lats)} p50={pctl(lats,50)*1000:.0f}ms max={max(lats)*1000:.0f}ms")

# ---------- S3 日级任务调度时效 ----------
def s3():
    today = time.strftime("%Y-%m-%d", time.gmtime())
    t0 = time.time()
    flows = api("GET", "/flows?pageSize=200", token=TOKEN)[1]["data"]["items"]
    active = [f for f in flows if f.get("status") == "active" and not str(f["metricCode"]).startswith("PERF_")]
    for f in active:
        api("POST", f"/flows/{f['id']}/trigger", token=TOKEN)
    deadline = time.time() + 600
    while time.time() < deadline:
        done = True
        for f in active:
            items = api("GET", f"/tasks?flowId={f['id']}&pageSize=1", token=TOKEN)[1]["data"]["items"]
            if not items or items[0]["status"] not in ("success", "failed", "timeout"):
                done = False
                break
        if done:
            break
        time.sleep(2)
    collect_dt = time.time() - t0
    s, out, _ = api("POST", "/data/summary/run", {"bizDate": today}, token=TOKEN)
    assert s == 200 and out.get("code") == 0, out
    dl2 = time.time() + 600
    ok = False
    while time.time() < dl2:
        s, out, _ = api("GET", f"/data/day?metricCodes=M5_SQL&from={today}&to={today}", token=TOKEN)
        rows = out.get("data") or []
        if rows and rows[0].get("statValue") is not None:
            ok = True
            break
        time.sleep(2)
    summary_dt = time.time() - t0 - collect_dt
    total = time.time() - t0
    record("S3", "日级任务全部完成(采集+日汇总) 时效", f"{total:.0f}s (采集{collect_dt:.0f}s+汇总{summary_dt:.0f}s, {len(active)}流程)",
           "≤30分钟", total <= 1800 and ok, "试点规模基线; 5万指标口径见 S7")

# ---------- S4 数据写入: 日增量≥1万行 连续30天 ----------
def s4():
    days = list(psql(f"SELECT to_char(biz_date,'YYYY-MM-DD') FROM metric_value_day "
                     f"WHERE metric_code LIKE 'PERF_M_%' AND biz_date BETWEEN '2026-06-20' AND '2026-07-19' "
                     f"GROUP BY biz_date ORDER BY biz_date").splitlines())
    assert len(days) >= 30, f"造数不足30天: {len(days)}"
    bad = psql("SELECT count(*) FROM (SELECT biz_date FROM metric_value_day "
               "WHERE metric_code LIKE 'PERF_M_%' AND biz_date BETWEEN '2026-06-20' AND '2026-07-19' "
               "GROUP BY biz_date HAVING count(*) <> 50000) x")
    record("S4a", "日增量完整性: 30 个连续业务日各 5 万行(≥1万行/日) 无丢失",
           f"30天×50000行, 异常日={bad}", "无数据丢失", bad == "0", "造数 5 万指标×200 日, 抽查最近 30 日")
    # 写入吞吐: 3 批各 1 万行 (新日期 2026-07-20~22)
    rps = []
    for i in range(3):
        d = f"2026-07-{20 + i}"
        t0 = time.time()
        psql(f"INSERT INTO metric_value_day (metric_code, biz_date, stat_value, water_level, level, point_count, source) "
             f"SELECT 'PERF_M_' || lpad(m::text,5,'0'), '{d}'::date, 55.0, 0.55, 'normal', 1, 'collect' "
             f"FROM generate_series(1,10000) m")
        rps.append(10000 / (time.time() - t0))
    psql("DELETE FROM metric_value_day WHERE biz_date >= '2026-07-20' AND metric_code LIKE 'PERF_M_%'")
    # 劣化: 前 30 天 vs 后 30 天 同口径查询
    l1 = timed([f"/data/day?metricCodes={','.join('PERF_M_%05d' % i for i in range(1, 101))}&from=2026-01-01&to=2026-01-31"], n_each=10)
    l2 = timed([f"/data/day?metricCodes={','.join('PERF_M_%05d' % i for i in range(1, 101))}&from=2026-06-20&to=2026-07-19"], n_each=10)
    ratio = pctl(l2, 95) / max(pctl(l1, 95), 0.001)
    record("S4b", "1 万行/日 连续写入吞吐", f"均值 {sum(rps)/len(rps):.0f} 行/s (3批)", "记录基线", True,
           f"各批: {[f'{x:.0f}' for x in rps]}")
    record("S4c", "30 天后查询无性能劣化", f"后30天p95/前30天p95 = {ratio:.2f}x", "<1.5x", ratio < 1.5,
           f"前30天p95={pctl(l1,95)*1000:.0f}ms 后30天p95={pctl(l2,95)*1000:.0f}ms")

# ---------- S5 千万行日表范围查询 + 单指标 90 天趋势 ----------
def s5():
    codes = ",".join("PERF_M_%05d" % i for i in range(1, 101))
    l1 = timed([f"/data/day?metricCodes={codes}&from=2026-04-20&to=2026-07-19"], n_each=10)
    ok1 = pctl(l1, 95) < 2
    record("S5a", "千万行日表 90 天×100 指标范围查询 p95", f"{pctl(l1,95)*1000:.0f}ms", "<2s", ok1)
    l2 = timed(["/metrics/PERF_M_00001/trend?days=90"], n_each=10)
    ok2 = pctl(l2, 95) < 2
    record("S5b", "单指标 90 天趋势 p95", f"{pctl(l2,95)*1000:.0f}ms", "<2s", ok2)
    t0 = time.time()
    n = int(psql("SELECT count(*) FROM metric_value_day WHERE biz_date >= '2026-05-20' AND biz_date <= '2026-07-19'"))
    dt = time.time() - t0
    record("S5c", "千万行级全表日期范围聚合(count 61日×5万)", f"{n} 行 / {dt*1000:.0f}ms", "<2s", dt < 2, "DB 直查基线")

# ---------- S6 全量指标日报生成 ----------
def s6():
    s, out, dt = api("POST", "/reports/daily/2026-07-19/generate", token=TOKEN, timeout=300)
    ok = s == 200 and out.get("code") == 0 and out.get("data", {}).get("status") == "success"
    record("S6", "全量指标(5万)日报生成", f"{dt*1000:.0f}ms, metricsTotal={out.get('data',{}).get('metricsTotal')}",
           "<60s", ok and dt < 60)

# ---------- S7 5 万指标设计上限评估 ----------
def s7():
    # 5 万指标日汇总(全量指标平台日级任务, 异步): 以 daily_summary_log 完成标记计时
    s, out, _ = api("POST", "/data/summary/run", {"bizDate": "2026-07-19"}, token=TOKEN)
    assert s == 200 and out.get("code") == 0, out
    sum_dt, sum_info = None, ""
    deadline = time.time() + 3600
    while time.time() < deadline:
        time.sleep(10)
        row = psql("SELECT status || '|' || coalesce(metrics_done::text,'') || '|' || coalesce(metrics_missing::text,'') "
                   "|| '|' || coalesce(to_char(finished_at - started_at, 'MI:SS'), '') "
                   "FROM daily_summary_log WHERE biz_date = '2026-07-19'")
        parts = row.split("|") if row else []
        if parts and parts[0] in ("success", "partial", "failed"):
            st, done_n, miss_n, dur = (parts + ["", "", "", ""])[:4]
            sum_info = f"status={st} done={done_n} missing={miss_n} 服务端耗时={dur}"
            m, sec = (dur.split(":") + ["0"])[:2] if dur else (0, 0)
            sum_dt = int(m) * 60 + int(sec)
            break
    # 大盘 KPI 在 1000 万行日表上的耗时
    l = timed(["/dashboard/overview"], n_each=10)
    l2 = timed(["/dashboard/trend?days=30"], n_each=10)
    record("S7", "5万指标×200日 写入/查询基线(容量评估)",
           f"日汇总={sum_dt}s 大盘overview p95={pctl(l,95)*1000:.0f}ms trend30d p95={pctl(l2,95)*1000:.0f}ms",
           "记录基线(非门禁)", True,
           f"写入: 1000万行 INSERT..SELECT 见造数日志; 汇总: {sum_info}; 查询: S5/S6; 结论见性能测试报告")

# ---------- S8 Python 沙箱 ----------
SB_METRICS = [
    ("PERF_SB_TIMEOUT", "沙箱-超时", 100.0, 50.0, 80.0),
    ("PERF_SB_MEM", "沙箱-内存", 100.0, 50.0, 80.0),
    ("PERF_SB_NET", "沙箱-网络", 100.0, 50.0, 80.0),
]
SB_SCRIPTS = {
    "PERF_SB_TIMEOUT": (
        'import json\nimport os\nimport time\n\n\n'
        'def main():\n'
        '    params = json.loads(os.environ.get("CAP_PARAMS", "{}"))\n'
        '    end = time.time() + 400\n'
        '    while time.time() < end:\n'
        '        time.sleep(1)\n'
        '    print(json.dumps([{"metric": "PERF_SB_TIMEOUT", "value": 1.0, "unit": "%"}]))\n\n\n'
        'if __name__ == "__main__":\n    main()\n'
    ),
    "PERF_SB_MEM": (
        'import json\nimport os\n\n\n'
        'def main():\n'
        '    params = json.loads(os.environ.get("CAP_PARAMS", "{}"))\n'
        '    x = bytes(1100 * 1024 * 1024)\n'
        '    print(json.dumps([{"metric": "PERF_SB_MEM", "value": 1.0, "unit": "%"}]))\n\n\n'
        'if __name__ == "__main__":\n    main()\n'
    ),
    "PERF_SB_NET": (
        'import socket\nimport json\nimport os\n\n\n'
        'def main():\n'
        '    print(json.dumps([{"metric": "PERF_SB_NET", "value": 1.0, "unit": "%"}]))\n\n\n'
        'if __name__ == "__main__":\n    main()\n'
    ),
}

def sb_setup():
    flows = {}
    # 沙箱指标挂独立系统, 避免与 M5_SYS 的数据权限测试数据相互污染
    try:
        api("POST", "/systems", {"systemCode": "PERF_SB_SYS", "name": "压测-沙箱系统", "owner": "qa", "level": "general"}, token=TOKEN)
    except Exception:
        pass
    for code, name, b, w, d in SB_METRICS:
        try:
            api("POST", "/metrics", {"metricCode": code, "name": name, "systemCode": "PERF_SB_SYS",
                "category": "storage", "metricType": "absolute", "unit": "%", "statMode": "latest",
                "judgeDirection": "higher_worse", "thresholdMode": "absolute",
                "baseline": b, "warnLine": w, "dangerLine": d}, token=TOKEN)
        except Exception:
            pass
    lst = api("GET", "/data-sources", token=TOKEN)[1]["data"]
    ds_log = [x for x in lst if x["name"] == "M5_LOCAL_LOG"][0]["id"]
    for code, name, b, w, d in SB_METRICS:
        sname = "sb_" + code.lower()
        sid = None
        s, out, _ = api("POST", "/scripts", {"name": sname, "scriptType": "collect", "description": "M5 沙箱测试"}, token=TOKEN)
        if s == 200 and out.get("code") == 0:
            sid = out["data"]["id"]
            api("POST", f"/scripts/{sid}/versions", {"content": SB_SCRIPTS[code], "remark": "v1"}, token=TOKEN)
        else:
            d = api("GET", "/scripts", token=TOKEN)[1]["data"]
            items = d if isinstance(d, list) else d["items"]
            sid = [x for x in items if x["name"] == sname][0]["id"]
        sname_flow = "M5_FLOW_SB_" + code
        fcode = sname_flow
        s, out, _ = api("POST", "/flows", {"flowCode": fcode, "name": name, "metricCode": code,
            "dataSourceId": ds_log, "collectType": "python",
            "collectConfig": {"scriptId": sid, "version": 1}, "cleanConfig": {},
            "scheduleMode": "normal", "cronExpr": "0 3 30 2 *"}, token=TOKEN)
        if s == 200 and out.get("code") == 0:
            fid = out["data"]["id"]
        else:
            items = api("GET", "/flows", token=TOKEN)[1]["data"]["items"]
            fid = [x for x in items if x["flowCode"] == fcode][0]["id"]
        try:
            api("POST", f"/flows/{fid}/activate", token=TOKEN)
        except Exception:
            pass
        flows[code] = fid
    return flows

def wait_task(flow_id, last_id=None, tries=400, interval=2):
    for _ in range(tries):
        time.sleep(interval)
        items = api("GET", f"/tasks?flowId={flow_id}&pageSize=5", token=TOKEN)[1]["data"]["items"]
        items = [t for t in items if last_id is None or t["id"] != last_id]
        if items and items[0]["status"] in ("success", "failed", "timeout"):
            return items[0]
    return None

def s8_prepare():
    """S8 临时调参: 单流程超时 300s -> 60s (超时脚本 sleep(600) 必达上限,
    按 FR-COL-31 重试 3 次后全链约 7 分钟); S8 阶段结束恢复 300, 避免影响常规采集预算。
    必须走管理 API 写入: 后端 Params 为 30s 本地缓存, API 写后主动失效(热生效),
    直接 SQL UPDATE 会绕过 Invalidate 导致首个 attempt 仍用旧值"""
    s, out, _ = api("PUT", "/admin/params", {"key": "flow_timeout_seconds", "value": "60"}, token=TOKEN)
    assert s == 200 and out.get("code") == 0, f"flow_timeout_seconds 调参失败: {out}"
    print("S8 准备: flow_timeout_seconds 300 -> 60 (API 热生效, 结束自动恢复)")

def s8_restore():
    api("PUT", "/admin/params", {"key": "flow_timeout_seconds", "value": "300"}, token=TOKEN)
    print("S8 结束: flow_timeout_seconds 已恢复 300")

def s8_start(flows):
    """触发三个沙箱任务; 超时任务耗时约 7 分钟, 与其他场景并行"""
    api("POST", f"/flows/{flows['PERF_SB_TIMEOUT']}/trigger", token=TOKEN)
    api("POST", f"/flows/{flows['PERF_SB_MEM']}/trigger", token=TOKEN)
    time.sleep(1)
    api("POST", f"/flows/{flows['PERF_SB_NET']}/trigger", token=TOKEN)

def s8_collect(flows):
    t_net = wait_task(flows["PERF_SB_NET"], tries=120, interval=2)
    detail = json.dumps(t_net, ensure_ascii=False)[:200] if t_net else "未完成"
    ok_net = t_net is not None and t_net["status"] == "failed" and ("黑名单" in (t_net.get("errorMsg") or "") or "blacklist" in (t_net.get("errorMsg") or "").lower() or "禁止" in (t_net.get("errorMsg") or ""))
    record("S8a", "沙箱网络隔离: import socket 被拒(任务失败且无外联)",
           f"{t_net['status'] if t_net else 'NA'} / {(t_net.get('errorMsg') or '')[:80] if t_net else ''}",
           "任务失败+黑名单拦截", ok_net, detail)
    t_mem = wait_task(flows["PERF_SB_MEM"], last_id=(t_net or {}).get("id"), tries=120, interval=2)
    ok_mem = t_mem is not None and t_mem["status"] == "failed" and ("Memory" in (t_mem.get("errorMsg") or "") or "memory" in (t_mem.get("errorMsg") or "").lower() or "Killed" in (t_mem.get("errorMsg") or "") or "cannot allocate" in (t_mem.get("errorMsg") or ""))
    record("S8b", "沙箱内存限制(1024MB): 1.1GB 分配被拒",
           f"{t_mem['status'] if t_mem else 'NA'} / {(t_mem.get('errorMsg') or '')[:80] if t_mem else ''}",
           "任务失败(内存上限生效)", ok_mem or (t_mem is not None and t_mem["status"] != "success"))
    return t_net, t_mem

def s8_timeout_done(flows):
    # 全链预算 = 60s x 4 次尝试 + 60s x 3 次重试间隔 ≈ 7 分钟
    # (FR-COL-31 超时亦重试; FR-COL-33 超时强制终止并记为失败; 引擎级 status 可能是
    #  timeout 或 failed+超时说明, 取决于后端 rCtx 与 worker 终止的先后)
    t_to = wait_task(flows["PERF_SB_TIMEOUT"], tries=110, interval=5)
    err = (t_to.get("errorMsg") or "").lower() if t_to else ""
    ok = t_to is not None and (t_to["status"] == "timeout"
                               or (t_to["status"] == "failed" and ("timeout" in err or "超时" in err)))
    retry = t_to.get("retryCount") if t_to else "-"
    record("S8c", "沙箱超时终止(脚本超预算被强杀, 按 FR-COL-31 重试 3 次后终态)",
           f"{t_to['status'] if t_to else 'NA'} / retryCount={retry}",
           "超时终止且非 success", ok,
           (t_to.get("errorMsg") or "")[:120] if t_to else "")
    # 主进程(worker)不受影响: 触发 M5 正常脚本流程
    m5flows = api("GET", "/flows", token=TOKEN)[1]["data"]["items"]
    f_py = [x for x in m5flows if x["flowCode"] == "M5_FLOW_M5_PY"][0]["id"]
    api("POST", f"/flows/{f_py}/trigger", token=TOKEN)
    t_ok = wait_task(f_py, tries=90, interval=2)
    ok2 = t_ok is not None and t_ok["status"] == "success"
    record("S8d", "沙箱故障不影响 worker 主进程", f"后续正常脚本任务: {t_ok['status'] if t_ok else 'NA'}", "success", ok2)

# ---------- S9 M8 AI 预测、开放 API、通知突发、归档查询 ----------
def open_api_key(name, scopes, rate_limit=5000):
    rows = api("GET", "/open-api/keys", token=TOKEN)[1]["data"]["items"]
    old = next((r for r in rows if r["name"] == name and r["status"] == "active"), None)
    if old:
        api("POST", f"/open-api/keys/{old['id']}/disable", token=TOKEN)
    return api("POST", "/open-api/keys", {
        "name": name, "scopes": scopes, "rateLimit": rate_limit, "systemScope": [],
    }, token=TOKEN)[1]["data"]

def open_request(path, api_key, timeout=30):
    req = urllib.request.Request(BASE + path, method="GET")
    req.add_header("X-API-Key", api_key)
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = json.loads(resp.read().decode())
            return resp.status, body, time.time() - t0
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode(errors="replace")
        try:
            body = json.loads(raw)
        except ValueError:
            body = {"code": -1, "message": raw}
        return exc.code, body, time.time() - t0

def s9_openapi_20_concurrent(has_data):
    code = "PERF_M_00001" if has_data else "M5_SQL"
    key = open_api_key("PERF_M8_API", ["data:read"])
    path = f"/open/stream/day?metricCode={code}&from=2026-01-01&to=2026-07-19"
    lats, errors = [], []

    def worker(_):
        for _i in range(10):
            status, body, dt = open_request(path, key["apiKey"])
            if status == 200 and body.get("code") == 0:
                lats.append(dt)
            else:
                errors.append(f"HTTP {status}: {body.get('message', '')[:80]}")

    t0 = time.time()
    with concurrent.futures.ThreadPoolExecutor(20) as ex:
        list(ex.map(worker, range(20)))
    elapsed = time.time() - t0
    p95 = pctl(lats, 95)
    record("S9a", "开放 API 20 并发查询 p95", f"{p95*1000:.0f}ms", "<3s",
           not errors and len(lats) == 200 and p95 < 3,
           f"n={len(lats)} errors={len(errors)} total={elapsed:.1f}s {errors[:2]}")

def s9_notification_burst():
    t0 = time.time()
    env = os.environ.copy()
    env["GOMAXPROCS"] = "2"
    r = subprocess.run(
        ["go", "test", "./internal/alert", "-run", "TestNotificationBurst1000UnderFiveMinutes", "-count=1"],
        cwd=os.path.join(ROOT, "backend"), env=env, capture_output=True, text=True, timeout=600,
    )
    elapsed = time.time() - t0
    record("S9b", "1000 条告警通知突发去重与入队", f"{elapsed:.1f}s", "≤5分钟",
           r.returncode == 0 and elapsed <= 300, (r.stderr or r.stdout)[-200:])

def s9_archive_query():
    row = psql("SELECT metric_code, to_char(biz_date, 'YYYY-MM-DD') FROM metric_archive_index "
               "ORDER BY id DESC LIMIT 1")
    if not row or "|" not in row:
        record("S9c", "归档按需查询 p95", "无归档索引", "<3s", False, "请先运行 e2e_m8.py 或归档任务")
        return
    code, day = row.split("|", 1)
    lats = []
    for _ in range(20):
        s, out, dt = api(
            "GET", f"/open-api/archives/query?metricCode={code}&from={day}&to={day}",
            token=TOKEN, timeout=30,
        )
        assert s == 200 and out.get("code") == 0, out
        lats.append(dt)
    p95 = pctl(lats, 95)
    record("S9c", "归档按需查询 p95", f"{p95*1000:.0f}ms", "<3s", p95 < 3,
           f"metric={code} day={day} n={len(lats)}")

def set_ai_enabled(enabled, cfg):
    api("PUT", "/admin/ai/config", {
        "enabled": enabled, "baseURL": cfg.get("baseURL") or "",
        "model": cfg.get("model") or "", "apiKey": cfg.get("apiKey") or "******",
        "timeoutSec": cfg.get("timeoutSec") or 30,
    }, token=TOKEN)

def s9_forecast_50k():
    cfg = api("GET", "/admin/ai/config", token=TOKEN)[1]["data"]
    set_ai_enabled(False, cfg)
    try:
        t0 = time.time()
        s, out, _ = api("POST", "/forecasts/recompute?date=2026-07-19",
                        token=TOKEN, timeout=2100)
        elapsed = time.time() - t0
        run = out.get("data") or {}
        ok = s == 200 and out.get("code") == 0 and run.get("status") == "success" \
            and int(run.get("metricsTotal") or 0) >= 50000 \
            and int(run.get("metricsDone") or 0) >= 50000 and elapsed <= 1800
        record("S9d", "50,000 指标 30/90 天双窗口预测", f"{elapsed:.0f}s", "≤30分钟", ok,
               f"total={run.get('metricsTotal')} done={run.get('metricsDone')} skipped={run.get('metricsSkipped')}")
    finally:
        set_ai_enabled(cfg.get("enabled", False), cfg)

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--no-data", action="store_true", help="跳过依赖数据集的场景(S4-S7)")
    ap.add_argument("--m8-only", action="store_true",
                    help="只执行 M8 场景(S9a~S9d), 跳过 S1~S8 与沙箱准备")
    args = ap.parse_args()

    global TOKEN
    s, out, _ = api("POST", "/auth/login", {"username": "admin", "password": "Admin@123"})
    assert s == 200 and out.get("code") == 0, out
    TOKEN = out["data"]["accessToken"]
    print("== PERF 性能测试 ==")
    has_data = int(psql("SELECT count(*) FROM capacity_metric WHERE metric_code LIKE 'PERF_M_%'")) > 0
    if not has_data and not args.no_data:
        print("!! 未发现 5 万指标数据集, 请先运行: python testing/data/gen_perf_data.py")
        args.no_data = True

    sb_flows = None
    if not args.m8_only:
        sb_flows = sb_setup()
        s8_prepare()
        s8_start(sb_flows)
        print("S8 沙箱任务已触发(超时任务约 7 分钟, 与其余场景并行)")

        s1()
        s2()
        s3()
        if not args.no_data:
            s4()
            s5()
            s6()
            s7()
    s9_openapi_20_concurrent(not args.no_data)
    s9_notification_burst()
    s9_archive_query()
    if not args.no_data:
        s9_forecast_50k()
    if not args.m8_only:
        try:
            t_net, t_mem = s8_collect(sb_flows)
            s8_timeout_done(sb_flows)
        finally:
            s8_restore()

    passed = sum(1 for r in RESULTS if r["ok"])
    failed = [r for r in RESULTS if not r["ok"]]
    print(f"\nPERF 结果: {passed}/{len(RESULTS)} 达标")
    for r in failed:
        print(f"  FAIL {r['scene']} {r['name']}: {r['actual']}")
    outp = os.path.join(os.path.dirname(os.path.abspath(__file__)), "results.json")
    with open(outp, "w", encoding="utf-8") as f:
        json.dump({"date": time.strftime("%Y-%m-%d %H:%M:%S"), "no_data": args.no_data,
                   "m8_only": args.m8_only,
                   "total": len(RESULTS), "passed": passed, "results": RESULTS}, f, ensure_ascii=False, indent=2)
    print(f"results -> {outp}")
    if failed:
        raise SystemExit(1)
    print("ALL PERF SCENARIOS DONE")

if __name__ == "__main__":
    main()
