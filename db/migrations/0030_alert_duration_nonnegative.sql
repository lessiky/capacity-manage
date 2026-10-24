-- 告警持续时长负值回收
--
-- alert_record.duration_min 由「判定时刻 - 触发时刻」得出。日汇总与补采以业务日
-- 00:00 作为判定时刻回放, 可能早于实时触发时刻, 从而把已累计的持续时长写成负数。
-- 服务端已改为只接受正向增量并保持单调(见 internal/alert/service.go); 本迁移回收
-- 已经落库的负值。
--
-- 负值无法还原真实时长: 历史判定时刻未留存, 且已恢复/关闭的告警不会再被评估,
-- 因此统一归零, 由后续判定重新累计; 0 与正数一律保持原样。

UPDATE alert_record SET duration_min = 0 WHERE duration_min < 0;
