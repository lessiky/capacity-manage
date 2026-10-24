// Package clock 提供业务日(本地日期)工具。
package clock

import "time"

// DayStart 返回 t 所属本地日期的 00:00:00 时刻。
// 注意: 不可使用 t.Truncate(24 * time.Hour) —— Truncate 基于纪元绝对时间对齐,
// 在非 UTC 时区(如 Asia/Shanghai)会把日期错移到前一天。
func DayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
