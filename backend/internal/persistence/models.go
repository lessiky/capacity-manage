package persistence

import "time"

// ---------- 系统域 ----------

type SysUser struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	Username      string     `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash  string     `gorm:"size:128" json:"-"`
	RealName      string     `gorm:"size:64" json:"realName"`
	Dept          string     `gorm:"size:128" json:"dept"`
	Email         string     `gorm:"size:128" json:"email"`
	Phone         string     `gorm:"size:32" json:"phone"`
	Status        string     `gorm:"size:16;default:active" json:"status"`
	MustChangePwd bool       `json:"mustChangePwd"`
	LastLoginAt   *time.Time `json:"lastLoginAt"`
	LastLoginIP   string     `gorm:"size:64" json:"-"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type SysRole struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Code      string    `gorm:"uniqueIndex;size:64" json:"code"`
	Name      string    `gorm:"size:128" json:"name"`
	Builtin   bool      `json:"builtin"`
	Status    string    `gorm:"size:16;default:active" json:"status"`
	Remark    string    `gorm:"size:256" json:"remark"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SysPermission struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Code      string    `gorm:"uniqueIndex;size:128" json:"code"`
	Name      string    `gorm:"size:128" json:"name"`
	Type      string    `gorm:"size:16" json:"type"` // menu/button/api
	Path      string    `gorm:"size:256" json:"path"`
	ParentID  *int64    `json:"parentId"`
	Sort      int       `json:"sort"`
	CreatedAt time.Time `json:"createdAt"`
}

type SysUserRole struct {
	UserID int64 `gorm:"primaryKey" json:"userId"`
	RoleID int64 `gorm:"primaryKey" json:"roleId"`
}

type SysRolePermission struct {
	RoleID       int64 `gorm:"primaryKey" json:"roleId"`
	PermissionID int64 `gorm:"primaryKey" json:"permissionId"`
}

type SysDataScope struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	UserID     *int64    `json:"userId"`
	RoleID     *int64    `json:"roleId"`
	SystemCode string    `gorm:"size:64" json:"systemCode"`
	ScopeType  string    `gorm:"size:16" json:"scopeType"` // manage/view
	CreatedAt  time.Time `json:"createdAt"`
}

type SysOperateLog struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    *int64    `json:"userId"`
	Username  string    `gorm:"size:64" json:"username"`
	Module    string    `gorm:"size:64" json:"module"`
	Action    string    `gorm:"size:64" json:"action"`
	Target    string    `gorm:"size:256" json:"target"`
	Detail    string    `gorm:"type:jsonb" json:"detail"`
	IP        string    `gorm:"size:64" json:"ip"`
	CreatedAt time.Time `gorm:"index" json:"createdAt"`
}

type SysParam struct {
	Key         string    `gorm:"primaryKey;size:128" json:"key"`
	Value       string    `gorm:"type:text" json:"value"`
	Description string    `gorm:"size:256" json:"description"`
	Category    string    `gorm:"size:32;default:general" json:"category"`
	UpdatedBy   string    `gorm:"size:64" json:"updatedBy"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ---------- 业务系统域 ----------

