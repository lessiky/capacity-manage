#!/usr/bin/env python3
"""Spike 03: Python 沙箱 PoC 验证器
复用 backend/worker/sandbox.py（模块白名单 + 黑名单 + 受限 os + 工作目录写限制）。
每个用例以独立子进程运行 (python -I -E sandbox.py <script>)，带超时强杀。
用法: python run_spike.py  （输出 JSON + 摘要表, 写入 output.txt）
"""
import json
import os
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(ROOT, "..", ".."))
SANDBOX = os.path.join(REPO, "backend", "worker", "sandbox.py")
CASES_DIR = os.path.join(ROOT, "cases")


def run_case(name, script, timeout_ms=8000, env_extra=None):
    case_dir = os.path.join(CASES_DIR, name)
    os.makedirs(case_dir, exist_ok=True)
    script_path = os.path.join(case_dir, "case.py")
    with open(script_path, "w", encoding="utf-8") as f:
        f.write(script)
    env = dict(os.environ)
    env["CAP_PARAMS"] = json.dumps({"x": 1})
    env["CAP_INPUT"] = json.dumps({})
    if env_extra:
        env.update(env_extra)
    t0 = time.time()
    proc = subprocess.Popen(
        [sys.executable, "-I", "-E", SANDBOX, script_path],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=case_dir, env=env,
    )
    status, exit_code = "success", 0
    try:
        out, err = proc.communicate(timeout=timeout_ms / 1000.0)
        exit_code = proc.returncode
        if exit_code != 0:
            status = "failed"
    except subprocess.TimeoutExpired:
        proc.kill()
        out, err = proc.communicate()
        exit_code = -9
        status = "timeout-killed"
    return {
        "case": name,
        "status": status,
        "exitCode": exit_code,
        "durationMs": int((time.time() - t0) * 1000),
        "stdout": (out or b"")[-400:].decode("utf-8", "replace").strip(),
        "stderr": (err or b"")[-400:].decode("utf-8", "replace").strip(),
    }


CASES = [
    ("ok_compute", 'import json, math\nprint(json.dumps({"ok": True, "sqrt": math.sqrt(2)}))\n'),
    ("ok_write_workdir", 'import os, json\nwith open("result.txt", "w", encoding="utf-8") as f:\n    f.write("hello")\nprint(json.dumps({"ok": True, "file": os.path.exists("result.txt")}))\n'),
    ("block_write_outside", 'import json\nwith open("/tmp/escape_test.txt", "w") as f:\n    f.write("x")\nprint("SHOULD NOT REACH")\n'),
    ("block_import_socket", 'import socket\nprint("SHOULD NOT REACH")\n'),
    ("block_import_subprocess", 'import subprocess\nprint("SHOULD NOT REACH")\n'),
    ("block_import_requests", 'import requests\nprint("SHOULD NOT REACH")\n'),
    ("timeout_infinite_loop", 'i = 0\nwhile True:\n    i += 1\n'),
    ("timeout_sleep", 'import time\ntime.sleep(60)\nprint("SHOULD NOT REACH")\n'),
    ("cpu_hot_loop", 'x = 0\nwhile True:\n    x += 1\n'),
]


def expected(r):
    name = r["case"]
    if name.startswith("ok_"):
        return r["status"] == "success" and '"ok":true' in r["stdout"].replace(" ", "")
    if name in ("block_write_outside", "block_import_socket", "block_import_subprocess", "block_import_requests"):
        return r["status"] == "failed" and "SHOULD NOT REACH" not in r["stdout"]
    if name.startswith("timeout_") or name == "cpu_hot_loop":
        return r["status"] == "timeout-killed"
    return False


def main():
    results = []
    for name, script in CASES:
        timeout = 8000
        r = run_case(name, script, timeout_ms=timeout)
        r["pass"] = expected(r)
        results.append(r)
        mark = "PASS" if r["pass"] else "FAIL"
        print(f"[{mark}] {name:26s} status={r['status']:14s} exit={r['exitCode']:3d} {r['durationMs']}ms")
        if r["stderr"]:
            print("       stderr:", r["stderr"].splitlines()[-1][:120])
    ok = sum(1 for r in results if r["pass"])
    print(f"\n===== {ok}/{len(results)} cases passed =====")
    out = os.path.join(ROOT, "output.txt")
    with open(out, "w", encoding="utf-8") as f:
        f.write(f"Python {sys.version.split()[0]} @ {time.strftime('%Y-%m-%d %H:%M:%S')}\n")
        f.write(f"sandbox: {SANDBOX}\n\n")
        for r in results:
            f.write(f"--- {r['case']} [{'PASS' if r['pass'] else 'FAIL'}] {r['status']} exit={r['exitCode']} {r['durationMs']}ms\n")
            if r["stdout"]:
                f.write(f"stdout: {r['stdout']}\n")
            if r["stderr"]:
                f.write(f"stderr: {r['stderr']}\n")
        f.write(f"\n===== {ok}/{len(results)} cases passed =====\n")
    print("output ->", out)


if __name__ == "__main__":
    main()