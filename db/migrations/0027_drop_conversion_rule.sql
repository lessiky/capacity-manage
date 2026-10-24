-- 移除自定义转换规则（原 FR-COL-23）
--
-- 该功能仅提供单值试算接口，从未接入采集执行链路：采集清洗只读取
-- collect_flow.clean_config 中的 transforms（unit/linear/round）。
-- 为避免两套语义相近的转换配置造成误解，整体下线该功能并删除其存储表。
-- 历史建表语句保留在 0023_open_api_retention.sql 中不改写，已应用的库由本迁移回收。

DROP TABLE IF EXISTS conversion_rule;
