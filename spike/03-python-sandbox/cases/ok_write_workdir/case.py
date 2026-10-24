import os, json
with open("result.txt", "w", encoding="utf-8") as f:
    f.write("hello")
print(json.dumps({"ok": True, "file": os.path.exists("result.txt")}))
