#!/usr/bin/env python3
"""capacity-worker: Python 沙箱 Worker (独立进程池)

契约: docs/10-design/05-接口契约.md §1
- POST /v1/execute : 执行采集/清洗脚本 (子进程 + OS 级限制)
- GET  /v1/health  : 存活与队列状态
隔离: 无网络(依赖部署层+白名单双保险)、只读 FS(仅工作目录可写)、
     超时 SIGKILL 进程组、内存/CPU rlimit、模块白名单
"""
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

WORKER_PORT = int(os.environ.get("WORKER_PORT", "9100"))
POOL_SIZE = int(os.environ.get("WORKER_POOL_SIZE", "4"))
WORK_ROOT = os.environ.get("WORKER_WORK_ROOT", tempfile.gettempdir() + "/capworker")
DEFAULT_TIMEOUT_MS = int(os.environ.get("WORKER_TIMEOUT_MS", "300000"))
MAX_OUTPUT_BYTES = 64 * 1024
SANDBOX = os.path.join(os.path.dirname(os.path.abspath(__file__)), "sandbox.py")

# 脚本注册表: 由平台侧通过环境变量注入? MVP: Worker 从平台 DB 拿不到脚本,
# 由 /v1/execute 请求携带 scriptContent (平台服务端读取 DB 后下发)
POOL = ThreadPoolExecutor(max_workers=POOL_SIZE)


def run_sandbox(req: dict) -> dict:
    task_id = req.get("taskId", "unknown")
    script_content = req.get("scriptContent", "")
    script_type = req.get("scriptType", "collect")
    params = req.get("params", {})
    timeout_ms = req.get("timeoutMs") or DEFAULT_TIMEOUT_MS
    work_dir = os.path.join(WORK_ROOT, task_id)
    os.makedirs(work_dir, exist_ok=True)
    script_path = os.path.join(work_dir, "_run_script.py")
    with open(script_path, "w", encoding="utf-8") as f:
        f.write(script_content)

    env = dict(os.environ)
    env["CAP_PARAMS"] = json.dumps(params, ensure_ascii=False)
    # input 上游(采集/清洗/AI 试跑)均为 JSON 文本, 直接透传, 避免二次编码
    raw_input = req.get("input", "")
    if isinstance(raw_input, str):
        env["CAP_INPUT"] = raw_input
    else:
        env["CAP_INPUT"] = json.dumps(raw_input, ensure_ascii=False)
    env["CAP_TASK_ID"] = task_id
    env["CAP_SCRIPT_TYPE"] = script_type

    t0 = time.time()
    # 子进程: 独立会话(进程组), 超时 SIGKILL 整个进程组
    try:
        proc = subprocess.Popen(
            [sys.executable, "-I", "-E", SANDBOX, script_path],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=work_dir,
            env=env,
            preexec_fn=os.setsid if hasattr(os, "setsid") else None,
        )
    except OSError as e:
        return {"status": "failed", "data": "", "exitCode": -1,
                "durationMs": int((time.time() - t0) * 1000),
                "stdout": "", "stderr": str(e), "errorType": "SpawnError", "errorMessage": str(e)}
    try:
        stdout_b, stderr_b = proc.communicate(timeout=timeout_ms / 1000.0)
        exit_code = proc.returncode
    except subprocess.TimeoutExpired:
        try:
            if hasattr(os, "killpg"):
                os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
            else:
                proc.kill()
        except ProcessLookupError:
            pass
        stdout_b, stderr_b = proc.communicate()
        return {"status": "timeout", "data": "", "exitCode": proc.returncode,
                "durationMs": int((time.time() - t0) * 1000),
                "stdout": (stdout_b or b"")[:MAX_OUTPUT_BYTES].decode("utf-8", "replace"),
                "stderr": (stderr_b or b"")[:MAX_OUTPUT_BYTES].decode("utf-8", "replace"),
                "errorType": "Timeout", "errorMessage": f"超时 {timeout_ms}ms 强制终止"}

    stdout = (stdout_b or b"")[:MAX_OUTPUT_BYTES].decode("utf-8", "replace")
    stderr = (stderr_b or b"")[:MAX_OUTPUT_BYTES].decode("utf-8", "replace")
    duration_ms = int((time.time() - t0) * 1000)
    if exit_code != 0:
        return {"status": "failed", "data": "", "exitCode": exit_code,
                "durationMs": duration_ms, "stdout": stdout, "stderr": stderr,
                "errorType": "ScriptError", "errorMessage": stderr.strip()[-500:]}
    # 约定: stdout 最后一行为 JSON
    data = ""
    for line in reversed(stdout.strip().splitlines()):
        line = line.strip()
        if not line:
            continue
        try:
            parsed = json.loads(line)
            data = json.dumps(parsed, ensure_ascii=False)
            break
        except json.JSONDecodeError:
            continue
    if data == "":
        return {"status": "failed", "data": "", "exitCode": exit_code,
                "durationMs": duration_ms, "stdout": stdout, "stderr": stderr,
                "errorType": "OutputError", "errorMessage": "stdout 无 JSON 输出行"}
    return {"status": "success", "data": data, "exitCode": 0,
            "durationMs": duration_ms, "stdout": stdout, "stderr": stderr}


class Handler(BaseHTTPRequestHandler):
    def _send(self, code: int, obj: dict):
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/v1/health":
            busy = POOL._work_queue.qsize()
            self._send(200, {"ok": True, "pool": {"busy": busy, "free": POOL_SIZE - busy, "queued": 0}})
            return
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if self.path not in ("/v1/execute", "/v1/check"):
            self._send(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        try:
            req = json.loads(self.rfile.read(length))
        except json.JSONDecodeError as e:
            self._send(400, {"error": f"bad request: {e}"})
            return
        if self.path == "/v1/check":
            self._send(200, self.do_check(req))
            return
        if not req.get("scriptContent"):
            self._send(400, {"error": "scriptContent required"})
            return
        try:
            result = POOL.submit(run_sandbox, req).result(timeout=(req.get("timeoutMs") or DEFAULT_TIMEOUT_MS) / 1000.0 + 10)
        except Exception as e:  # noqa: BLE001
            self._send(500, {"status": "failed", "errorType": "WorkerError", "errorMessage": str(e)})
            return
        self._send(200, result)

    def do_check(self, req: dict) -> dict:
        """POST /v1/check — 语法校验 (FR-COL-19): 不执行, 仅编译检查"""
        content = req.get("scriptContent", "")
        if not content:
            return {"ok": False, "error": "scriptContent required"}
        try:
            compile(content, "script.py", "exec")
            return {"ok": True}
        except SyntaxError as e:
            return {"ok": False, "error": f"{e.msg} (line {e.lineno})", "line": e.lineno}
        except Exception as e:  # noqa: BLE001
            return {"ok": False, "error": str(e)}

    def log_message(self, fmt, *args):  # 静默默认日志, 接入结构化日志
        pass


def main():
    # 默认 0.0.0.0: compose 中 worker 独立容器, 需被 backend 跨容器访问; 单机部署可用 WORKER_HOST=127.0.0.1 收紧
    host = os.environ.get("WORKER_HOST", "0.0.0.0")
    server = ThreadingHTTPServer((host, WORKER_PORT), Handler)
    print(f"capacity-worker listening on {host}:{WORKER_PORT} pool={POOL_SIZE}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
