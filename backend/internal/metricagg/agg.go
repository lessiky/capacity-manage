// Package metricagg 汇总指标"模块级跨子指标聚合"的统一计算口径。
// metric 模块视图 API(展示)与 data 日汇总(日值 + 阈值告警联动)共用, 保证展示与告警口径一致。
package metricagg

// Pair 某时间点单个子指标的 (值, 权重) 对; 该指标未配置权重指标时 Weight 为 nil。
type Pair struct {
	Value  *float64
	Weight *float64
}

// Mode 模块跨子指标合并方式:
//   - 仅一个子指标: self(直接取值)
//   - 任一子指标配置了权重指标: weighted_avg(加权均值)
//   - 否则: 取首个子指标的 statMode(sum/max/min/avg, 默认 max)
func Mode(statModes []string, weights []*string) string {
	if len(statModes) <= 1 {
		return "self"
	}
	for _, w := range weights {
		if w != nil && *w != "" {
			return "weighted_avg"
		}
	}
	switch statModes[0] {
	case "sum", "max", "min", "avg":
		return statModes[0]
	default:
		return "max"
	}
}

// Aggregate 计算模块在单个时间点的聚合值:
//   - self/max/min/avg/sum: 按语义直接聚合(忽略权重)
//   - weighted_avg: 加权均值 sum(值×权重)/sum(权重), 仅统计权重>0 的子指标;
//     无任何可用权重时退化为简单均值
//
// 全部值为空时返回 nil。
func Aggregate(mode string, pairs []Pair) *float64 {
	var sum float64
	var mn, mx *float64
	var wnum, wsum float64
	n := 0
	for i := range pairs {
		p := &pairs[i]
		if p.Value == nil {
			continue
		}
		if n == 0 {
			mn, mx = p.Value, p.Value
		} else {
			if *p.Value < *mn {
				mn = p.Value
			}
			if *p.Value > *mx {
				mx = p.Value
			}
		}
		n++
		sum += *p.Value
		if p.Weight != nil && *p.Weight > 0 {
			wnum += *p.Value * *p.Weight
			wsum += *p.Weight
		}
	}
	if n == 0 {
		return nil
	}
	switch mode {
	case "sum":
		return &sum
	case "min":
		return mn
	case "avg":
		avg := sum / float64(n)
		return &avg
	case "weighted_avg":
		if wsum > 0 {
			avg := wnum / wsum
			return &avg
		}
		avg := sum / float64(n)
		return &avg
	default: // max / self
		return mx
	}
}
