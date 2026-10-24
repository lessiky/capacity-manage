package tradingday

import (
	"context"
	"fmt"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Calendar 交易日服务: 日历查询/推导, Redis 缓存(日历变更主动失效)
type Calendar struct {
	db     *gorm.DB
	rdb    *redis.Client
	params *system.Params
}

func NewCalendar(db *gorm.DB, rdb *redis.Client, params *system.Params) *Calendar {
	return &Calendar{db: db, rdb: rdb, params: params}
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func (c *Calendar) cacheKey(market string, d time.Time) string {
	return "td:" + market + ":" + d.Format("2006-01-02")
}

// IsTradingDay 某日是否交易日（日历缺失时按周末规则兜底）
func (c *Calendar) IsTradingDay(ctx context.Context, market string, d time.Time) (bool, error) {
	d = dateOnly(d)
	key := c.cacheKey(market, d)
	if v, err := c.rdb.Get(ctx, key).Int(); err == nil {
		return v == 1, nil
	}
	var row persistence.TradingDayCalendar
	err := c.db.Where("market = ? AND cal_date = ?", market, d).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		// 日历未覆盖: 按周末规则兜底
		is := d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
		_ = c.rdb.Set(ctx, key, map[bool]int{true: 1, false: 0}[is], 1*time.Hour).Err()
		return is, nil
	}
	if err != nil {
		return false, err
	}
	_ = c.rdb.Set(ctx, key, map[bool]int{true: 1, false: 0}[row.IsTrading], 24*time.Hour).Err()
	return row.IsTrading, nil
}

// tradingDaysIn 返回 [from, to] 内交易日(升序)
func (c *Calendar) tradingDaysIn(ctx context.Context, market string, from, to time.Time) ([]time.Time, error) {
	var dates []time.Time
	err := c.db.Model(&persistence.TradingDayCalendar{}).
		Where("market = ? AND cal_date >= ? AND cal_date <= ? AND is_trading = ?",
			market, from, to, true).Order("cal_date").Pluck("cal_date", &dates).Error
	if err != nil {
		return nil, err
	}
	return dates, nil
}

// PrevTradingDay 前一交易日
func (c *Calendar) PrevTradingDay(ctx context.Context, market string, d time.Time) (time.Time, error) {
	return c.Offset(ctx, market, d, -1)
}

// NextTradingDay 后一交易日
func (c *Calendar) NextTradingDay(ctx context.Context, market string, d time.Time) (time.Time, error) {
	return c.Offset(ctx, market, d, +1)
}

// Offset 前/后第 N 个交易日（N>0 向后, N<0 向前; 0 返回 d 本身）
func (c *Calendar) Offset(ctx context.Context, market string, d time.Time, n int) (time.Time, error) {
	d = dateOnly(d)
	if n == 0 {
		return d, nil
	}
	margin := n + 21
	if n < 0 {
		margin = -n + 21
	}
	// 日历未覆盖时 tradingDaysIn 可能为空 → 退化为逐日 IsTradingDay 推导
	days, err := c.tradingDaysIn(ctx, market, d.AddDate(0, 0, -margin), d.AddDate(0, 0, margin))
	if err != nil {
		return time.Time{}, err
	}
	if len(days) == 0 {
		// 日历未覆盖: 逐日推导(计数交易日)
		absn := n
		if absn < 0 {
			absn = -absn
		}
		cnt := 0
		for i := 1; i <= margin*2 && cnt < absn; i++ {
			probed := d
			if n > 0 {
				probed = d.AddDate(0, 0, i)
			} else {
				probed = d.AddDate(0, 0, -i)
			}
			is, err := c.IsTradingDay(ctx, market, probed)
			if err != nil {
				return time.Time{}, err
			}
			if is {
				cnt++
				if cnt == absn {
					return probed, nil
				}
			}
		}
	}
	if n > 0 {
		cnt := 0
		for _, td := range days {
			if td.After(d) {
				cnt++
				if cnt == n {
					return td, nil
				}
			}
		}
	} else {
		cnt := 0
		for i := len(days) - 1; i >= 0; i-- {
			if days[i].Before(d) {
				cnt++
				if cnt == -n {
					return days[i], nil
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("cannot derive trading day offset %d from %s (market %s), 请检查日历覆盖范围", n, d.Format("2006-01-02"), market)
}

// windowStartHM 解析调度窗口起点: 兼容旧版单时刻 "HH:MM" 与新版范围 "HH:MM-HH:MM"（取范围起点触发）
func windowStartHM(windowTime string) (int, int, error) {
	s := strings.TrimSpace(windowTime)
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	var hh, mm int
	if _, err := fmt.Sscanf(s, "%d:%d", &hh, &mm); err != nil {
		return 0, 0, fmt.Errorf("bad window time %q", windowTime)
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("bad window time %q", windowTime)
	}
	return hh, mm, nil
}

// NextTriggerAfter 交易日调度: 给定窗口(单时刻 "HH:MM" 或范围 "HH:MM-HH:MM", 窗口起点触发), 返回 from 之后第一个触发时刻
func (c *Calendar) NextTriggerAfter(ctx context.Context, market, windowTime string, from time.Time) (time.Time, error) {
	hh, mm, err := windowStartHM(windowTime)
	if err != nil {
		return time.Time{}, err
	}
	todayWin := dateOnly(from).Add(time.Duration(hh) * time.Hour + time.Duration(mm) * time.Minute)
	if todayWin.After(from) {
		if isTrading, _ := c.IsTradingDay(ctx, market, dateOnly(from)); isTrading {
			return todayWin, nil
		}
	}
	d := dateOnly(from).AddDate(0, 0, 1)
	for i := 0; i < 90; i++ {
		if isTrading, _ := c.IsTradingDay(ctx, market, d); isTrading {
			return d.Add(time.Duration(hh) * time.Hour + time.Duration(mm) * time.Minute), nil
		}
		d = d.AddDate(0, 0, 1)
	}
	return time.Time{}, fmt.Errorf("no trading day found after %s (market %s)", from.Format("2006-01-02"), market)
}

// InvalidateCache 日历批量变更后失效相关缓存
func (c *Calendar) InvalidateCache(ctx context.Context, market string, from, to time.Time) {
	for d := dateOnly(from); !d.After(dateOnly(to)); d = d.AddDate(0, 0, 1) {
		_ = c.rdb.Del(ctx, c.cacheKey(market, d)).Err()
	}
}