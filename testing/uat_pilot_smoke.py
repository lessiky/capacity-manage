# -*- coding: utf-8 -*-
"""M5 试点 UAT 冒烟验证 (WBS 5-4): 前置 python testing/data/pilot_seed.py

验证试点闭环: 6 流程采集成功 -> 告警生成(预期 6 个 warn) -> 日汇总落地 -> 日报生成+导出
输出: 控制台 + testing/uat_smoke_results.json
幂等可重复执行(同一业务日重跑覆盖同批记录)。
"""
import json, os, subprocess, sys, time, urllib.request, urllib.error

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

BASE = "http://127.0.0.1:8080/api/v1"
CTR_PG = "capacity-manage-postgres-1"
TODAY = time.strftime("%Y-%m-%d", time.gmtime())
TOKEN = None
RESULTS = []

def api(method, path, body=None, timeout=120):
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", "Bearer " + token_safe())
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode())

def token_safe():
    return TOKEN

def psql(sql):
    r = subprocess.run(["docker", "exec", CTR_PG, "psql", "-U", "capacity", "-d", "capacity", "-t", "-A", "-c", sql],
                       capture_output=True, text=True, timeout=600)
    if r.returncode != 0:
        raise RuntimeError(r.stderr[:300])
    return r.stdout.strip()

def check(name, ok, actual, expect):
    RESULTS.append({"name": name, "ok": bool(ok), "actual": str(actual)[:300], "expect": str(expect)[:200]})
    print(f"  [{'PASS' if ok else 'FAIL'}] {name}: {actual}")
    return bool(ok)

PILOT_METRICS = ["TRADE_DB_USAGE", "TRADE_MSG_QUEUE", "RISK_CPU", "RISK_MEM_GB", "RPT_LOG_SIZE", "RPT_JOB_DUR"]

def main():
    global TOKEN
    s, out = api("POST", "/auth/login", {"username": "admin", "password": "Admin@123"})
    assert s == 200 and out.get("code") == 0, out
    TOKEN = out["data"]["accessToken"]
    print(f"== 试点 UAT 冒烟 ({TODAY}) ==")

    # 1) 6 流程当日采集成功
    s, out = api("GET", "/flows?pageSize=200")
    flows = [f for f in out["data"]["items"] if f["flowCode"].startswith("PILOT_FLOW_")]
    assert len(flows) == 6, f"试点流程数={len(flows)}"
    ok_tasks = 0
    for f in flows:
        s, out = api("GET", f"/tasks?flowId={f['id']}&pageSize=5")
        items = [t for t in out["data"]["items"] if t["triggeredAt"].startswith(TODAY)]
        if items and items[0]["status"] == "success":
            ok_tasks += 1
        else:
            check(f"采集 {f['flowCode']}", False, str(items[0] if items else "无当日任务")[:120], "success")
    check("1) 6 试点流程当日采集 success", ok_tasks == 6, f"{ok_tasks}/6", "6/6")

    # 2) 告警: 6 指标均超 warn 线 -> 预期 6 个 active 告警
    s, out = api("GET", "/alerts?pageSize=200")
    active = [a for a in out["data"]["items"] if a.get("status") == "active" and a.get("metricCode") in PILOT_METRICS]
    got = sorted(a["metricCode"] for a in active)
    check("2) 告警生成(6 指标均超预警线)", got == sorted(PILOT_METRICS),
          f"{len(got)} 个 active: {got}", "6 个 active: " + ",".join(sorted(PILOT_METRICS)))

    # 3) 日汇总: 触发当日汇总并等待完成(全量指标, ~5 分钟), 验证试点指标日数据落地
    psql("SELECT 1")
    s, out = api("POST", "/data/summary/run", {"bizDate": TODAY})
    print(f"  日汇总已触发: {out.get('message', out.get('code'))}")
    t0 = time.time()
    row = None
    for _ in range(90):
        time.sleep(10)
        row = psql(f"SELECT status||'|'||coalesce(metrics_done::text,'?')||'|'||coalesce(metrics_missing::text,'?') FROM daily_summary_log WHERE biz_date='{TODAY}'")
        st = row.split("|")[0]
        if st in ("success", "partial", "failed"):
            break
    dt = int(time.time() - t0)
    st, done, miss = (row or "running|?|?").split("|")
    check("3a) 当日日汇总完成(服务端计时)", st in ("success", "partial"), f"status={st} done={done} missing={miss} 耗时={dt}s", "success/partial")
    rows = psql(f"SELECT count(*) FROM metric_value_day WHERE biz_date='{TODAY}' AND metric_code IN "
                f"({','.join(repr(m) for m in PILOT_METRICS)})")
    check("3b) 试点指标日数据落地", rows == "6", f"{rows}/6 行", "6/6")

    # 4) 日报: 生成 + 查询 + 导出
    s, out = api("POST", f"/reports/daily/{TODAY}/generate")
    time.sleep(3)
    s, out = api("GET", f"/reports/daily?date={TODAY}")
    d = out["data"]
    pilot_in_report = [m for m in d.get("metrics", []) if m.get("metricCode") in PILOT_METRICS]
    check("4a) 日报生成含试点指标", len(pilot_in_report) == 6, f"{len(pilot_in_report)}/6 (status={d.get('status')})", "6/6")
    ok_x = ok_p = False
    for fmt in ("xlsx", "pdf"):
        req = urllib.request.Request(BASE + f"/reports/daily/{TODAY}/export?format={fmt}", method="GET")
        req.add_header("Authorization", "Bearer " + TOKEN)
        with urllib.request.urlopen(req, timeout=120) as r:
            head = r.read(8)
        ok_x = ok_x or fmt == "xlsx" and head[:2] == b"PK"
        ok_p = ok_p or fmt == "pdf" and head[:4] == b"%PDF"
    check("4b) 导出 xlsx(PK)/pdf(%PDF) 魔数正确", ok_x and ok_p, f"xlsx={ok_x} pdf={ok_p}", "True/True")

    passed = sum(1 for r in RESULTS if r["ok"])
    print(f"\nUAT 冒烟结果: {passed}/{len(RESULTS)} 通过")
    outp = os.path.join(os.path.dirname(os.path.abspath(__file__)), "uat_smoke_results.json")
    with open(outp, "w", encoding="utf-8") as f:
        json.dump({"date": time.strftime("%Y-%m-%d %H:%M:%S"), "bizDate": TODAY,
                   "passed": passed, "total": len(RESULTS), "results": RESULTS}, f, ensure_ascii=False, indent=2)
    print(f"results -> {outp}")
    if passed != len(RESULTS):
        raise SystemExit(1)
    print("ALL UAT SMOKE CHECKS PASSED")

if __name__ == "__main__":
    main()
