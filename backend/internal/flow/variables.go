package flow

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"capacity-manage/backend/internal/tradingday"
)

var varPattern = regexp.MustCompile(`\$\{([a-z_]+)(?::([0-9A-Za-z_:/.-]+))?\}`)

// ResolveVars 变量替换引擎（docs/10-design/05 §6）
// 支持: ${biz_date} ${prev_trading_day} ${next_trading_day} ${trading_day_offset:N}
//       ${system_code} ${hour} ${flow_code} ${metric_code}
// 日期变量支持 :format 后缀自定义输出格式, 如 ${prev_trading_day:YYYYMMDD}
// 交易日变量按流程 schedule_mode 解析: normal 模式按自然日退化为日历推导(非交易日回退)
func ResolveVars(ctx context.Context, cal *tradingday.Calendar, market string, base time.Time,
	systemCode, flowCode, metricCode string, isTradingMode bool) (map[string]string, error) {
	out := make(map[string]string)
	out["biz_date"] = base.Format("2006-01-02")
	out["hour"] = fmt.Sprintf("%02d", base.Hour())
	out["system_code"] = systemCode
	out["flow_code"] = flowCode
	out["metric_code"] = metricCode

	if market == "" {
		market = "SH"
	}
	prev, err := cal.PrevTradingDay(ctx, market, base)
	if err != nil {
		return nil, fmt.Errorf("prev_trading_day: %w", err)
	}
	next, err := cal.NextTradingDay(ctx, market, base)
	if err != nil {
		return nil, fmt.Errorf("next_trading_day: %w", err)
	}
	out["prev_trading_day"] = prev.Format("2006-01-02")
	out["next_trading_day"] = next.Format("2006-01-02")
	_ = isTradingMode
	return out, nil
}

// dateVars 支持 :format 后缀的日期类变量
var dateVars = map[string]bool{
	"biz_date": true, "prev_trading_day": true, "next_trading_day": true,
}

// DateLayout 用户日期格式 → Go time layout。
// 支持 Java 风格: YYYYMMDD / YYYY-MM-DD / YYYY/MM/DD / YYYYMM / YYYY.MM.DD 等
func DateLayout(format string) (string, error) {
	f := strings.TrimSpace(format)
	if f == "" {
		return "2006-01-02", nil
	}
	if !strings.Contains(f, "YYYY") {
		return "", fmt.Errorf("日期格式须包含 YYYY (如 YYYYMMDD / YYYY-MM-DD)")
	}
	out := strings.ReplaceAll(f, "YYYY", "2006")
	out = strings.ReplaceAll(out, "MM", "01")
	out = strings.ReplaceAll(out, "DD", "02")
	for i, r := range out {
		switch {
		case r >= '0' && r <= '9':
		case r == '-' || r == '/' || r == '.' || r == ' ':
		default:
			return "", fmt.Errorf("不支持的日期格式: %s (第 %d 位)", format, i+1)
		}
	}
	return out, nil
}

// FormatDateValue 将 YYYY-MM-DD 日期字符串按目标格式重新格式化
func FormatDateValue(dateStr, format string) (string, error) {
	layout, err := DateLayout(format)
	if err != nil {
		return "", err
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(dateStr))
	if err != nil {
		return "", fmt.Errorf("无法解析日期 %q: %w", dateStr, err)
	}
	return t.Format(layout), nil
}

// OffsetFn 计算 ${trading_day_offset:N(:format)}; 返回 (值, false) 表示保留占位符
type OffsetFn func(n int, format string) (string, bool)

// parseOffsetArg 解析 trading_day_offset 参数: "N" 或 "N:format"
func parseOffsetArg(arg string) (n int, format string, ok bool) {
	if arg == "" {
		return 0, "", true
	}
	if i := strings.IndexByte(arg, ':'); i >= 0 {
		if _, err := fmt.Sscanf(arg[:i], "%d", &n); err != nil {
			return 0, "", false
		}
		return n, arg[i+1:], true
	}
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil {
		return 0, "", false
	}
	return n, "", true
}

// renderCore 统一变量替换:
//
//	${var}                     直接替换
//	${dateVar:format}          日期变量自定义格式(如 YYYYMMDD / YYYY-MM-DD)
//	${trading_day_offset:N}    经 offsetFn 推导; 支持 N:format 组合
//
// strict=true 时未知变量/无法推导为错误; strict=false(SQL/日志采集) 保留原占位符
func renderCore(template string, vars map[string]string, escape func(string) string, offsetFn OffsetFn, strict bool) (string, error) {
	var renderErr error
	out := varPattern.ReplaceAllStringFunc(template, func(m string) string {
		g := varPattern.FindStringSubmatch(m)
		name, arg := g[1], g[2]
		var val string
		switch name {
		case "trading_day_offset":
			n, format, ok := parseOffsetArg(arg)
			if !ok {
				renderErr = fmt.Errorf("非法 trading_day_offset 参数 %q", arg)
				return m
			}
			if offsetFn == nil {
				if strict {
					renderErr = fmt.Errorf("trading_day_offset 需要交易日日历")
				}
				return m
			}
			s, done := offsetFn(n, format)
			if !done {
				if strict {
					renderErr = fmt.Errorf("无法推导第 %d 个交易日", n)
				}
				return m
			}
			val = s
		default:
			v, exists := vars[name]
			if !exists {
				if strict {
					renderErr = fmt.Errorf("unknown variable ${%s}", name)
				}
				return m
			}
			if arg != "" {
				if !dateVars[name] {
					renderErr = fmt.Errorf("变量 ${%s} 不支持格式参数", name)
					return m
				}
				s, err := FormatDateValue(v, arg)
				if err != nil {
					renderErr = err
					return m
				}
				v = s
			}
			val = v
		}
		if escape != nil {
			val = escape(val)
		}
		return val
	})
	return out, renderErr
}

// RenderTemplate 采集模板宽容替换(未知占位符原样保留, 执行时由数据库/文件暴露)
// escape 作用于替换值(SQL 注入防护); 传 nil 表示不转义
func RenderTemplate(template string, vars map[string]string, escape func(string) string, offsetFn OffsetFn) (string, error) {
	return renderCore(template, vars, escape, offsetFn, false)
}

type Resolver struct {
	ctx    context.Context
	cal    *tradingday.Calendar
	market string
	base   time.Time
}

// Render 将模板中的 ${var} / ${var:format} / ${trading_day_offset:N(:format)} 全部替换(严格: 未知变量报错)
func (r *Resolver) Render(template string, vars map[string]string) (string, error) {
	offsetFn := func(n int, format string) (string, bool) {
		td, err := r.cal.Offset(r.ctx, r.market, r.base, n)
		if err != nil {
			return "", false
		}
		s := td.Format("2006-01-02")
		if format != "" {
			s, err = FormatDateValue(s, format)
			if err != nil {
				return "", false
			}
		}
		return s, true
	}
	return renderCore(template, vars, nil, offsetFn, true)
}

// NewResolver 创建渲染器
func NewResolver(ctx context.Context, cal *tradingday.Calendar, market string, base time.Time) *Resolver {
	return &Resolver{ctx: ctx, cal: cal, market: market, base: base}
}