package persistence

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Seed 初始化: 管理员/内置角色/权限点/交易日日历(2026-2027)
// 幂等: 已存在则跳过。
func Seed(ctx context.Context, db *gorm.DB) error {
	// 顺序敏感: 先建内置角色与权限, 再建 admin 并绑定角色(INSERT...SELECT 依赖角色行)
	if err := seedRolesAndPerms(ctx, db); err != nil {
		return err
	}
	if err := seedAdmin(ctx, db); err != nil {
		return err
	}
	if err := seedTradingCalendar(ctx, db); err != nil {
		return err
	}
	if err := seedDemoAggregate(ctx, db); err != nil {
		return err
	}
	return nil
}

func seedAdmin(ctx context.Context, db *gorm.DB) error {
	var n int64
	if err := db.Model(&SysUser{}).Where("username = ?", "admin").Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("Admin@123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	admin := SysUser{
		Username: "admin", PasswordHash: string(hash), RealName: "平台管理员",
		Dept: "信息科技", Status: "active",
	}
	if err := db.WithContext(ctx).Create(&admin).Error; err != nil {
		return err
	}
	return db.Exec(`INSERT INTO sys_user_role (user_id, role_id)
		SELECT u.id, r.id FROM sys_user u, sys_role r WHERE u.username='admin' AND r.code='platform_admin'`).Error
}

// 内置 5 类角色（需求 2.1）
var builtinRoles = []SysRole{
	{Code: "platform_admin", Name: "平台管理员", Builtin: true, Remark: "全部功能权限与数据权限"},
	{Code: "ops", Name: "运维", Builtin: true, Remark: "采集/告警处理/数据源管理"},
	{Code: "system_owner", Name: "系统负责人", Builtin: true, Remark: "所辖业务系统指标与流程管理"},
	{Code: "biz_owner", Name: "业务负责人", Builtin: true, Remark: "所辖业务系统容量查看与报表"},
	{Code: "viewer", Name: "查看者", Builtin: true, Remark: "只读"},
}

// 权限点(code, name, type, path)
var builtinPerms = []struct {
	Code, Name, Type, Path string
}{
	{"dashboard:view", "监控大盘", "menu", "/dashboard"},
	{"system:view", "业务系统查看", "menu", "/systems"},
	{"system:manage", "业务系统管理", "button", ""},
	{"system:export", "业务系统导出", "button", ""},
	{"metric:view", "指标查看", "menu", "/metrics"},
	{"metric:manage", "指标与阈值管理", "button", ""},
	{"datasource:view", "数据源查看", "menu", "/datasources"},
	{"datasource:manage", "数据源管理", "button", ""},
	{"flow:view", "采集流程查看", "menu", "/flows"},
	{"flow:manage", "采集流程管理", "button", ""},
	{"script:view", "脚本查看", "menu", "/scripts"},
	{"script:manage", "脚本管理", "button", ""},
	{"task:view", "任务监控查看", "menu", "/tasks"},
	{"task:manage", "任务手工触发", "button", ""},
	{"data:view", "数据查询", "menu", "/data"},
	{"data:correct", "数据手工修正", "button", ""},
	{"alert:view", "告警查看", "menu", "/alerts"},
	{"alert:handle", "告警认领处理", "button", ""},
	{"alert:export", "告警导出", "button", ""},
	{"alert:policy:manage", "告警升级策略管理", "button", ""},
	{"notification:view", "通知配置查看", "menu", "/notifications"},
	{"notification:manage", "通知配置管理", "button", ""},
	{"report:view", "报表查看", "menu", "/reports"},
	{"report:export", "报表导出", "button", ""},
	{"tradingday:view", "交易日查看", "menu", "/tradingdays"},
	{"tradingday:manage", "交易日管理", "button", ""},
	{"ai:use", "AI 辅助使用", "button", ""},
	{"forecast:event:manage", "事件因子管理", "button", ""},
	{"admin:user:manage", "用户管理", "menu", "/admin/users"},
	{"admin:role:manage", "角色权限管理", "menu", "/admin/roles"},
	{"admin:datascope:manage", "数据权限管理", "menu", "/admin/datascope"},
	{"admin:param:manage", "系统参数管理", "menu", "/admin/params"},
	{"admin:log:view", "操作日志/审计", "menu", "/admin/logs"},
	{"admin:health:view", "平台自检", "menu", "/admin/health"},
}