type BusinessSystem struct {
	SystemCode     string     `gorm:"primaryKey;size:64" json:"systemCode"`
	Name           string     `gorm:"uniqueIndex;size:128" json:"name"`
	Dept           string     `gorm:"size:128" json:"dept"`
	Owner          string     `gorm:"size:64" json:"owner"`
	Level          string     `gorm:"size:16;default:general" json:"level"`
	Status         string     `gorm:"size:16;default:normal" json:"status"`
	OnlineDate     *time.Time `gorm:"type:date" json:"onlineDate"`
	SilentMaintain bool       `gorm:"default:true" json:"silentMaintain"`
	Remark         string     `gorm:"size:512" json:"remark"`
	CreatedBy      string     `gorm:"size:64" json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type BusinessSystemResource struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	SystemCode string    `gorm:"size:64;index" json:"systemCode"`
	ResType    string    `gorm:"size:16" json:"resType"` // server/db/middleware/storage/network
	Name       string    `gorm:"size:128" json:"name"`
	Address    string    `gorm:"size:256" json:"address"`
	Remark     string    `gorm:"size:256" json:"remark"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ---------- 交易日域 ----------

type TradingDayCalendar struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Market    string    `gorm:"size:16;default:SH" json:"market"`
	CalDate   time.Time `gorm:"type:date;uniqueIndex:uk_trading_calendar" json:"calDate"`
	IsTrading bool      `json:"isTrading"`
	DayType   string    `gorm:"size:16" json:"dayType"` // trading/weekend/holiday/makeup
	Source    string    `gorm:"size:16;default:manual" json:"source"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ---------- 指标与阈值域 ----------

type CapacityMetric struct {
	MetricCode       string    `gorm:"primaryKey;size:64" json:"metricCode"`
	Name             string    `gorm:"size:128" json:"name"`
	SystemCode       string    `gorm:"size:64;index" json:"systemCode"`
	Category         string    `gorm:"size:32" json:"category"`
	MetricKind       string    `gorm:"size:16;default:normal" json:"metricKind"` // normal/group/aggregate
	ParentCode       *string   `gorm:"size:64;index" json:"parentCode"`
	StatModule       string    `gorm:"size:32" json:"statModule"`
	MainModule       string    `gorm:"size:32" json:"mainModule"`
	WeightMetricCode *string   `gorm:"size:64;index" json:"weightMetricCode"`
	MetricType       string    `gorm:"size:16" json:"metricType"` // usage/absolute/increment
	Unit             string    `gorm:"size:32" json:"unit"`
	DsType           string    `gorm:"size:16" json:"dsType"`
	CollectMethod    string    `gorm:"size:16" json:"collectMethod"`
	StatMode         string    `gorm:"size:16;default:latest" json:"statMode"`
	JudgeDirection   string    `gorm:"size:16;default:higher_worse" json:"judgeDirection"`
	Baseline         *float64  `gorm:"type:numeric(20,6)" json:"baseline"`
	BaselineSource   string    `gorm:"size:16;default:manual" json:"baselineSource"`
	ThresholdMode    string    `gorm:"size:16;default:percent" json:"thresholdMode"`
	WarnLine         *float64  `gorm:"type:numeric(20,6)" json:"warnLine"`
	DangerLine       *float64  `gorm:"type:numeric(20,6)" json:"dangerLine"`
	Status           string    `gorm:"size:16;default:active;index" json:"status"`
	Remark           string    `gorm:"size:512" json:"remark"`
	CreatedBy        string    `gorm:"size:64" json:"createdBy"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type ThresholdHistory struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	MetricCode  string    `gorm:"size:64;index" json:"metricCode"`
	ChangedAt   time.Time `gorm:"index" json:"changedAt"`
	Operator    string    `gorm:"size:64" json:"operator"`
	BaselineOld *float64  `gorm:"type:numeric(20,6)" json:"baselineOld"`
	BaselineNew *float64  `gorm:"type:numeric(20,6)" json:"baselineNew"`
	WarnOld     *float64  `gorm:"type:numeric(20,6)" json:"warnOld"`
	WarnNew     *float64  `gorm:"type:numeric(20,6)" json:"warnNew"`
	DangerOld   *float64  `gorm:"type:numeric(20,6)" json:"dangerOld"`
	DangerNew   *float64  `gorm:"type:numeric(20,6)" json:"dangerNew"`
	Reason      string    `gorm:"size:512" json:"reason"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ---------- 数据源域 ----------

type DataSourceCategory struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	SystemCode string    `gorm:"size:64;index" json:"systemCode"` // 归属业务系统(必填)
	Name       string    `gorm:"size:128" json:"name"`            // 系统内唯一
	Remark     string    `gorm:"size:512" json:"remark"`
	Sort       int       `json:"sort"`
	CreatedBy  string    `gorm:"size:64" json:"createdBy"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type DataSource struct {
	ID               int64      `gorm:"primaryKey" json:"id"`
	Name             string     `gorm:"uniqueIndex;size:128" json:"name"`
	Type             string     `gorm:"size:16" json:"type"` // db/log
	Status           string     `gorm:"size:16;default:available" json:"status"`
	Config           string     `gorm:"type:jsonb" json:"config"`        // 含 password_enc
	SystemCode       string     `gorm:"size:64;index" json:"systemCode"` // 归属业务系统(必填)
	CategoryID       *int64     `gorm:"index" json:"categoryId"`         // 归属分类(须为该系统下, 必填)
	HealthStatus     string     `gorm:"size:16;default:unknown" json:"healthStatus"`
	HealthFailStreak int        `gorm:"default:0" json:"healthFailStreak"` // 连续健康检查失败次数
	LastSuccessAt    *time.Time `json:"lastSuccessAt"`
	LastError        string     `gorm:"size:1024" json:"lastError"`
	CreatedBy        string     `gorm:"size:64" json:"createdBy"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// ---------- 流程域 ----------

type PythonScript struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:128;uniqueIndex:uk_python_script_name" json:"name"`
	ScriptType  string    `gorm:"size:16;uniqueIndex:uk_python_script_name" json:"scriptType"`
	Language    string    `gorm:"size:16;default:python" json:"language"` // python/bash/powershell
	Description string    `gorm:"size:512" json:"description"`
	Status      string    `gorm:"size:16;default:active" json:"status"`
	CreatedBy   string    `gorm:"size:64" json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type PythonScriptVersion struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	ScriptID    int64      `gorm:"index" json:"scriptId"`
	Version     int        `json:"version"`
	Content     string     `gorm:"type:text" json:"content"`
	Checksum    string     `gorm:"size:64" json:"-"`
	Active      bool       `json:"active"`
	FileRef     string     `gorm:"size:256" json:"fileRef"`
	PublishedBy string     `gorm:"size:64" json:"publishedBy"`
	PublishedAt *time.Time `json:"publishedAt"`
	Remark      string     `gorm:"size:512" json:"remark"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type CollectFlow struct {
	ID                   int64     `gorm:"primaryKey" json:"id"`
	FlowCode             string    `gorm:"uniqueIndex;size:64" json:"flowCode"`
	Name                 string    `gorm:"size:128" json:"name"`
	MetricCode           string    `gorm:"size:64;uniqueIndex" json:"metricCode"`
	DataSourceID         *int64    `gorm:"index" json:"dataSourceId"`         // 数据源(与 DataSourceCategoryID 二选一)
	DataSourceCategoryID *int64    `gorm:"index" json:"dataSourceCategoryId"` // 数据源分类(logparse: 分类下全部日志数据源共用同一处理逻辑)
	CollectType          string    `gorm:"size:16" json:"collectType"`        // sql/logparse/python
	CollectConfig        string    `gorm:"type:jsonb" json:"collectConfig"`
	CleanConfig          string    `gorm:"type:jsonb;default:'{}'" json:"cleanConfig"`
	ScheduleMode         string    `gorm:"size:16;default:normal" json:"scheduleMode"`
	CronExpr             string    `gorm:"size:64" json:"cronExpr"`
	TradingMarket        string    `gorm:"size:16" json:"tradingMarket"`
	WindowTime           string    `gorm:"size:16" json:"windowTime"`
	BizDateMode          string    `gorm:"size:16;default:natural" json:"bizDateMode"`
	Version              int       `gorm:"default:1" json:"version"`
	Status               string    `gorm:"size:16;default:created;index" json:"status"`
	Enabled              bool      `gorm:"default:true" json:"enabled"`
	Remark               string    `gorm:"size:512" json:"remark"`
	CreatedBy            string    `gorm:"size:64" json:"createdBy"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type CollectTaskLog struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	FlowID         int64      `gorm:"index" json:"flowId"`
	FlowCode       string     `gorm:"size:64" json:"flowCode"`
	MetricCode     string     `gorm:"size:64" json:"metricCode"`
	TriggeredAt    time.Time  `gorm:"index" json:"triggeredAt"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	Status         string     `gorm:"size:16;index;default:running" json:"status"`
	DurationMS     *int64     `json:"durationMs"`
	RowsCollected  *int       `json:"rowsCollected"`
	ParseFailCount int        `json:"parseFailCount"`
	ParseFailRate  *float64   `gorm:"type:numeric(7,4)" json:"parseFailRate"`
	AnomalyCount   int        `json:"anomalyCount"`
	ErrMsg         string     `gorm:"column:error_msg;size:2048" json:"errorMsg"`
	RetryCount     int        `json:"retryCount"`
	RunLog         string     `gorm:"type:text" json:"runLog,omitempty"`
	Source         string     `gorm:"size:16;default:auto" json:"source"`
	Operator       string     `gorm:"size:64" json:"operator"`
	CreatedAt      time.Time  `json:"createdAt"`
}

type CleaningTrace struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	TaskLogID     int64     `gorm:"index" json:"taskLogId"`
	MetricCode    string    `gorm:"size:64" json:"metricCode"`
	RawValue      string    `gorm:"size:256" json:"rawValue"`
	CleanValue    *float64  `gorm:"type:numeric(24,8)" json:"cleanValue"`
	RulesApplied  string    `gorm:"type:jsonb" json:"rulesApplied"`
	IsAnomaly     bool      `json:"isAnomaly"`
	AnomalyReason string    `gorm:"size:256" json:"anomalyReason"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ---------- 数据域 ----------

type MetricValueRT struct {
	MetricCode    string    `gorm:"size:64;primaryKey" json:"metricCode"`
	Ts            time.Time `gorm:"primaryKey" json:"ts"`
	RawValue      *float64  `gorm:"type:numeric(24,8)" json:"rawValue"`
	CleanValue    *float64  `gorm:"type:numeric(24,8)" json:"cleanValue"`
	IsAnomaly     bool      `json:"isAnomaly"`
	AnomalyReason string    `gorm:"size:256" json:"anomalyReason"`
	JudgeLevel    string    `gorm:"size:16;default:normal" json:"judgeLevel"`
	Source        string    `gorm:"size:16;default:collect" json:"source"`
	CreatedAt     time.Time `json:"createdAt"`
}

// MetricPeakStat 指标历史峰值/谷值记录（按日数据 stat_value 口径）:
// 日汇总/手工修正后自动维护; 指标历史日数据被删除时记录保留, 用于"历史峰值"展示
type MetricPeakStat struct {
	MetricCode string     `gorm:"size:64;primaryKey" json:"metricCode"`
	MaxValue   *float64   `gorm:"type:numeric(24,8)" json:"maxValue"`
	MaxBizDate *time.Time `gorm:"type:date" json:"maxBizDate"`
	MinValue   *float64   `gorm:"type:numeric(24,8)" json:"minValue"`
	MinBizDate *time.Time `gorm:"type:date" json:"minBizDate"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type MetricValueDay struct {
	MetricCode       string     `gorm:"size:64;primaryKey" json:"metricCode"`
	BizDate          time.Time  `gorm:"type:date;primaryKey" json:"bizDate"`
	DayMax           *float64   `gorm:"type:numeric(24,8)" json:"dayMax"`
	DayMin           *float64   `gorm:"type:numeric(24,8)" json:"dayMin"`
	DayAvg           *float64   `gorm:"type:numeric(24,8)" json:"dayAvg"`
	DayLast          *float64   `gorm:"type:numeric(24,8)" json:"dayLast"`
	StatValue        *float64   `gorm:"type:numeric(24,8)" json:"statValue"`
	WaterLevel       *float64   `gorm:"type:numeric(9,4)" json:"waterLevel"`
	Level            string     `gorm:"size:16;default:normal" json:"level"`
	PointCount       int        `json:"pointCount"`
	Source           string     `gorm:"size:16;default:collect" json:"source"`
	Corrected        bool       `json:"corrected"`
	CorrectReason    string     `gorm:"size:512" json:"correctReason"`
	CorrectedBy      string     `gorm:"size:64" json:"correctedBy"`
	CorrectedAt      *time.Time `json:"correctedAt"`
	OriginalSnapshot *string    `gorm:"type:jsonb" json:"originalSnapshot"` // jsonb: 指针保证零值落 NULL(空串非法 JSON)
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type DailySummaryLog struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	BizDate        time.Time  `gorm:"type:date;uniqueIndex" json:"bizDate"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	Status         string     `gorm:"size:16;default:running" json:"status"`
	MetricsTotal   *int       `json:"metricsTotal"`
	MetricsDone    *int       `json:"metricsDone"`
	MetricsMissing int        `json:"metricsMissing"`
	ErrMsg         string     `gorm:"column:error_msg;size:2048" json:"errorMsg"`
	CreatedAt      time.Time  `json:"createdAt"`
}

type DataCorrection struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	MetricCode  string    `gorm:"size:64;index" json:"metricCode"`
	BizDate     time.Time `gorm:"type:date" json:"bizDate"`
	Field       string    `gorm:"size:32" json:"field"`
	OldValue    *float64  `gorm:"type:numeric(24,8)" json:"oldValue"`
	NewValue    *float64  `gorm:"type:numeric(24,8)" json:"newValue"`
	Reason      string    `gorm:"size:512" json:"reason"`
	Operator    string    `gorm:"size:64" json:"operator"`
	RefreshHint string    `gorm:"size:256" json:"refreshHint"`
	CreatedAt   time.Time `json:"createdAt"`
}

// MetricQualityDay 按指标/业务日保存质量分和规则标记，供预测质量门禁与报表解释读取。
type MetricQualityDay struct {
	MetricCode    string    `gorm:"primaryKey;size:64" json:"metricCode"`
	BizDate       time.Time `gorm:"primaryKey;type:date" json:"bizDate"`
	SystemCode    string    `gorm:"size:64;index" json:"systemCode"`
	Score         float64   `gorm:"type:numeric(6,4)" json:"score"`
	Completeness  float64   `gorm:"type:numeric(6,4)" json:"completeness"`
	Timeliness    float64   `gorm:"type:numeric(6,4)" json:"timeliness"`
	AnomalyRate   float64   `gorm:"type:numeric(6,4)" json:"anomalyRate"`
	DuplicateRate float64   `gorm:"type:numeric(6,4)" json:"duplicateRate"`
	Freshness     float64   `gorm:"type:numeric(6,4)" json:"freshness"`
	ValidPoints   int       `json:"validPoints"`
	TotalPoints   int       `json:"totalPoints"`
	Flags         string    `gorm:"type:text" json:"flags"`
	EvaluatedAt   time.Time `json:"evaluatedAt"`
}

type DataBackfillRun struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	FlowID      int64      `gorm:"index" json:"flowId"`
	MetricCode  string     `gorm:"size:64;index" json:"metricCode"`
	FromDate    time.Time  `gorm:"type:date" json:"fromDate"`
	ToDate      time.Time  `gorm:"type:date" json:"toDate"`
	Source      string     `gorm:"size:16;default:backfill" json:"source"`
	Status      string     `gorm:"size:16;default:running;index" json:"status"`
	TotalDays   int        `json:"totalDays"`
	SuccessDays int        `json:"successDays"`
	FailedDays  int        `json:"failedDays"`
	Error       string     `gorm:"column:error;size:1024" json:"error"`
	CreatedBy   string     `gorm:"size:64" json:"createdBy"`
	StartedAt   *time.Time `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type DataBackfillItem struct {
	ID                int64     `gorm:"primaryKey" json:"id"`
	RunID             int64     `gorm:"index" json:"runId"`
	BizDate           time.Time `gorm:"type:date;index" json:"bizDate"`
	Status            string    `gorm:"size:16;index" json:"status"`
	Error             string    `gorm:"column:error;size:1024" json:"error"`
	SummaryRefreshed  bool      `json:"summaryRefreshed"`
	QualityRefreshed  bool      `json:"qualityRefreshed"`
	AlertReplayed     bool      `json:"alertReplayed"`
	ReportInvalidated bool      `json:"reportInvalidated"`
	CreatedAt         time.Time `json:"createdAt"`
}

// ---------- 告警域 ----------

type AlertRecord struct {
	ID                 int64      `gorm:"primaryKey" json:"id"`
	AlertNo            string     `gorm:"uniqueIndex;size:32" json:"alertNo"`
	MetricCode         string     `gorm:"size:64;index" json:"metricCode"`
	SystemCode         string     `gorm:"size:64;index" json:"systemCode"`
	Level              string     `gorm:"size:8;index" json:"level"` // p1/p2/p3/p4
	AlertType          string     `gorm:"size:16" json:"alertType"`
	Status             string     `gorm:"size:16;index;default:active" json:"status"`
	TriggerAt          time.Time  `gorm:"index" json:"triggerAt"`
	LastValueAt        *time.Time `json:"lastValueAt"`
	FirstValue         *float64   `gorm:"type:numeric(24,8)" json:"firstValue"`
	LastValue          *float64   `gorm:"type:numeric(24,8)" json:"lastValue"`
	Threshold          *float64   `gorm:"type:numeric(24,8)" json:"threshold"`
	WaterLevel         *float64   `gorm:"type:numeric(9,4)" json:"waterLevel"`
	DurationMin        *int       `json:"durationMin"`
	DeadlineAt         *time.Time `json:"deadlineAt"`
	RecoveredAt        *time.Time `json:"recoveredAt"`
	ClaimedBy          string     `gorm:"size:64" json:"claimedBy"`
	ClaimedAt          *time.Time `json:"claimedAt"`
	HandleNote         string     `gorm:"type:jsonb" json:"handleNote"` // [{time,user,note}]
	ClosedBy           string     `gorm:"size:64" json:"closedBy"`
	ClosedAt           *time.Time `json:"closedAt"`
	CloseResult        string     `gorm:"size:1024" json:"closeResult"`
	Silent             bool       `json:"silent"`
	SourceLine         string     `gorm:"size:16" json:"sourceLine"` // warn/danger
	EscalationPolicyID *int64     `gorm:"index" json:"escalationPolicyId"`
	EscalatedAt        *time.Time `json:"escalatedAt"`
	EscalationCount    int        `gorm:"default:0" json:"escalationCount"`
	EvidenceWindow     *string    `gorm:"type:jsonb" json:"evidenceWindow"`
	Detail             *string    `gorm:"type:jsonb" json:"detail"` // jsonb: 指针保证零值落 NULL(空串非法 JSON)
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type AlertEscalationPolicy struct {
	ID              int64  `gorm:"primaryKey" json:"id"`
	Name            string `gorm:"uniqueIndex;size:128" json:"name"`
	SourceLine      string `gorm:"size:16" json:"sourceLine"` // warn/danger
	TargetLevel     string `gorm:"size:8" json:"targetLevel"` // p2/p1
	Mode            string `gorm:"size:16" json:"mode"`       // consecutive/window_count
	ConsecutiveDays int    `json:"consecutiveDays,omitempty"`
	WindowDays      int    `json:"windowDays,omitempty"`
	HitDays         int    `json:"hitDays,omitempty"`
	DayBasis        string `gorm:"size:16;default:natural" json:"dayBasis"`
	ScopeType       string `gorm:"size:16;default:global;index" json:"scopeType"` // global/system/metric
	// ScopeValue: global=空数组; system=系统编码集合; metric="系统.指标"集合（jsonb 数组，支持多选）
	ScopeValue StringList `gorm:"type:jsonb" json:"scopeValue"`
	Enabled    bool       `gorm:"default:true;index" json:"enabled"`
	Priority   int        `gorm:"default:100" json:"priority"`
	CreatedBy  string     `gorm:"size:64" json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type AlertDailyHit struct {
	MetricCode string    `gorm:"primaryKey;size:64" json:"metricCode"`
	BizDate    time.Time `gorm:"primaryKey;type:date" json:"bizDate"`
	SystemCode string    `gorm:"size:64;index" json:"systemCode"`
	WarnHit    bool      `json:"warnHit"`
	DangerHit  bool      `json:"dangerHit"`
	DataState  string    `gorm:"size:16" json:"dataState"` // valid/missing/anomaly
	Source     string    `gorm:"size:16;default:collect" json:"source"`
	ComputedAt time.Time `json:"computedAt"`
}

type AlertLevelHistory struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	AlertID   int64     `gorm:"index" json:"alertId"`
	FromLevel string    `gorm:"size:8" json:"fromLevel"`
	ToLevel   string    `gorm:"size:8" json:"toLevel"`
	Action    string    `gorm:"size:24;default:escalate;index" json:"action"` // escalate/downgrade_suggestion/manual_downgrade
	Reason    string    `gorm:"size:256" json:"reason"`
	Evidence  string    `gorm:"type:jsonb" json:"evidence"`
	PolicyID  *int64    `json:"policyId"`
	Operator  string    `gorm:"size:64;default:system" json:"operator"`
	ChangedAt time.Time `json:"changedAt"`
}

// ---------- 通知域 ----------

type NotificationChannel struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	Name         string     `gorm:"uniqueIndex;size:128" json:"name"`
	Type         string     `gorm:"size:24;index" json:"type"` // smtp/wecom/dingtalk/feishu/webhook
	Config       string     `gorm:"type:text" json:"-"`        // JSON，敏感配置由应用层 AES-GCM 加密
	Enabled      bool       `gorm:"default:true;index" json:"enabled"`
	HealthStatus string     `gorm:"size:16;default:unknown" json:"healthStatus"`
	LastError    string     `gorm:"size:1024" json:"lastError"`
	LastTestAt   *time.Time `json:"lastTestAt"`
	CreatedBy    string     `gorm:"size:64" json:"createdBy"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type NotificationTemplate struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:128" json:"name"`
	Event       string    `gorm:"size:24;index:uk_notification_template_event" json:"event"`       // alert/recovered/escalate/report
	ChannelType string    `gorm:"size:24;index:uk_notification_template_event" json:"channelType"` // * or concrete channel
	Format      string    `gorm:"size:16;default:text" json:"format"`                              // text/markdown/card
	Subject     string    `gorm:"size:256" json:"subject"`
	Body        string    `gorm:"type:text" json:"body"`
	Variables   string    `gorm:"type:text" json:"variables"`
	Version     int       `gorm:"default:1" json:"version"`
	Enabled     bool      `gorm:"default:true;index" json:"enabled"`
	CreatedBy   string    `gorm:"size:64" json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type NotificationSubscription struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	Name           string    `gorm:"uniqueIndex;size:128" json:"name"`
	ScopeType      string    `gorm:"size:16;default:global;index" json:"scopeType"` // global/system/metric
	ScopeValue     string    `gorm:"size:128;index" json:"scopeValue"`
	Levels         string    `gorm:"size:32" json:"levels"`       // CSV p1,p2...
	AlertTypes     string    `gorm:"size=256" json:"alertTypes"`  // CSV or *
	Recipients     string    `gorm:"type:text" json:"recipients"` // JSON array
	ChannelIDs     string    `gorm:"type:text" json:"channelIds"` // JSON array
	SilentStart    string    `gorm:"size:8" json:"silentStart"`
	SilentEnd      string    `gorm:"size:8" json:"silentEnd"`
	DedupWindowMin int       `gorm:"default:60" json:"dedupWindowMin"`
	Enabled        bool      `gorm:"default:true;index" json:"enabled"`
	CreatedBy      string    `gorm:"size:64" json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type NotificationSilence struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	ScopeType  string    `gorm:"size:16;index" json:"scopeType"` // global/system/metric
	ScopeValue string    `gorm:"size:128;index" json:"scopeValue"`
	StartAt    time.Time `gorm:"index" json:"startAt"`
	EndAt      time.Time `gorm:"index" json:"endAt"`
	Reason     string    `gorm:"size:512" json:"reason"`
	CreatedBy  string    `gorm:"size:64" json:"createdBy"`
	CreatedAt  time.Time `json:"createdAt"`
}

type AlertNotificationLog struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	AlertID     int64      `gorm:"index" json:"alertId"`
	AlertNo     string     `gorm:"size:32;index" json:"alertNo"`
	ChannelID   int64      `gorm:"index" json:"channelId"`
	ChannelType string     `gorm:"size:24" json:"channelType"`
	Target      string     `gorm:"size:1024" json:"target"`
	Level       string     `gorm:"size:8;index" json:"level"`
	Event       string     `gorm:"size:24;index" json:"event"`
	TemplateID  *int64     `json:"templateId"`
	TemplateVer int        `json:"templateVersion"`
	DedupeKey   string     `gorm:"uniqueIndex;size:256" json:"dedupeKey"`
	Status      string     `gorm:"size:16;index" json:"status"` // queued/sent/retrying/failed/suppressed
	Attempts    int        `gorm:"default:0" json:"attempts"`
	MaxAttempts int        `gorm:"default:4" json:"maxAttempts"`
	NextRetryAt *time.Time `gorm:"index" json:"nextRetryAt"`
	Payload     string     `gorm:"type:text" json:"payload"`
	Error       string     `gorm:"column:error;size:1024" json:"error"`
	SentAt      *time.Time `json:"sentAt"`
	CreatedAt   time.Time  `gorm:"index" json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// ---------- 报表 / AI 域 ----------

type ReportResult struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	ReportType   string    `gorm:"size:32;uniqueIndex:uk_report_result" json:"reportType"`
	BizDate      time.Time `gorm:"type:date;uniqueIndex:uk_report_result" json:"bizDate"`
	Status       string    `gorm:"size:16;default:running" json:"status"`
	FileRef      string    `gorm:"size:256" json:"fileRef"`
	MetricsTotal *int      `json:"metricsTotal"`
	MetricsOver  *int      `json:"metricsOver"`
	ErrMsg       string    `gorm:"column:error_msg;size:1024" json:"errorMsg"`
	GeneratedAt  time.Time `json:"generatedAt"`
}

// ReportDefinition 报表中心模板（周报/月报/容量分析/告警汇总/预测/日报）。
type ReportDefinition struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	Name            string    `gorm:"size:128;uniqueIndex:uk_report_definition_name" json:"name"`
	ReportType      string    `gorm:"size:32;index" json:"reportType"`
	SystemScope     string    `gorm:"type:jsonb;default:'[]'" json:"systemScope"`
	MetricScope     string    `gorm:"type:jsonb;default:'[]'" json:"metricScope"`
	GroupDimension  string    `gorm:"size:32" json:"groupDimension"`
	SortBy          string    `gorm:"size:32" json:"sortBy"`
	SortDesc        bool      `gorm:"default:true" json:"sortDesc"`
	IncludeForecast bool      `gorm:"default:false" json:"includeForecast"`
	PeriodWindow    int       `gorm:"default:30" json:"periodWindow"`
	ChartOptions    string    `gorm:"type:jsonb;default:'{}'" json:"chartOptions"`
	Enabled         bool      `gorm:"default:true" json:"enabled"`
	CreatedBy       string    `gorm:"size:64" json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// ReportRun 一次报表生成运行，文件路径分别保存 Excel/PDF。
type ReportRun struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	DefinitionID int64      `gorm:"index" json:"definitionId"`
	RunDate      time.Time  `gorm:"type:date;index" json:"runDate"`
	PeriodStart  time.Time  `gorm:"type:date" json:"periodStart"`
	PeriodEnd    time.Time  `gorm:"type:date" json:"periodEnd"`
	Status       string     `gorm:"size:16;index;default:running" json:"status"`
	MetricsTotal int        `json:"metricsTotal"`
	Files        string     `gorm:"type:jsonb;default:'{}'" json:"files"`
	ErrorMsg     string     `gorm:"column:error_msg;size:2048" json:"errorMsg"`
	GeneratedBy  string     `gorm:"size:64" json:"generatedBy"`
	StartedAt    *time.Time `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// ReportSubscription 将报表定义按固定时间推送到一个或多个通知渠道。
type ReportSubscription struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	Name         string     `gorm:"size:128;index" json:"name"`
	DefinitionID int64      `gorm:"index" json:"definitionId"`
	Recipients   string     `gorm:"type:jsonb;default:'[]'" json:"recipients"`
	ChannelIDs   string     `gorm:"type:jsonb;default:'[]'" json:"channelIds"`
	ScheduleType string     `gorm:"size:16;default:daily" json:"scheduleType"`
	ScheduleTime string     `gorm:"size:8;default:08:30" json:"scheduleTime"`
	Weekday      int        `gorm:"default:1" json:"weekday"`
	DayOfMonth   int        `gorm:"default:1" json:"dayOfMonth"`
	Timezone     string     `gorm:"size:64;default:Asia/Shanghai" json:"timezone"`
	Enabled      bool       `gorm:"default:true" json:"enabled"`
	LastRunAt    *time.Time `json:"lastRunAt"`
	NextRunAt    *time.Time `gorm:"index" json:"nextRunAt"`
	CreatedBy    string     `gorm:"size:64" json:"createdBy"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type AICallLog struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Feature      string    `gorm:"size:32" json:"feature"`
	UserID       string    `gorm:"size:64" json:"userId"`
	Model        string    `gorm:"size:128" json:"model"`
	InputDigest  string    `gorm:"size:512" json:"inputDigest"`
	OutputDigest string    `gorm:"size:1024" json:"outputDigest"`
	LatencyMS    *int      `json:"latencyMs"`
	Status       string    `gorm:"size:16" json:"status"`
	ErrMsg       string    `gorm:"column:error_msg;size:1024" json:"errorMsg"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ForecastRun keeps one reproducible prediction batch.
type ForecastRun struct {
	ID             int64      `gorm:"primaryKey" json:"id"`
	RunDate        time.Time  `gorm:"type:date;uniqueIndex:uk_forecast_run_date" json:"runDate"`
	ModelVersion   string     `gorm:"size:32" json:"modelVersion"`
	DataEndDate    time.Time  `gorm:"type:date" json:"dataEndDate"`
	Status         string     `gorm:"size:16;default:running;index" json:"status"`
	MetricsTotal   int        `json:"metricsTotal"`
	MetricsDone    int        `json:"metricsDone"`
	MetricsSkipped int        `json:"metricsSkipped"`
	AIEnabled      bool       `json:"aiEnabled"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	ErrorMsg       string     `gorm:"column:error_msg;size:2048" json:"errorMsg"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// ForecastResult stores a 30/90 day statistical prediction and all inputs
// needed to reproduce it (data end date, quality, model version, digest).
type ForecastResult struct {
	ID                 int64      `gorm:"primaryKey" json:"id"`
	RunID              int64      `gorm:"index;uniqueIndex:uk_forecast_result" json:"runId"`
	MetricCode         string     `gorm:"size:64;index;uniqueIndex:uk_forecast_result" json:"metricCode"`
	HorizonDays        int        `gorm:"uniqueIndex:uk_forecast_result" json:"horizonDays"`
	ModelType          string     `gorm:"size:32" json:"modelType"`
	Status             string     `gorm:"size:24;index" json:"status"`
	ETAWarnDate        *time.Time `gorm:"type:date" json:"etaWarnDate"`
	ETADangerDate      *time.Time `gorm:"type:date" json:"etaDangerDate"`
	RiskScore          *float64   `gorm:"type:numeric(6,2)" json:"riskScore"`
	Confidence         *float64   `gorm:"type:numeric(5,4)" json:"confidence"`
	Slope              *float64   `gorm:"type:numeric(24,8)" json:"slope"`
	Intercept          *float64   `gorm:"type:numeric(24,8)" json:"intercept"`
	ResidualStd        *float64   `gorm:"column:residual_stddev;type:numeric(24,8)" json:"residualStd"`
	DataQuality        *float64   `gorm:"type:numeric(5,4)" json:"dataQuality"`
	EventFactorVersion string     `gorm:"size:32" json:"eventFactorVersion"`
	Explanation        string     `gorm:"size:2048" json:"explanation"`
	Curve              string     `gorm:"type:jsonb;default:'[]'" json:"curve"`
	InputDigest        string     `gorm:"size:128" json:"inputDigest"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// ForecastEvent 是事件因子（计划 §6.4）。M7 只读取 confirmed 事件计算版本号，
// M8 补齐录入、AI 建议、人工确认、过期与版本追踪。
type ForecastEvent struct {
	ID              int64      `gorm:"primaryKey" json:"id"`
	Name            string     `gorm:"size:128" json:"name"`
	EventType       string     `gorm:"size:32" json:"eventType"`
	ScopeType       string     `gorm:"size:16" json:"scopeType"`
	ScopeValue      string     `gorm:"size:128" json:"scopeValue"`
	StartDate       time.Time  `gorm:"type:date" json:"startDate"`
	EndDate         *time.Time `gorm:"type:date" json:"endDate"`
	ImpactDirection string     `gorm:"size:16" json:"impactDirection"`
	ImpactPct       *float64   `gorm:"type:numeric(8,4)" json:"impactPct"`
	Confidence      *float64   `gorm:"type:numeric(5,4)" json:"confidence"`
	Status          string     `gorm:"size:16;default:draft" json:"status"`
	AISuggestion    string     `gorm:"type:jsonb;default:'{}'::jsonb" json:"aiSuggestion"`
	Description     string     `gorm:"size:1024" json:"description"`
	CreatedBy       string     `gorm:"size:64" json:"createdBy"`
	ConfirmedBy     string     `gorm:"size:64" json:"confirmedBy"`
	ConfirmedAt     *time.Time `json:"confirmedAt"`
	// M8 版本追踪（迁移 0022）
	Version       int        `gorm:"default:1" json:"version"`
	RootID        *int64     `json:"rootId"`
	SupersededBy  *int64     `json:"supersededBy"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	AIModel       string     `gorm:"size:128" json:"aiModel"`
	AIGeneratedAt *time.Time `json:"aiGeneratedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// ForecastAIAdjustment 保存 AI 调节前后结果，便于对比与回滚（FR-AI-05）。
type ForecastAIAdjustment struct {
	ID               int64     `gorm:"primaryKey" json:"id"`
	ResultID         int64     `gorm:"uniqueIndex" json:"resultId"`
	RunID            int64     `gorm:"index" json:"runId"`
	MetricCode       string    `gorm:"size:64;index" json:"metricCode"`
	HorizonDays      int       `json:"horizonDays"`
	AIEnabled        bool      `json:"aiEnabled"`
	Status           string    `gorm:"size:24;index" json:"status"`
	Model            string    `gorm:"size:128" json:"model"`
	SlopeMultiplier  *float64  `gorm:"type:numeric(8,4)" json:"slopeMultiplier"`
	InterceptAdjPct  *float64  `gorm:"type:numeric(8,4)" json:"interceptAdjustmentPct"`
	ConfidenceAdj    *float64  `gorm:"type:numeric(6,4)" json:"confidenceAdjustment"`
	Rationale        string    `gorm:"size:1024" json:"rationale"`
	BaseSnapshot     string    `gorm:"type:jsonb;default:'{}'" json:"baseSnapshot"`
	AdjustedSnapshot string    `gorm:"type:jsonb;default:'{}'" json:"adjustedSnapshot"`
	ETAShiftDays     *int      `json:"etaShiftDays"`
	AdvisoryOnly     bool      `json:"advisoryOnly"`
	LatencyMS        *int      `json:"latencyMs"`
	ErrMsg           string    `gorm:"column:error_msg;size:1024" json:"errorMsg"`
	CreatedAt        time.Time `json:"createdAt"`
}

// APIKey 是开放 API 凭证（迁移 0023）。只存 secret 摘要，明文仅在创建/轮换时返回。
type APIKey struct {
	ID                int64      `gorm:"primaryKey" json:"id"`
	Name              string     `gorm:"size:128" json:"name"`
	KeyID             string     `gorm:"size:32;uniqueIndex" json:"keyId"`
	KeyHash           string     `gorm:"size:128" json:"-"`
	Scopes            string     `gorm:"type:jsonb;default:'[]'" json:"scopes"`
	SystemScope       string     `gorm:"type:jsonb;default:'[]'" json:"systemScope"`
	RateLimit         int        `gorm:"default:300" json:"rateLimit"`
	SignatureRequired bool       `gorm:"default:false" json:"signatureRequired"`
	Status            string     `gorm:"size:16;default:active" json:"status"`
	Remark            string     `gorm:"size:512" json:"remark"`
	CreatedBy         string     `gorm:"size:64" json:"createdBy"`
	LastUsedAt        *time.Time `json:"lastUsedAt"`
	ExpiresAt         *time.Time `json:"expiresAt"`
	CallCount         int64      `json:"callCount"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// APICallLog 是开放 API 调用审计，含被拒绝的越权/超限请求。
type APICallLog struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	KeyID     *int64    `json:"keyId"`
	KeyName   string    `gorm:"size:128" json:"keyName"`
	Method    string    `gorm:"size:8" json:"method"`
	Path      string    `gorm:"size:256" json:"path"`
	Scope     string    `gorm:"size:64" json:"scope"`
	Status    int       `json:"status"`
	Allowed   bool      `json:"allowed"`
	Reason    string    `gorm:"size:128" json:"reason"`
	IP        string    `gorm:"size:64" json:"ip"`
	LatencyMS *int      `json:"latencyMs"`
	CreatedAt time.Time `json:"createdAt"`
}

// MetricArchiveRun 记录一次超期分区压缩归档（FR-DAY-06）。
type MetricArchiveRun struct {
	ID         int64      `gorm:"primaryKey" json:"id"`
	TableName  string     `gorm:"size:64;uniqueIndex:uk_metric_archive_partition" json:"tableName"`
	Partition  string     `gorm:"size:128;uniqueIndex:uk_metric_archive_partition" json:"partition"`
	FromDate   time.Time  `gorm:"type:date" json:"fromDate"`
	ToDate     time.Time  `gorm:"type:date" json:"toDate"`
	RowCount   int64      `json:"rowCount"`
	FileRef    string     `gorm:"size:512" json:"fileRef"`
	Bytes      int64      `json:"bytes"`
	// OnlineDropped 表示归档成功后在线分区已下线(DETACH+DROP)，数据只保留在归档文件。
	OnlineDropped  bool       `gorm:"column:online_dropped;default:false" json:"onlineDropped"`
	DroppedAt      *time.Time `json:"droppedAt"`
	RestoredAt     *time.Time `json:"restoredAt"`
	PayloadVersion int        `gorm:"column:payload_version;default:1" json:"payloadVersion"`
	Status     string     `gorm:"size:16;default:running" json:"status"`
	ErrMsg     string     `gorm:"column:error_msg;size:1024" json:"errorMsg"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// MetricArchiveIndex 是归档明细索引，支持按指标+日期按需恢复。
type MetricArchiveIndex struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	ArchiveID  int64     `gorm:"index" json:"archiveId"`
	MetricCode string    `gorm:"size:64;index:idx_metric_archive_index_lookup" json:"metricCode"`
	BizDate    time.Time `gorm:"type:date;index:idx_metric_archive_index_lookup" json:"bizDate"`
	FileRef    string    `gorm:"size:512" json:"fileRef"`
	ByteOffset int64     `json:"byteOffset"`
	RowBytes   int       `json:"rowBytes"`
	CreatedAt  time.Time `json:"createdAt"`
}

// DerivedMetric 是基于表达式或聚合源指标计算的衍生指标定义（FR-MET-04）。
type DerivedMetric struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	MetricCode    string    `gorm:"size:64;uniqueIndex" json:"metricCode"`
	Name          string    `gorm:"size:128" json:"name"`
	SystemCode    string    `gorm:"size:64" json:"systemCode"`
	Expression    string    `gorm:"type:text" json:"expression"`
	SourceMetrics string    `gorm:"type:jsonb;default:'[]'" json:"sourceMetrics"`
	Unit          string    `gorm:"size:32" json:"unit"`
	Status        string    `gorm:"size:16;default:active" json:"status"`
	CreatedBy     string    `gorm:"size:64" json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ThresholdTemplate 是批量套用的阈值模板（FR-MET-13）。
type ThresholdTemplate struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	Name           string    `gorm:"size:128" json:"name"`
	JudgeDirection string    `gorm:"size:16" json:"judgeDirection"`
	WarnLine       *float64  `gorm:"type:numeric(24,8)" json:"warnLine"`
	DangerLine     *float64  `gorm:"type:numeric(24,8)" json:"dangerLine"`
	WarnRatio      *float64  `gorm:"type:numeric(6,4)" json:"warnRatio"`
	DangerRatio    *float64  `gorm:"type:numeric(6,4)" json:"dangerRatio"`
	Unit           string    `gorm:"size:32" json:"unit"`
	Status         string    `gorm:"size:16;default:active" json:"status"`
	Remark         string    `gorm:"size:512" json:"remark"`
	CreatedBy      string    `gorm:"size:64" json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// MetricImportRun 记录一次指标批量导入（FR-COL-16）。
type MetricImportRun struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	Filename    string     `gorm:"size:256" json:"filename"`
	TotalRows   int        `json:"totalRows"`
	SuccessRows int        `json:"successRows"`
	FailedRows  int        `json:"failedRows"`
	Status      string     `gorm:"size:16;default:running" json:"status"`
	ErrorReport string     `gorm:"type:jsonb;default:'[]'" json:"errorReport"`
	CreatedBy   string     `gorm:"size:64" json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
}

// CalendarCheckRun 是交易日历完整性检查结果（FR-TD-05）。
type CalendarCheckRun struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Year         int       `json:"year"`
	ExpectedDays int       `json:"expectedDays"`
	ActualDays   int       `json:"actualDays"`
	MissingDates string    `gorm:"type:jsonb;default:'[]'" json:"missingDates"`
	Status       string    `gorm:"size:16;default:success" json:"status"`
	Message      string    `gorm:"size:1024" json:"message"`
	CreatedAt    time.Time `json:"createdAt"`
}

