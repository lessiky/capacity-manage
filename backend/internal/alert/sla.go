package alert

import (
	"math"
	"time"

	"capacity-manage/backend/internal/system"
)

// SLA 统计（FR-MON-24, docs/10-design/04 §4）:
// MTTA = avg(claimed_at - trigger_at), 未认领不计入
// MTTR = avg((recovered_at ∨ closed_at) - claimed_at)
// 达成率 = 时限内闭环数 / 应闭环数（按级别分别统计）
// 目标值: 响应 P1 30min / P2 2h / P3 4h / P4 24h;
// 闭环 P1 4h / P2 24h / P3 24h / P4 5d, 均可参数化

type slaTarget struct {
	claim   int // 响应时限（分钟）
	resolve int // 闭环时限（自认领起, 分钟）
}

// SlaTargets 各级别 SLA 目标（系统参数可覆盖）
func SlaTargets(p *system.Params) map[string]slaTarget {
	claim := func(level string, def int) int {
		if p == nil {
			return def
		}
		return p.Int("sla_claim_"+level+"_minutes", def)
	}
	resolve := func(level string, def int) int {
		if p == nil {
			return def
		}
		return p.Int("sla_resolve_"+level+"_minutes", def)
	}
	return map[string]slaTarget{
		"p1": {claim: claim("p1", 30), resolve: resolve("p1", 240)},
		"p2": {claim: claim("p2", 120), resolve: resolve("p2", 1440)},
		"p3": {claim: claim("p3", 240), resolve: resolve("p3", 1440)},
		"p4": {claim: claim("p4", 1440), resolve: resolve("p4", 7200)},
	}
}

// slaRow 参与 SLA 计算的告警行（由 /alerts/stats 查询预取）
type slaRow struct {
	Level     string     `gorm:"column:level"`
	TriggerAt time.Time  `gorm:"column:trigger_at"`
	ClaimedAt *time.Time `gorm:"column:claimed_at"`
	EndAt     *time.Time `gorm:"column:end_at"` // recovered_at ∨ closed_at
}

type SLAByLevel struct {
	Level      string   `json:"level"`
	ClaimMin   int      `json:"claimMin"`
	ResolveMin int      `json:"resolveMin"`
	Total      int      `json:"total"`  // 应闭环数（区间内触发）
	Closed     int      `json:"closed"` // 已闭环数（恢复/关闭）
	InTime     int      `json:"inTime"` // 时限内闭环数
	MTTAMin    *float64 `json:"mttaMinutes"`
	MTTRMin    *float64 `json:"mttrMinutes"`
	Rate       *float64 `json:"rate"` // 达成率 %
}

// ComputeSLA 按级别计算 MTTA/MTTR/达成率（纯函数, 便于单测）
func ComputeSLA(rows []slaRow, targets map[string]slaTarget) []SLAByLevel {
	type acc struct {
		total, closed, inTime, mttN, mttrN int
		mttSec, mttrSec                    float64
	}
	by := map[string]*acc{}
	for _, r := range rows {
		a := by[r.Level]
		if a == nil {
			a = &acc{}
			by[r.Level] = a
		}
		a.total++
		if r.EndAt != nil {
			a.closed++
		}
		if r.ClaimedAt == nil {
			continue
		}
		mttSec := r.ClaimedAt.Sub(r.TriggerAt).Seconds()
		a.mttN++
		a.mttSec += mttSec
		if r.EndAt != nil {
			mttrSec := r.EndAt.Sub(*r.ClaimedAt).Seconds()
			a.mttrN++
			a.mttrSec += mttrSec
			t := targets[r.Level]
			if t.claim > 0 && t.resolve > 0 &&
				mttSec <= float64(t.claim)*60 && mttrSec <= float64(t.resolve)*60 {
				a.inTime++
			}
		}
	}
	out := make([]SLAByLevel, 0, len(by))
	for _, lv := range []string{"p1", "p2", "p3", "p4"} {
		a := by[lv]
		if a == nil {
			continue
		}
		t := targets[lv]
		s := SLAByLevel{
			Level: lv, ClaimMin: t.claim, ResolveMin: t.resolve,
			Total: a.total, Closed: a.closed, InTime: a.inTime,
		}
		if a.mttN > 0 {
			v := round1(a.mttSec / float64(a.mttN) / 60)
			s.MTTAMin = &v
		}
		if a.mttrN > 0 {
			v := round1(a.mttrSec / float64(a.mttrN) / 60)
			s.MTTRMin = &v
		}
		if a.total > 0 {
			v := round1(float64(a.inTime) / float64(a.total) * 100)
			s.Rate = &v
		}
		out = append(out, s)
	}
	return out
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