var rolePerms = map[string][]string{
	"platform_admin": {"dashboard:view", "system:view", "system:manage", "system:export",
		"metric:view", "metric:manage", "datasource:view", "datasource:manage",
		"flow:view", "flow:manage", "script:view", "script:manage",
		"task:view", "task:manage", "data:view", "data:correct",
		"alert:view", "alert:handle", "alert:export", "alert:policy:manage", "notification:view", "notification:manage",
		"report:view", "report:export",
		"tradingday:view", "tradingday:manage", "ai:use", "forecast:event:manage",
		"admin:user:manage", "admin:role:manage", "admin:datascope:manage",
		"admin:param:manage", "admin:log:view", "admin:health:view"},
	"ops": {"dashboard:view", "system:view", "metric:view",
		"datasource:view", "datasource:manage", "flow:view", "flow:manage",
		"script:view", "script:manage", "task:view", "task:manage", "data:view",
		"alert:view", "alert:handle", "alert:export", "alert:policy:manage", "notification:view", "notification:manage",
		"report:view", "report:export",
		"tradingday:view", "ai:use", "forecast:event:manage", "admin:health:view"},
	"system_owner": {"dashboard:view", "system:view", "system:manage", "system:export",
		"metric:view", "metric:manage", "flow:view", "flow:manage",
		"task:view", "data:view", "alert:view", "alert:handle", "notification:view",
		"report:view", "report:export", "tradingday:view", "ai:use", "forecast:event:manage"},
	"biz_owner": {"dashboard:view", "system:view", "system:export",
		"metric:view", "data:view", "alert:view",
		"notification:view",
		"report:view", "report:export", "tradingday:view"},
	"viewer": {"dashboard:view", "system:view", "metric:view", "data:view", "notification:view",
		"alert:view", "report:view", "tradingday:view"},
}

func seedRolesAndPerms(ctx context.Context, db *gorm.DB) error {
	var n int64
	if err := db.Model(&SysRole{}).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx := db.WithContext(ctx).Begin()
	defer func() {
		if tx.Error != nil {
			tx.Rollback()
		}
	}()
	roleID := map[string]int64{}
	for _, r := range builtinRoles {
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		var got SysRole
		tx.Where("code = ?", r.Code).First(&got)
		roleID[r.Code] = got.ID
	}
	permID := map[string]int64{}
	for _, p := range builtinPerms {
		pg := SysPermission{Code: p.Code, Name: p.Name, Type: p.Type, Path: p.Path}
		if err := tx.Create(&pg).Error; err != nil {
			return err
		}
		var got SysPermission
		tx.Where("code = ?", p.Code).First(&got)
		permID[p.Code] = got.ID
	}
	for roleCode, codes := range rolePerms {
		uid := roleID[roleCode]
		for _, c := range codes {
			if err := tx.Create(&SysRolePermission{RoleID: uid, PermissionID: permID[c]}).Error; err != nil {
				return err
			}
		}
	}
	return tx.Commit().Error
}

// cnMarketHolidays A 股休市日（法定节假日，2026-2027）。
// 注: 以交易所/国务院办公厅最终公告为准，seed 后可在 UI 人工修正或 Excel 导入（source=manual）。
var cnMarketHolidays = map[string]string{
	// 2026
	"2026-01-01": "holiday",
	"2026-02-16": "holiday",
	"2026-02-17": "holiday",
	"2026-02-18": "holiday",
	"2026-02-19": "holiday",
	"2026-02-20": "holiday",
	"2026-02-22": "holiday",
	"2026-04-06": "holiday",
	"2026-05-01": "holiday",
	"2026-05-04": "holiday",
	"2026-05-05": "holiday",
	"2026-06-19": "holiday",
	"2026-06-22": "holiday",
	"2026-09-25": "holiday",
	"2026-10-01": "holiday",
	"2026-10-02": "holiday",
	"2026-10-05": "holiday",
	"2026-10-06": "holiday",
	"2026-10-07": "holiday",
	// 2027
	"2027-01-01": "holiday",
	"2027-02-08": "holiday",
	"2027-02-09": "holiday",
	"2027-02-10": "holiday",
	"2027-02-11": "holiday",
	"2027-04-05": "holiday",
	"2027-05-03": "holiday",
	"2027-05-04": "holiday",
	"2027-05-05": "holiday",
	"2027-06-09": "holiday",
	"2027-06-11": "holiday",
	"2027-09-15": "holiday",
	"2027-10-01": "holiday",
	"2027-10-04": "holiday",
	"2027-10-05": "holiday",
	"2027-10-06": "holiday",
	"2027-10-07": "holiday",
}

