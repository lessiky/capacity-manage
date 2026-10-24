-- 0015: 脚本语言(python/bash/powershell)
-- 需求: 采集脚本支持 python/bash/powershell 三种语言; 远程执行时按语言选择运行器与文件后缀。
-- 存量脚本均为 python(本地沙箱), 故默认 python。

ALTER TABLE python_script ADD COLUMN IF NOT EXISTS language varchar(16) NOT NULL DEFAULT 'python';
COMMENT ON COLUMN python_script.language IS '脚本语言: python/bash/powershell';
