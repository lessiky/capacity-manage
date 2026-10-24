# -*- coding: utf-8 -*-
"""数据源 CSV 批量导入 API 验证

覆盖:
- 上传 CSV 批量登记数据源: 成功/失败统计 + 逐行失败原因(分类不存在)
- 分类按「所属系统 + 分类名称」定位, 类型专属列落库, 凭据加密存储且列表脱敏
- 同名数据源 upsert: 配置覆盖、密码留空保留原密文、类型变更被拒
- 缺表头 / 空请求体 / 未认证的边界
运行: python testing/ds_import_check.py   (需后端 :8080 已启动)
"""
import json, sys, time, urllib.error, urllib.request

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

BASE = "http://127.0.0.1:8080/api/v1"
TOKEN = None
SYS = "DSI_%04d" % (int(time.time()) % 10000)
HEADER = ("name,type,systemCode,categoryName,driver,host,port,database,service,"
          "username,password,scheme,pathTemplate,globTemplate,os,tempDir")


def request(method, path, body=None, expect=None, raw=None, content_type=None, token=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", content_type or "application/json")
    tok = token if token is not None else TOKEN
    if tok:
        req.add_header("Authorization", "Bearer " + tok)
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            out = json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        out = json.loads(e.read().decode())
        if expect and expect in str(out.get("message", "")):
            return out
        raise AssertionError("HTTP %d %s" % (e.code, out))
    if expect and out.get("code") != 0:
        assert expect in str(out.get("message", "")), "expect %r in %s" % (expect, out)
    if out.get("code") != 0:
        raise AssertionError("api error %s" % out)
    return out.get("data")


def upload_csv(path, content, filename="data-sources.csv", token=None, expect=None):
    boundary = "----capdsimport%d" % (time.time_ns())
    parts = [
        ("--%s\r\n" % boundary).encode(),
        ('Content-Disposition: form-data; name="file"; filename="%s"\r\n' % filename).encode(),
        b"Content-Type: text/csv\r\n\r\n",
        content.encode("utf-8"),
        ("\r\n--%s--\r\n" % boundary).encode(),
    ]
    return request("POST", path, raw=b"".join(parts), expect=expect,
                   content_type="multipart/form-data; boundary=%s" % boundary, token=token)


def line(*fields):
    return ",".join('"%s"' % f.replace('"', '""') if any(c in f for c in ',"\n') else f
                    for f in fields)


# 1) 登录
d = request("POST", "/auth/login", {"username": "admin", "password": "Admin@123"})
TOKEN = d["accessToken"] if "accessToken" in d else d.get("token")
print("1) login ok")

# 2) 系统与分类
try:
    request("POST", "/systems", {"systemCode": SYS, "name": SYS + "系统", "owner": "e2e", "level": "general"})
except AssertionError:
    pass
CAT_DB = request("POST", "/data-source-categories", {"systemCode": SYS, "name": "核心库", "sort": 1})
CAT_LOG = request("POST", "/data-source-categories", {"systemCode": SYS, "name": "日志目录", "sort": 2})
print("2) system/categories ok: %s" % SYS)

# 3) 批量导入: 2 行成功 + 1 行分类不存在
csv_doc = "\n".join([
    HEADER,
    line(SYS + "_DB", "db", SYS, "核心库", "oracle", "10.0.0.1", "1521", "", "ORCL",
         "cap_read", "p@ss,word", "", "", "", "", ""),
    line(SYS + "_LOG", "log", SYS, "日志目录", "", "", "", "", "", "", "",
         "local", "/data/cap", "cap_*.log", "", ""),
    line(SYS + "_BAD", "db", SYS, "不存在的分类", "oracle", "10.0.0.3", "1521", "", "ORCL",
         "cap_read", "x", "", "", "", "", ""),
])
imp = upload_csv("/data-sources/import", csv_doc)
assert imp["total"] == 3 and imp["success"] == 2 and imp["failed"] == 1 and imp["status"] == "partial", imp
assert imp["errors"][0]["row"] == 3 and "分类" in imp["errors"][0]["reason"], imp["errors"]
print("3) CSV 导入 ok: total=%d success=%d failed=%d 首错=行%d %s" %
      (imp["total"], imp["success"], imp["failed"], imp["errors"][0]["row"], imp["errors"][0]["reason"]))

# 4) 列表校验: 分类名回填, 凭据脱敏
rows = {r["name"]: r for r in request("GET", "/data-sources?systemCode=" + SYS)}
assert set(rows) == {SYS + "_DB", SYS + "_LOG"}, rows.keys()
db_row, log_row = rows[SYS + "_DB"], rows[SYS + "_LOG"]
assert db_row["categoryName"] == "核心库" and db_row["type"] == "db", db_row
assert log_row["categoryName"] == "日志目录" and log_row["type"] == "log", log_row
assert "******" in db_row["config"] and "p@ss,word" not in db_row["config"], db_row["config"]
assert json.loads(db_row["config"])["service"] == "ORCL", db_row["config"]
print("4) 列表回填/脱敏 ok: %s / %s" % (db_row["categoryName"], log_row["categoryName"]))

# 5) upsert: 同名更新配置, 密码留空保留原密文; 类型变更被拒
upd = "\n".join([
    HEADER,
    line(SYS + "_DB", "db", SYS, "核心库", "oracle", "10.0.0.2", "1521", "", "NEW", "cap_read",
         "", "", "", "", "", ""),
    line(SYS + "_DB", "log", SYS, "日志目录", "", "", "", "", "", "", "", "local", "/x", "y", "", ""),
])
imp2 = upload_csv("/data-sources/import", upd)
assert imp2["success"] == 1 and imp2["failed"] == 1, imp2
assert "类型" in imp2["errors"][0]["reason"], imp2["errors"]
rows2 = {r["name"]: r for r in request("GET", "/data-sources?systemCode=" + SYS)}
assert len(rows2) == 2, rows2.keys()
updated = json.loads(rows2[SYS + "_DB"]["config"])
assert updated["host"] == "10.0.0.2" and updated["service"] == "NEW", updated
assert updated["password"] == "******", updated
print("5) upsert ok: host=%s service=%s 类型变更被拒" % (updated["host"], updated["service"]))

# 6) 边界: 缺表头 / 空体 / 未认证
bad_header = upload_csv("/data-sources/import", "name,type\nfoo,db\n", expect="表头")
assert bad_header["code"] != 0 and "表头" in bad_header["message"], bad_header
empty = upload_csv("/data-sources/import", "", expect="至少需要表头")
assert empty["code"] != 0, empty
anon = upload_csv("/data-sources/import", csv_doc, token="", expect="未认证")
assert anon["code"] == 40100, anon
print("6) 边界 ok: 缺表头/空内容 400, 未认证 401")

print()
print("ALL CHECKS PASSED (SYS=%s)" % SYS)
