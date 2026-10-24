package judge

import (
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/system"
)

// Judge 阈值判定与水位计算（docs/10-design/04 §1）
type Judge struct {
	params *system.Params
}

func New(params *system.Params, _ interface{}) *Judge {
	return &Judge{params: params}
}

type Result struct {
	Level      string  // normal/warn/danger
	Value      *float64
	WaterLevel *float64
	Threshold  *float64 // 触发的阈值(告警内容用)
}

// Evaluate 单值判定（清洗值为空或异常时跳过, 返回 normal）
func (j *Judge) Evaluate(m *persistence.CapacityMetric, value *float64) Result {
	res := Result{Level: "normal"}
	if value == nil || m.WarnLine == nil && m.DangerLine == nil {
		return res
	}
	v := *value
	res.Value = value
	// 水位
	res.WaterLevel = j.waterLevel(m, v)
	higher := m.JudgeDirection != "lower_worse"
	var danger, warn *float64
	danger, warn = m.DangerLine, m.WarnLine
	if higher {
		if danger != nil && v >= *danger {
			res.Level = "danger"
			res.Threshold = danger
			return res
		}
		if warn != nil && v >= *warn {
			res.Level = "warn"
			res.Threshold = warn
			return res
		}
	} else {
		if danger != nil && v <= *danger {
			res.Level = "danger"
			res.Threshold = danger
			return res
		}
		if warn != nil && v <= *warn {
			res.Level = "warn"
			res.Threshold = warn
			return res
		}
	}
	return res
}

// waterLevel 水位: percent 模式 relative to baseline; lower_worse 为剩余占比
func (j *Judge) waterLevel(m *persistence.CapacityMetric, v float64) *float64 {
	if m.Baseline == nil || *m.Baseline == 0 {
		return nil
	}
	base := *m.Baseline
	var wl float64
	if m.JudgeDirection == "lower_worse" {
		wl = (base - v) / base
	} else {
		wl = v / base
	}
	if wl < 0 {
		wl = 0
	}
	if wl > 1 {
		wl = 1
	}
	r := round4(wl)
	return &r
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}