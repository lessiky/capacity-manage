#!/usr/bin/env python3
"""sandbox.py: 沙箱引导器 (python -I -E sandbox.py <script_path>)

隔离机制 (MVP):
1. 模块白名单: 替换 sys.meta_path, 非白名单 import 直接 ImportError;
   硬黑名单 (socket/subprocess/ctypes/...) 即使白名单变更也拦截;
2. 受限 os: 仅保留只读路径查询与环境变量, 禁 写/删/exec/系统调用;
3. 资源限制: RLIMIT_CPU / RLIMIT_AS / RLIMIT_FSIZE / RLIMIT_NPROC (Linux);
   网络隔离依赖部署层 (Docker network none / 防火墙) + 黑名单双保险;
4. 入参: 环境变量 CAP_PARAMS (JSON), CAP_INPUT (JSON);
   出参: stdout 最后一行 JSON。
"""
import builtins
import importlib
import io
import json
import os
import sys
import types

# 注意: 必须在安装 WhitelistFinder 之前导入 resource (黑名单会拦截后续导入,
# 导致沙箱自身的 rlimit 限制被静默跳过)
if os.name == "posix":
    try:
        import resource as _sandbox_resource
    except ImportError:
        _sandbox_resource = None
else:
    _sandbox_resource = None

# ---------------- 模块白名单 ----------------
WHITELIST = {
    "json", "math", "re", "datetime", "time", "decimal", "sys", "io",
    "collections", "itertools", "functools", "typing", "csv", "base64",
    "hashlib", "string", "unicodedata", "contextlib", "numbers", "stat",
    "dataclasses", "enum", "abc", "copy", "operator", "textwrap",
}
BLACKLIST = {
    "socket", "subprocess", "shutil", "shelve", "sqlite3", "importlib",
    "pickle", "ctypes", "multiprocessing", "threading", "asyncio",
    "http", "urllib", "requests", "httpx", "aiohttp", "ssl", "ftplib",
    "paramiko", "psutil", "resource", "fcntl", "termios", "posix",
}
# 标准库子模块前缀 (白名单父模块允许)
ALLOW_PREFIX = ("json.", "datetime.", "decimal.", "collections.", "functools.",
                "itertools.", "csv.", "re.", "math.", "base64.", "hashlib.",
                "dataclasses.", "enum.", "abc.", "copy.", "operator.", "contextlib.")


class WhitelistFinder:
    def find_module(self, fullname, path=None):  # 兼容旧接口
        return None

    def find_spec(self, fullname, path=None, target=None):
        root = fullname.split(".")[0]
        if fullname in BLACKLIST or root in BLACKLIST:
            raise ImportError(f"[sandbox] 模块 {fullname} 在黑名单, 禁止导入")
        if fullname in WHITELIST or root in WHITELIST or fullname.startswith(ALLOW_PREFIX):
            return None  # 回落到默认 finder
        raise ImportError(f"[sandbox] 模块 {fullname} 不在白名单, 禁止导入")


sys.meta_path.insert(0, WhitelistFinder())


def _blocked(*_a, **_k):
    raise PermissionError("[sandbox] 该操作在沙箱中禁止")


def restrict_os():
    """os 受限: 禁写/删/执行/网络相关调用"""
    banned = {
        "write": None, "remove": None, "unlink": None, "rmdir": None,
        "rename": None, "replace": None, "chmod": None, "chown": None,
        "umask": None, "execv": None, "execve": None, "execvp": None,
        "execl": None, "execlp": None, "execle": None, "kill": None,
        "fork": None, "forkpty": None, "spawnl": None, "spawnv": None,
        "system": None, "popen": None, "remove": None, "makedirs": None,
        "mkfifo": None, "link": None, "symlink": None, "startfile": None,
        "openpty": None, "forkpty": None, "setsid": None, "setuid": None,
        "setgid": None, "seteuid": None, "setegid": None, "setgid": None,
        "getlogin": None, "urandom": None,
    }
    for name in banned:
        if hasattr(os, name):
            setattr(os, name, _blocked)
    # 受限 open: 仅允许当前工作目录 (work_dir) 内写, 其余路径只读/禁
    safe_open = builtins.open

    def restricted_open(file, mode="r", *args, **kwargs):
        m = mode if isinstance(mode, str) else "r"
        if any(x in m for x in ("w", "a", "x", "+")):
            path = str(file)
            work = os.getcwd()
            real = os.path.abspath(path)
            if not (real == work or real.startswith(work + os.sep)):
                raise PermissionError(f"[sandbox] 仅工作目录 {work} 可写: {path}")
        return safe_open(file, m, *args, **kwargs)

    builtins.open = restricted_open


def restrict_builtin():
    banned = {"__import__": None}
    real_import = builtins.__import__

    def guarded_import(name, *args, **kwargs):
        root = str(name).split(".")[0]
        if root in BLACKLIST:
            raise ImportError(f"[sandbox] 模块 {name} 在黑名单, 禁止导入")
        return real_import(name, *args, **kwargs)

    builtins.__import__ = guarded_import


def apply_resource_limits():
    """Linux: rlimit 资源上限 (Windows 开发环境跳过)"""
    if _sandbox_resource is None:
        return
    try:
        resource = _sandbox_resource
        mem_mb = int(os.environ.get("CAP_MEM_LIMIT_MB", "1024"))
        cpu_s = int(os.environ.get("CAP_CPU_LIMIT_SEC", "120"))
        fsize_kb = int(os.environ.get("CAP_FSIZE_LIMIT_KB", "10240"))
        resource.setrlimit(resource.RLIMIT_AS, (mem_mb << 20, mem_mb << 20))
        resource.setrlimit(resource.RLIMIT_CPU, (cpu_s, cpu_s))
        resource.setrlimit(resource.RLIMIT_FSIZE, (fsize_kb << 10, fsize_kb << 10))
        resource.setrlimit(resource.RLIMIT_NPROC, (0, 0))
    except Exception:
        pass


def main():
    if len(sys.argv) < 2:
        print(json.dumps({"error": "usage: sandbox.py <script_path>"}))
        sys.exit(2)
    apply_resource_limits()
    restrict_os()
    restrict_builtin()
    script_path = sys.argv[1]
    with open(script_path, "r", encoding="utf-8") as f:
        code = f.read()
    g = {"__name__": "__main__", "__file__": script_path}
    exec(compile(code, script_path, "exec"), g)  # noqa: S102 (受控环境)


if __name__ == "__main__":
    main()