// seedCalendarRow 单一日历行分类（纯函数, 便于单测）。
// d 须为某时区下的 00:00:00 本地时刻; 周末恒休, 法定节假日休市, 其余为交易日。
func seedCalendarRow(d time.Time) TradingDayCalendar {
	wd := d.Weekday()
	switch {
	case wd == time.Saturday || wd == time.Sunday:
		return TradingDayCalendar{Market: "SH", CalDate: d, IsTrading: false, DayType: "weekend", Source: "seed"}
	default:
		if _, isHoliday := cnMarketHolidays[d.Format("2006-01-02")]; isHoliday {
			return TradingDayCalendar{Market: "SH", CalDate: d, IsTrading: false, DayType: "holiday", Source: "seed"}
		}
		return TradingDayCalendar{Market: "SH", CalDate: d, IsTrading: true, DayType: "trading", Source: "seed"}
	}
}

// seedTradingCalendar 生成 SH 市场 2026-01-01 ~ 2027-12-31 日历（工作日-节假日；A 股周末恒休）
func seedTradingCalendar(ctx context.Context, db *gorm.DB) error {
	var n int64
	if err := db.Model(&TradingDayCalendar{}).Where("market = ?", "SH").Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	loc := time.FixedZone("CST", 8*3600)
	rows := make([]TradingDayCalendar, 0, 750)
	for d := time.Date(2026, 1, 1, 0, 0, 0, 0, loc); !d.After(time.Date(2027, 12, 31, 0, 0, 0, 0, loc)); d = d.AddDate(0, 0, 1) {
		rows = append(rows, seedCalendarRow(d))
	}
	batch := 200
	for i := 0; i < len(rows); i += batch {
		end := i + batch
		if end > len(rows) {
			end = len(rows)
		}
		sub := rows[i:end]
		if err := db.WithContext(ctx).Create(&sub).Error; err != nil {
			return fmt.Errorf("seed calendar batch %d: %w", i, err)
		}
	}
	return nil
}

