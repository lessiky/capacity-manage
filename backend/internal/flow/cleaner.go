package flow

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// RawRow 采集原始行
type RawRow struct {
	Raw   string
	Value *float64
	Unit  string
}

// CleanRow 清洗后行
type CleanRow struct {
	Raw       string
	Value     *float64
	Unit      string
	IsAnomaly bool
	Reason    string
	Rules     []string
}

// RawValuePtr 原始值数值化（用于日内点 raw_value 列）
func (r CleanRow) RawValuePtr() *float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(r.Raw), 64); err == nil {
		return &v
	}
	return nil
}

// ScriptRef Python 脚本引用（采集/脚本清洗共用）
type ScriptRef struct {
	ScriptID int64 `json:"scriptId"`
	Version  int   `json:"version"`
}

// Transform 流程级转换规则（FR-COL-20），随 clean_config 定义并生效
type Transform struct {
	Type   string  `json:"type"` // unit / linear / round
	Target string  `json:"target"` // unit 换算目标单位, 如 GB
	A      float64 `json:"a"`
	B      float64 `json:"b"`
	Digits int     `json:"digits"`
}

// CleanConfig 清洗配置（collect_flow.clean_config）
type CleanConfig struct {
	Transforms []Transform `json:"transforms"`
	DedupTake  string      `json:"dedupTake"` // latest/first/avg/max/min/sum, 默认按指标 stat_mode
	Script     *ScriptRef  `json:"script"`    // FR-COL-18: Python 脚本清洗, 插入"类型转换之后"
}

// CleanPhase1 内置链第 1 段: 类型转换 / 单位换算（FR-COL-20）
// 空值/非数值在行上标记, 异常值域判定在第 2 段执行（脚本清洗可先改值再判域）
func CleanPhase1(rawRows []RawRow, cfg CleanConfig, metricUnit string) []CleanRow {
	rows := make([]CleanRow, 0, len(rawRows))
	for _, r := range rawRows {
		cr := CleanRow{Raw: r.Raw, Unit: r.Unit}
		v, err := strconv.ParseFloat(strings.TrimSpace(r.Raw), 64)
		if err != nil {
			cr.IsAnomaly = true
			cr.Reason = "非数值: " + trunc(r.Raw, 32)
			cr.Rules = []string{"type_convert:fail"}
			rows = append(rows, cr)
			continue
		}
		cr.Value = &v
		cr.Rules = []string{"type_convert"}
		// 内置转换规则
		target := metricUnit
		for _, t := range cfg.Transforms {
			switch t.Type {
			case "unit":
				target = t.Target
			case "linear":
				nv := t.A*v + t.B
				cr.Value = &nv
				cr.Rules = append(cr.Rules, fmt.Sprintf("linear(a=%g,b=%g)", t.A, t.B))
			case "round":
				d := t.Digits
				if d == 0 {
					d = 2
				}
				fac := math.Pow10(d)
				nv := math.Round(*cr.Value*fac) / fac
				cr.Value = &nv
				cr.Rules = append(cr.Rules, fmt.Sprintf("round(%d)", d))
			}
			if cr.Value == nil {
				break
			}
		}
		// 单位换算
		if r.Unit != "" && target != "" && r.Unit != target {
			f, ok := unitFactor(r.Unit, target)
			if ok {
				nv := *cr.Value * f
				cr.Value = &nv
				cr.Rules = append(cr.Rules, fmt.Sprintf("unit(%s→%s)", r.Unit, target))
			}
		}
		rows = append(rows, cr)
	}
	return rows
}

// CleanPhase2 内置链第 2 段: 空值与异常值标记 → 去重取值（FR-COL-21/22）
func CleanPhase2(rows []CleanRow, cfg CleanConfig, metricType, statMode string) ([]CleanRow, CleanRow) {
	for i := range rows {
		if rows[i].Value == nil {
			continue
		}
		if !rows[i].IsAnomaly {
			switch metricType {
			case "usage":
				if *rows[i].Value < 0 || *rows[i].Value > 100 {
					rows[i].IsAnomaly = true
					rows[i].Reason = "使用率超出 [0,100]"
					rows[i].Rules = append(rows[i].Rules, "anomaly:range")
				}
			default:
				if *rows[i].Value < 0 {
					rows[i].IsAnomaly = true
					rows[i].Reason = "负值"
					rows[i].Rules = append(rows[i].Rules, "anomaly:range")
				}
			}
		}
	}
	// 去重取值: 多条 → 按统计方式
	if len(rows) > 1 {
		take := cfg.DedupTake
		if take == "" {
			take = statMode
		}
		return rows, aggregate(rows, take)
	}
	if len(rows) == 1 {
		return rows, rows[0]
	}
	return rows, CleanRow{IsAnomaly: true, Reason: "无有效数据"}
}

// Clean 内置完整清洗链（类型转换/单位换算 → 异常标记 → 去重取值），不含脚本清洗
func Clean(rawRows []RawRow, cfg CleanConfig, metricUnit, metricType, statMode string) ([]CleanRow, CleanRow) {
	return CleanPhase2(CleanPhase1(rawRows, cfg, metricUnit), cfg, metricType, statMode)
}

func aggregate(rows []CleanRow, mode string) CleanRow {
	valid := make([]float64, 0, len(rows))
	for _, r := range rows {
		if r.Value != nil && !r.IsAnomaly {
			valid = append(valid, *r.Value)
		}
	}
	if len(valid) == 0 {
		return CleanRow{IsAnomaly: true, Reason: "全部数据异常或为空"}
	}
	var out float64
	switch mode {
	case "sum":
		for _, v := range valid {
			out += v
		}
	case "max":
		out = valid[0]
		for _, v := range valid[1:] {
			if v > out {
				out = v
			}
		}
	case "min":
		out = valid[0]
		for _, v := range valid[1:] {
			if v < out {
				out = v
			}
		}
	case "avg":
		for _, v := range valid {
			out += v
		}
		out /= float64(len(valid))
	case "first":
		out = valid[0]
	default: // latest
		out = valid[len(valid)-1]
	}
	return CleanRow{Value: &out, Rules: []string{"dedup:" + mode}}
}

var unitFactors = map[string]float64{
	"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40, "PB": 1 << 50,
}

// unitFactor 返回 from→to 换算系数（仅容量单位; 不支持返回 false）
func unitFactor(from, to string) (float64, bool) {
	f, ok1 := unitFactors[strings.ToUpper(from)]
	t, ok2 := unitFactors[strings.ToUpper(to)]
	if !ok1 || !ok2 {
		return 0, false
	}
	return f / t, true
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
