# -*- coding: utf-8 -*-
"""数据源分类管理 API 验证 (WBS 数据源分类)

覆盖:
- 分类 CRUD: 新建(必须选系统) / 列表(系统筛选) / 编辑 / 删除(被引用时拒绝)
- 数据源: 创建/编辑必须携带 systemCode + categoryId, 且分类须归属该系统
运行: python testing/category_api_check.py   (需后端已在 :8080 启动)
"""
import json, time, urllib.request, urllib.error

BASE = "http://127.0.0.1:8080/api/v1"
TOKEN = None
SYS = "E2E_CAT_%04d" % (int(time.time()) % 10000)

def api(method, path, body=None, expect=None):
    global TOKEN
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", "Bearer " + TOKEN)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            out = json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        out = json.loads(e.read().decode())
        if expect and expect in str(out.get("message", "")):
            return out
        raise
    if expect and out.get("code") != 0:
        assert expect in str(out.get("message", "")), f"expect {expect!r} in {out}"
    if out.get("code") != 0:
        raise AssertionError(f"api error {out}")
    return out.get("data")

d = api("POST", "/auth/login", {"username": "admin", "password": "Admin@123"})
TOKEN = d["accessToken"] if "accessToken" in d else d.get("token")
print("1) login ok")

# 2) 系统
try:
    api("POST", "/systems", {"systemCode": SYS, "name": SYS + "系统", "owner": "e2e", "level": "general"})
    print("2) system created:", SYS)
except AssertionError:
    print("2) system exists")

# 3) 分类 CRUD
def cat_get(name):
    lst = api("GET", f"/data-source-categories?systemCode={SYS}")
    hit = [x for x in lst if x["name"] == name]
    return hit[0] if hit else None

c1 = cat_get("核心库") or api("POST", "/data-source-categories", {"systemCode": SYS, "name": "核心库", "sort": 1, "remark": "验证用"})
c2 = cat_get("日志归档") or api("POST", "/data-source-categories", {"systemCode": SYS, "name": "日志归档", "sort": 2})
print("3) categories:", c1["id"], c2["id"])

api("POST", "/data-source-categories", {"systemCode": SYS, "name": "核心库"}, expect="已存在同名分类")
api("POST", "/data-source-categories", {"systemCode": "NO_SUCH_SYS", "name": "x"}, expect="业务系统不存在")
lst = api("GET", "/data-source-categories?systemCode=" + SYS)
assert all(x["systemCode"] == SYS for x in lst)
api("PUT", f"/data-source-categories/{c1['id']}", {"name": "核心库", "remark": "改备注", "sort": 10})
c1 = cat_get("核心库")
assert c1["sort"] == 10 and c1["remark"] == "改备注"
print("3) category CRUD + 同名/无系统 校验 ok")

# 4) 数据源必须携带 系统+分类
api("POST", "/data-sources", {"name": "CAT_NOATTR", "type": "log",
    "config": {"scheme": "local", "pathTemplate": "/tmp/x.log"}}, expect="参数错误")
api("POST", "/data-sources", {"name": "CAT_BADSYS", "type": "log", "systemCode": "NO_SUCH_SYS",
    "categoryId": c1["id"], "config": {"scheme": "local", "pathTemplate": "/tmp/x.log"}}, expect="业务系统不存在")
print("4) 缺系统/分类 校验 ok")

# 4.1) 分类须属于所选系统: 建第二个系统, 用其系统码 + 第一系统的分类
SYS2 = SYS + "B"
try:
    api("POST", "/systems", {"systemCode": SYS2, "name": SYS2 + "系统", "owner": "e2e", "level": "general"})
except AssertionError:
    pass
api("POST", "/data-sources", {"name": "CAT_CROSS", "type": "log", "systemCode": SYS2,
    "categoryId": c1["id"], "config": {"scheme": "local", "pathTemplate": "/tmp/x.log"}}, expect="所选分类不属于该业务系统")
print("4.1) 跨系统分类 校验 ok")

# 5) 正常创建 + 列表筛选
DS = api("POST", "/data-sources", {"name": "CAT_DS_" + SYS, "type": "log",
    "systemCode": SYS, "categoryId": c2["id"],
    "config": {"scheme": "local", "pathTemplate": f"/tmp/cat_{SYS}.log", "encoding": "utf-8"}})
assert DS["systemCode"] == SYS and DS["categoryId"] == c2["id"]
assert DS["systemName"] and DS["categoryName"] == "日志归档"
lst = api("GET", "/data-sources?systemCode=" + SYS)
assert any(x["id"] == DS["id"] for x in lst)
lst = api("GET", f"/data-sources?categoryId={c2['id']}")
assert any(x["id"] == DS["id"] for x in lst)
lst = api("GET", f"/data-sources?systemCode={SYS}&categoryId={c1['id']}")
assert not any(x["id"] == DS["id"] for x in lst)
print("5) datasource create + 系统/分类 筛选 ok")

# 6) 编辑: 换分类 ok; 缺字段拒绝
api("PUT", f"/data-sources/{DS['id']}", {"name": "CAT_DS_" + SYS, "type": "log",
    "systemCode": SYS, "categoryId": c1["id"],
    "config": {"scheme": "local", "pathTemplate": f"/tmp/cat_{SYS}.log", "encoding": "utf-8"}})
det = api("GET", f"/data-sources/{DS['id']}")
assert det["categoryId"] == c1["id"] and det["categoryName"] == "核心库"
api("PUT", f"/data-sources/{DS['id']}", {"name": "CAT_DS_" + SYS}, expect="参数错误")
print("6) datasource update + 必填校验 ok")

# 7) 删除: 分类被引用拒绝 -> 停用语源后删语源 -> 删分类
api("DELETE", f"/data-source-categories/{c1['id']}", expect="仍有 1 个数据源")
api("POST", f"/data-sources/{DS['id']}/disable")
api("DELETE", f"/data-sources/{DS['id']}")
api("DELETE", f"/data-source-categories/{c1['id']}")
api("DELETE", f"/data-source-categories/{c2['id']}")
print("7) 删除保护 + 清理 ok")

# 8) 清理系统 (无系统删除接口, 直接 DB 级无法清理; 保留 e2e 系统)
print("ALL PASSED")