// ---------- 汇总指标演示数据 ----------
// 以"服务请求"汇总指标为例: 汇总 -> 子指标分组(订单/支付) -> 统计模块(请求量/最大耗时/平均耗时)
// 幂等: SRV_REQ 已存在则整体跳过; 无业务系统时先建演示系统 DEMO_CORE
func seedDemoAggregate(ctx context.Context, db *gorm.DB) error {
	var n int64
	if err := db.Model(&CapacityMetric{}).Where("metric_code = ?", "SRV_REQ").Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// 归属系统: 优先取已有系统, 无则建演示系统
	today := time.Now().Truncate(24 * time.Hour)
	tx := db.Begin()
	defer func() { _ = tx.Rollback() }()
	var sys BusinessSystem
	if err := tx.Order("created_at").First(&sys).Error; err != nil {
		sys = BusinessSystem{
			SystemCode: "DEMO_CORE", Name: "演示核心系统", Dept: "信息科技",
			Owner: "admin", Level: "general", Status: "normal",
			Remark: "种子数据演示用业务系统",
		}
		if err := tx.Create(&sys).Error; err != nil {
			return err
		}
	}
	// 演示数据跨月, 先确保月分区存在(分区函数幂等)
	rangeStart := today.AddDate(0, 0, -62)
	if err := tx.Exec(`SELECT ensure_partitions('metric_value_day', ?, ?)`, rangeStart, today).Error; err != nil {
		return err
	}
	if err := tx.Exec(`SELECT ensure_partitions('metric_value_rt', ?, ?)`, rangeStart, today).Error; err != nil {
		return err
	}
	rnd := rand.New(rand.NewSource(20261016))

	type modSpec struct {
		Code, Name, Module, Unit, StatMode string
		Baseline, Warn, Danger             float64
		Weight                             string // 权重指标(加权均值, 如平均耗时按请求量加权)
	}
	specs := map[string][]modSpec{
		"SRV_REQ.ORD": {
			{"SRV_REQ.ORD.COUNT", "请求量", "count", "笔", "sum", 1200000, 80, 90, ""},
			{"SRV_REQ.ORD.MAX", "最大耗时", "max_latency", "ms", "max", 1000, 80, 90, ""},
			{"SRV_REQ.ORD.AVG", "平均耗时", "avg_latency", "ms", "avg", 300, 70, 85, "SRV_REQ.ORD.COUNT"},
		},
		"SRV_REQ.PAY": {
			{"SRV_REQ.PAY.COUNT", "请求量", "count", "笔", "sum", 600000, 80, 90, ""},
			{"SRV_REQ.PAY.MAX", "最大耗时", "max_latency", "ms", "max", 800, 80, 90, ""},
			{"SRV_REQ.PAY.AVG", "平均耗时", "avg_latency", "ms", "avg", 160, 70, 85, "SRV_REQ.PAY.COUNT"},
		},
	}
	// 汇总指标: 主统计模块=count(请求量), 绝对值阈值演示日值告警联动(工作日请求量≈35万+)
	aggWarn, aggDanger := 330000.0, 365000.0
	agg := CapacityMetric{
		MetricCode: "SRV_REQ", Name: "服务请求", SystemCode: sys.SystemCode,
		MetricKind: "aggregate", Category: "middleware", Unit: "-",
		MainModule: "count", JudgeDirection: "higher_worse", ThresholdMode: "absolute",
		WarnLine: &aggWarn, DangerLine: &aggDanger,
		Remark: "汇总指标演示: 订单/支付子服务请求的请求量与耗时",
	}
	if err := tx.Create(&agg).Error; err != nil {
		return err
	}
	// 子指标分组
	for groupCode, groupName := range map[string]string{
		"SRV_REQ.ORD": "订单服务请求",
		"SRV_REQ.PAY": "支付服务请求",
	} {
		pc := "SRV_REQ"
		g := CapacityMetric{
			MetricCode: groupCode, Name: groupName, SystemCode: sys.SystemCode,
			MetricKind: "group", ParentCode: &pc, Unit: "-",
		}
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
	}
	// 统计模块(普通指标)
	mods := 0
	for _, ss := range specs {
		for _, sp := range ss {
			// 分组码 = SRV_REQ.ORD / SRV_REQ.PAY (模块码去掉最后一段)
			pc := sp.Code[:strings.LastIndex(sp.Code, ".")]
			m := CapacityMetric{
				MetricCode: sp.Code, Name: sp.Name, SystemCode: sys.SystemCode,
				MetricKind: "normal", ParentCode: &pc, StatModule: sp.Module,
				MetricType: "absolute", Unit: sp.Unit, StatMode: sp.StatMode,
				JudgeDirection: "higher_worse", Baseline: &sp.Baseline,
				BaselineSource: "manual", ThresholdMode: "percent",
				WarnLine: &sp.Warn, DangerLine: &sp.Danger, Status: "active",
			}
			if sp.Weight != "" {
				wc := sp.Weight
				m.WeightMetricCode = &wc
			}
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
			mods++
		}
	}
	// 演示数据: 近 60 日日数据 + 昨日/今日日内数据(昨日/今日由日内数据推导, 与日汇总口径一致)
	baseByCode := map[string]float64{
		"SRV_REQ.ORD.COUNT": 240000, "SRV_REQ.ORD.MAX": 700, "SRV_REQ.ORD.AVG": 150,
		"SRV_REQ.PAY.COUNT": 95000, "SRV_REQ.PAY.MAX": 450, "SRV_REQ.PAY.AVG": 80,
	}
	statModes := map[string]string{
		"SRV_REQ.ORD.COUNT": "sum", "SRV_REQ.ORD.MAX": "max", "SRV_REQ.ORD.AVG": "avg",
		"SRV_REQ.PAY.COUNT": "sum", "SRV_REQ.PAY.MAX": "max", "SRV_REQ.PAY.AVG": "avg",
	}
	aggDayStat := func(v map[string]float64) float64 {
		// 主统计模块 count: 请求量跨子指标求和
		return v["SRV_REQ.ORD.COUNT"] + v["SRV_REQ.PAY.COUNT"]
	}
	for i := 59; i >= 2; i-- { // 60..3 天前(今日/昨日单独处理)
		day := today.AddDate(0, 0, -i)
		growth := 1 + (59-float64(i))*0.0012
		weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
		dayVals := map[string]float64{}
		for code, base := range baseByCode {
			v := base * growth * (1 + 0.18*(rnd.Float64()-0.5))
			if weekend {
				v *= 0.55
			}
			dayVals[code] = v
			row := demoDayRow(code, day, v, statModes[code], rnd)
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		// 汇总指标历史日值(count 模块求和; 日汇总运行后按主模块口径覆盖刷新)
		av := aggDayStat(dayVals)
		aggRow := MetricValueDay{
			MetricCode: "SRV_REQ", BizDate: day, StatValue: &av,
			Level: "normal", PointCount: 2, Source: "aggregate",
		}
		if err := tx.Create(&aggRow).Error; err != nil {
			return err
		}
	}
	// 昨日/今日日内数据
	for dayOff := 1; dayOff >= 0; dayOff-- {
		day := today.AddDate(0, 0, -dayOff)
		dayStat := map[string]float64{}
		for code, base := range baseByCode {
			var pts []float64
			hours := 24
			if dayOff == 0 {
				hours = time.Now().Hour() + 1
				if hours < 4 {
					hours = 4
				}
			}
			for h := 0; h < hours; h++ {
				peak := 1.0 + 0.7*math.Sin(float64(h)/24*2*math.Pi) // 白天高峰
				var v float64
				switch statModes[code] {
				case "max":
					v = base * (0.45 + 0.55*rnd.Float64()) * (0.8 + 0.2*peak)
				case "avg":
					v = base * (0.6 + 0.7*rnd.Float64()) * (0.85 + 0.15*peak)
				default: // sum: 逐点分摊, 全日合计约 base
					v = base / 24 * peak * (1 + 0.3*(rnd.Float64()-0.5))
				}
				if v < 0 {
					v = 0
				}
				pts = append(pts, v)
			}
			for h, v := range pts {
				rt := MetricValueRT{
					MetricCode: code,
					Ts:         day.Add(time.Duration(h) * time.Hour),
					RawValue:   &v, CleanValue: &v,
					JudgeLevel: "normal", Source: "collect",
				}
				if err := tx.Create(&rt).Error; err != nil {
					return err
				}
			}
			dayRow := demoDayRowFromPoints(code, day, pts, statModes[code])
			if err := tx.Create(&dayRow).Error; err != nil {
				return err
			}
			dayStat[code] = *dayRow.StatValue
		}
		// 汇总指标日值(count 求和); 昨日日值随后由日汇总按阈值口径刷新并联动告警
		av := aggDayStat(dayStat)
		aggRow := MetricValueDay{
			MetricCode: "SRV_REQ", BizDate: day, StatValue: &av,
			Level: "normal", PointCount: 2, Source: "aggregate",
		}
		if err := tx.Create(&aggRow).Error; err != nil {
			return err
		}
	}
	return tx.Commit().Error
}

func demoDayRow(code string, day time.Time, v float64, statMode string, rnd *rand.Rand) MetricValueDay {
	var dayMax, dayMin, dayAvg, dayLast float64
	switch statMode {
	case "max":
		dayMax = v
		dayMin = v * (0.6 + 0.2*rnd.Float64())
		dayAvg = v * (0.35 + 0.15*rnd.Float64())
		dayLast = v * (0.4 + 0.3*rnd.Float64())
	case "avg":
		dayAvg = v
		dayMax = v * (2.2 + 1.2*rnd.Float64())
		dayMin = v * (0.55 + 0.2*rnd.Float64())
		dayLast = v * (0.7 + 0.5*rnd.Float64())
	default: // sum: 日内逐点
		dayAvg = v
		dayMax = v * (1.02 + 0.05*rnd.Float64())
		dayMin = v * 0.95
		dayLast = v
	}
	stat := v
	if statMode == "max" {
		stat = dayMax
	}
	return MetricValueDay{
		MetricCode: code, BizDate: day,
		DayMax: &dayMax, DayMin: &dayMin, DayAvg: &dayAvg, DayLast: &dayLast,
		StatValue: &stat, Level: "normal", PointCount: 96, Source: "collect",
	}
}

func demoDayRowFromPoints(code string, day time.Time, pts []float64, statMode string) MetricValueDay {
	var sum, mx, mn, last float64
	for i, v := range pts {
		if i == 0 || v > mx {
			mx = v
		}
		if i == 0 || v < mn {
			mn = v
		}
		sum += v
		last = v
	}
	n := float64(len(pts))
	avg := sum / n
	stat := last
	switch statMode {
	case "sum":
		stat = sum
	case "max":
		stat = mx
	case "avg":
		stat = avg
	}
	return MetricValueDay{
		MetricCode: code, BizDate: day,
		DayMax: &mx, DayMin: &mn, DayAvg: &avg, DayLast: &last,
		StatValue: &stat, Level: "normal", PointCount: len(pts), Source: "collect",
	}
}
