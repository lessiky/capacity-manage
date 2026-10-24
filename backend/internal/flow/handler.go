package flow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
"capacity-manage/backend/internal/pkg/scope"
	"capacity-manage/backend/internal/pyclient"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	db     *gorm.DB
	engine *Engine
	az     *middleware.AuthZ
	py     *pyclient.Client
	log    *slog.Logger
}

func NewHandler(db *gorm.DB, engine *Engine, az *middleware.AuthZ, py *pyclient.Client, log *slog.Logger) *Handler {
	return &Handler{db: db, engine: engine, az: az, py: py, log: log}
}

type flowReq struct {
	FlowCode             string          `json:"flowCode" binding:"required,min=2,max=64"`
	Name                 string          `json:"name" binding:"required"`
	MetricCode           string          `json:"metricCode" binding:"required"`
	DataSourceID         *int64          `json:"dataSourceId"`        // 数据源(与 DataSourceCategoryID 二选一)
	DataSourceCategoryID *int64          `json:"dataSourceCategoryId"` // 数据源分类(logparse 专用)
	CollectType          string          `json:"collectType" binding:"required,oneof=sql logparse python script"`
	CollectConfig json.RawMessage `json:"collectConfig" binding:"required"`
	CleanConfig   json.RawMessage `json:"cleanConfig"`
	ScheduleMode  string          `json:"scheduleMode" binding:"omitempty,oneof=normal trading_day"`
	CronExpr      string          `json:"cronExpr"`
	TradingMarket string          `json:"tradingMarket"`
	WindowTime    string          `json:"windowTime"`
	BizDateMode   string          `json:"bizDateMode" binding:"omitempty,oneof=natural prev_day calendar"`
	Remark        string          `json:"remark"`
}

func (h *Handler) List(c *gin.Context) {
	q := h.db.Model(&persistence.CollectFlow{})
	if v := c.Query("metricCode"); v != "" {
		q = q.Where("metric_code = ?", v)
	}
	if v := c.Query("dataSourceId"); v != "" {
		q = q.Where("data_source_id = ?", v)
	}
	if v := c.Query("dataSourceCategoryId"); v != "" {
		q = q.Where("data_source_category_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("collectType"); v != "" {
		q = q.Where("collect_type = ?", v)
	}
	if sys := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)); sys != nil {
		q = q.Joins("JOIN capacity_metric m ON m.metric_code = collect_flow.metric_code")
		q = scope.Apply(q, sys)
	}
	var total int64
	_ = q.Count(&total).Error
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := atoi(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	var items []persistence.CollectFlow
	_ = q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error
	resp.OK(c, resp.Page(items, total, page, size))
}

func (h *Handler) Get(c *gin.Context) {
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	resp.OK(c, fl)
}

func (h *Handler) Create(c *gin.Context) {
	var req flowReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	if err := h.validate(&req); err != nil {
		resp.Fail(c, err)
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	cleanCfg := req.CleanConfig
	if len(cleanCfg) == 0 {
		cleanCfg = json.RawMessage("{}")
	}
	fl := persistence.CollectFlow{
		FlowCode: req.FlowCode, Name: req.Name, MetricCode: req.MetricCode,
		DataSourceID: req.DataSourceID, DataSourceCategoryID: req.DataSourceCategoryID,
		CollectType: req.CollectType,
		CollectConfig: string(req.CollectConfig), CleanConfig: string(cleanCfg),
		ScheduleMode: defaultStr(req.ScheduleMode, "normal"), CronExpr: req.CronExpr,
		TradingMarket: defaultStr(req.TradingMarket, "SH"), WindowTime: req.WindowTime,
		BizDateMode: defaultStr(req.BizDateMode, "natural"),
		Version: 1, Status: "created", Enabled: false, Remark: req.Remark, CreatedBy: un,
	}
	if err := h.db.Create(&fl).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "创建失败(流程编码或指标已占用?)"))
		return
	}
	middleware.Log(c, "flow", "flow.create", fl.FlowCode, "")
	resp.OK(c, fl)
}

func (h *Handler) validate(req *flowReq) error {
	var n int64
	_ = h.db.Model(&persistence.CapacityMetric{}).Where("metric_code = ? AND status='active'", req.MetricCode).Count(&n).Error
	if n == 0 {
		return resp.NewErr(resp.CodeBadRequest, "指标不存在或已停用")
	}
	// 一指标一流程
	_ = h.db.Model(&persistence.CollectFlow{}).Where("metric_code = ? AND status <> 'archived'", req.MetricCode).Count(&n).Error
	if n > 0 {
		return resp.NewErr(resp.CodeConflict, "该指标已存在采集流程")
	}
	if err := h.validateDSBinding(req.CollectType, req.DataSourceID, req.DataSourceCategoryID, req.CollectConfig); err != nil {
		return err
	}
	if req.ScheduleMode == "normal" && req.CronExpr == "" {
		return resp.NewErr(resp.CodeBadRequest, "normal 调度模式须配置 cronExpr")
	}
	if req.ScheduleMode == "trading_day" {
		if req.WindowTime == "" {
			return resp.NewErr(resp.CodeBadRequest, "trading_day 调度模式须配置 windowTime(非交易时点)")
		}
		if err := validateWindowTime(req.WindowTime); err != nil {
			return err
		}
	}
	if req.CollectType == "python" {
		var pc pyConfig
		if err := json.Unmarshal(req.CollectConfig, &pc); err != nil || pc.ScriptID == 0 {
			return resp.NewErr(resp.CodeBadRequest, "python 采集须配置 scriptId")
		}
		// 本地沙箱仅能执行 python 脚本; bash/powershell 须走 script 采集(ssh 执行目标)
		var s persistence.PythonScript
		if err := h.db.First(&s, pc.ScriptID).Error; err == nil && s.Language != "" && s.Language != "python" {
			return resp.NewErr(resp.CodeBadRequest,
				"本地沙箱仅支持 python 脚本, 该脚本语言为 %s; 请改用脚本采集并绑定 ssh 执行目标", s.Language)
		}
	}
	if req.CollectType == "script" {
		var sc scriptConfig
		if err := json.Unmarshal(req.CollectConfig, &sc); err != nil || sc.ScriptID == 0 {
			return resp.NewErr(resp.CodeBadRequest, "脚本采集须配置 scriptId")
		}
	}
	return nil
}

// bindIDTri 三态解析数据源/分类绑定字段: JSON key 缺省=未提供(沿用); null=清空; 数字=设置
func bindIDTri(raw json.RawMessage) (val *int64, provided bool, err error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return nil, false, nil
	}
	if s == "null" {
		return nil, true, nil
	}
	var v int64
	if e := json.Unmarshal([]byte(s), &v); e != nil {
		return nil, false, e
	}
	return &v, true, nil
}

func (h *Handler) Update(c *gin.Context) {
	var req struct {
		Name                 string          `json:"name"`
		DataSourceID         json.RawMessage `json:"dataSourceId"`
		DataSourceCategoryID json.RawMessage `json:"dataSourceCategoryId"`
		CollectConfig        json.RawMessage `json:"collectConfig"`
		CleanConfig          json.RawMessage `json:"cleanConfig"`
		CronExpr             string          `json:"cronExpr"`
		TradingMarket        string          `json:"tradingMarket"`
		WindowTime           string          `json:"windowTime"`
		BizDateMode          string          `json:"bizDateMode"`
		Remark               string          `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	if fl.Status == "archived" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "已归档流程不可编辑"))
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	// 数据源/分类重绑定(三态): 字段缺省=沿用原值; null=显式清空; 数字=设置
	newDS, chDS := fl.DataSourceID, false
	if b, ch, err := bindIDTri(req.DataSourceID); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "dataSourceId 参数错误: %v", err))
		return
	} else if ch {
		newDS, chDS = b, true
	}
	newCat, chCat := fl.DataSourceCategoryID, false
	if b, ch, err := bindIDTri(req.DataSourceCategoryID); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "dataSourceCategoryId 参数错误: %v", err))
		return
	} else if ch {
		newCat, chCat = b, true
	}
	dsBindingChanged := chDS || chCat
	if dsBindingChanged {
		cfgForCheck := req.CollectConfig
		if len(cfgForCheck) == 0 {
			cfgForCheck = json.RawMessage(fl.CollectConfig)
		}
		if err := h.validateDSBinding(fl.CollectType, newDS, newCat, cfgForCheck); err != nil {
			resp.Fail(c, err)
			return
		}
		updates["data_source_id"] = newDS
		updates["data_source_category_id"] = newCat
	}
	if len(req.CollectConfig) > 0 {
		updates["collect_config"] = string(req.CollectConfig)
	}
	if len(req.CleanConfig) > 0 {
		updates["clean_config"] = string(req.CleanConfig)
	}
	if req.CronExpr != "" {
		updates["cron_expr"] = req.CronExpr
	}
	if req.TradingMarket != "" {
		updates["trading_market"] = req.TradingMarket
	}
	if req.WindowTime != "" {
		if err := validateWindowTime(req.WindowTime); err != nil {
			resp.Fail(c, err)
			return
		}
		updates["window_time"] = req.WindowTime
	}
	if req.BizDateMode != "" {
		if req.BizDateMode != "natural" && req.BizDateMode != "prev_day" && req.BizDateMode != "calendar" {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "bizDateMode 取值须为 natural/prev_day/calendar"))
			return
		}
		updates["biz_date_mode"] = req.BizDateMode
	}
	if req.Remark != "" {
		updates["remark"] = req.Remark
	}
	// 配置变更: version+1 且回到 created(需重新预览生效)
	if dsBindingChanged || len(req.CollectConfig) > 0 || len(req.CleanConfig) > 0 || req.CronExpr != "" || req.WindowTime != "" || req.BizDateMode != "" {
		updates["version"] = fl.Version + 1
		updates["status"] = "created"
		updates["enabled"] = false
	}
	_ = h.db.Model(&fl).Updates(updates).Error
	middleware.Log(c, "flow", "flow.update", fl.FlowCode, "")
	resp.OK(c, nil)
}

func (h *Handler) Preview(c *gin.Context) {
	res, err := h.engine.RunPreview(c.Request.Context(), mustInt64(c.Param("id")))
	if err != nil {
		var be *BizErr
		if errors.As(err, &be) {
			resp.Fail(c, resp.NewErr(be.Code, "%s", be.Msg))
			return
		}
		resp.Fail(c, err)
		return
	}
	resp.OK(c, res)
}

func (h *Handler) Activate(c *gin.Context) {
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	if fl.Status != "created" && fl.Status != "disabled" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅 created/disabled 状态可生效"))
		return
	}
	_ = h.db.Model(&fl).Updates(map[string]any{"status": "active", "enabled": true}).Error
	middleware.Log(c, "flow", "flow.activate", fl.FlowCode, "")
	resp.OK(c, nil)
}

func (h *Handler) Disable(c *gin.Context) {
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	_ = h.db.Model(&fl).Updates(map[string]any{"status": "disabled", "enabled": false}).Error
	middleware.Log(c, "flow", "flow.disable", fl.FlowCode, "")
	resp.OK(c, nil)
}

func (h *Handler) Archive(c *gin.Context) {
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	_ = h.db.Model(&fl).Updates(map[string]any{"status": "archived", "enabled": false}).Error
	middleware.Log(c, "flow", "flow.archive", fl.FlowCode, "")
	resp.OK(c, nil)
}

// Delete 删除（仅已归档流程可删除; 其执行日志/清洗痕迹按 FK ON DELETE CASCADE 一并清除）
func (h *Handler) Delete(c *gin.Context) {
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "流程不存在"))
		return
	}
	if fl.Status != "archived" {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "仅已归档的流程可删除, 请先归档"))
		return
	}
	var running int64
	_ = h.db.Model(&persistence.CollectTaskLog{}).
		Where("flow_id = ? AND status = 'running'", fl.ID).Count(&running).Error
	if running > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该流程存在执行中的任务, 请稍后再试"))
		return
	}
	if err := h.db.Delete(&fl).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "flow", "flow.delete", fl.FlowCode, "")
	resp.OK(c, nil)
}

// Trigger 手工触发一次（异步执行, 立即返回任务提示）
func (h *Handler) Trigger(c *gin.Context) {
	fl, err := h.loadFlowFor(c, c.Param("id"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	var running int64
	_ = h.db.Model(&persistence.CollectTaskLog{}).
		Where("flow_id = ? AND status = 'running'", fl.ID).Count(&running).Error
	if running > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "该流程存在执行中的任务, 请稍后再试"))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	// 独立后台 context: 请求返回后任务继续执行（超时与调度器一致）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), h.engine.RunTimeout())
		defer cancel()
		_, _ = h.engine.Run(ctx, fl.ID, "manual", un)
	}()
	resp.OK(c, gin.H{"flowCode": fl.FlowCode, "message": "已触发, 请在任务监控查看结果"})
}

func (h *Handler) loadFlowFor(c *gin.Context, id string) (*persistence.CollectFlow, error) {
	var fl persistence.CollectFlow
	if err := h.db.First(&fl, id).Error; err != nil {
		return nil, resp.NewErr(resp.CodeNotFound, "流程不存在")
	}
	return &fl, nil
}

// validateDSBinding 数据源绑定校验: 数据源与数据源分类二选一; 分类仅日志采集可用且须存在可用日志数据源;
// python 采集在平台沙箱执行, 不使用数据源, 允许两项都不绑定
func (h *Handler) validateDSBinding(collectType string, dataSourceID, categoryID *int64, collectConfig json.RawMessage) error {
	hasDS, hasCat := dataSourceID != nil && *dataSourceID > 0, categoryID != nil && *categoryID > 0
	// python 采集在平台沙箱执行, 不访问数据源 → 允许两项都不绑定
	if collectType == "python" && !hasDS && !hasCat {
		return nil
	}
	if hasDS == hasCat {
		return resp.NewErr(resp.CodeBadRequest, "数据源与数据源分类须且仅可选择一项")
	}
	var n int64
	if hasCat {
		if collectType != "logparse" {
			return resp.NewErr(resp.CodeBadRequest, "数据源分类仅日志解析采集可用")
		}
		var cat persistence.DataSourceCategory
		if err := h.db.First(&cat, *categoryID).Error; err != nil {
			return resp.NewErr(resp.CodeBadRequest, "数据源分类不存在")
		}
		_ = h.db.Model(&persistence.DataSource{}).
			Where("category_id = ? AND type = 'log' AND status <> 'disabled'", *categoryID).Count(&n).Error
		if n == 0 {
			return resp.NewErr(resp.CodeBadRequest, "该分类下暂无可用日志数据源, 请先添加未停用的日志数据源")
		}
		return nil
	}
	_ = h.db.Model(&persistence.DataSource{}).Where("id = ? AND status <> 'disabled'", *dataSourceID).Count(&n).Error
	if n == 0 {
		return resp.NewErr(resp.CodeBadRequest, "数据源不存在或已停用")
	}
	// 脚本采集在目标机上执行: 必须绑定 ssh 类型的执行目标
	if collectType == "script" {
		var ds persistence.DataSource
		if err := h.db.First(&ds, *dataSourceID).Error; err != nil {
			return resp.NewErr(resp.CodeBadRequest, "数据源不存在")
		}
		if ds.Type != "ssh" {
			return resp.NewErr(resp.CodeBadRequest,
				"脚本采集须绑定执行目标(数据源类型 ssh), 当前为 %s", ds.Type)
		}
	}
	return nil
}

// ---------- 脚本管理 ----------

func (h *Handler) ListScripts(c *gin.Context) {
	var items []persistence.PythonScript
	q := h.db.Model(&persistence.PythonScript{})
	if v := c.Query("scriptType"); v != "" {
		q = q.Where("script_type = ?", v)
	}
	_ = q.Order("id").Find(&items).Error
	resp.OK(c, items)
}

func (h *Handler) CreateScript(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		ScriptType  string `json:"scriptType" binding:"required,oneof=collect clean"`
		Language    string `json:"language" binding:"omitempty,oneof=python bash powershell"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	lang := req.Language
	if lang == "" {
		lang = "python"
	}
	// 清洗脚本在本地沙箱执行, 仅支持 python
	if req.ScriptType == "clean" && lang != "python" {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "清洗脚本在本地沙箱执行, 仅支持 python"))
		return
	}
	s := persistence.PythonScript{Name: req.Name, ScriptType: req.ScriptType, Language: lang, Description: req.Description, CreatedBy: un}
	if err := h.db.Create(&s).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "脚本已存在"))
		return
	}
	middleware.Log(c, "script", "script.create", s.Name, "")
	resp.OK(c, s)
}

func (h *Handler) ListVersions(c *gin.Context) {
	var items []persistence.PythonScriptVersion
	_ = h.db.Where("script_id = ?", c.Param("id")).Order("version DESC").Find(&items).Error
	resp.OK(c, items)
}

func (h *Handler) CreateVersion(c *gin.Context) {
	var req struct {
		Content string `json:"content" binding:"required"`
		Remark  string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误: %v", err))
		return
	}
	id := mustInt64(c.Param("id"))
	var script persistence.PythonScript
	if err := h.db.First(&script, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "脚本不存在"))
		return
	}
	// FR-COL-19: 语法校验通过才保存。
	// 本地沙箱校验器只认 Python; bash/powershell 脚本由目标机解释器执行,
	// 不能拿 Python 解析器去校验(否则 bash 脚本会被误判, 如 "invalid decimal literal")。
	lang := script.Language
	if lang == "" {
		lang = "python"
	}
	if h.py != nil && lang == "python" {
		ck, cerr := h.py.Check(c.Request.Context(), req.Content)
		if cerr != nil {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "语法校验不可用(worker 不可达), 请稍后重试: %v", cerr))
			return
		}
		if !ck.OK {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "语法校验失败: %s", ck.Error))
			return
		}
	}
	var maxV int64
	_ = h.db.Model(&persistence.PythonScriptVersion{}).Where("script_id = ?", id).Select("COALESCE(MAX(version),0)").Scan(&maxV).Error
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	sum := sha256.Sum256([]byte(req.Content))
	v := persistence.PythonScriptVersion{
		ScriptID: id, Version: int(maxV) + 1, Content: req.Content,
		Checksum: hex.EncodeToString(sum[:]),
		Active:   maxV == 0, // 首版本自动生效
		PublishedBy: un, PublishedAt: ptrTime(time.Now()), Remark: req.Remark,
	}
	if err := h.db.Create(&v).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "script", "script.version", script.Name, "v"+fmt.Sprint(v.Version))
	resp.OK(c, v)
}

func (h *Handler) ActivateVersion(c *gin.Context) {
	id := mustInt64(c.Param("id"))
	v := int(mustInt64(c.Param("v")))
	tx := h.db.Begin()
	_ = tx.Model(&persistence.PythonScriptVersion{}).Where("script_id = ? AND active", id).Update("active", false).Error
	res := tx.Model(&persistence.PythonScriptVersion{}).Where("script_id = ? AND version = ?", id, v).Update("active", true)
	if res.RowsAffected == 0 {
		tx.Rollback()
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "版本不存在"))
		return
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "script", "script.activate", "", "script="+fmt.Sprint(id)+" v="+fmt.Sprint(v))
	resp.OK(c, nil)
}

// validateWindowTime 校验非交易时点: 单时刻 "HH:MM", 允许 15:00 至次日 09:00, 15 分钟一个间隔
func validateWindowTime(s string) error {
	str := strings.TrimSpace(s)
	if str == "" {
		return resp.NewErr(resp.CodeBadRequest, "windowTime 不能为空")
	}
	parts := strings.Split(str, ":")
	if len(parts) != 2 {
		return resp.NewErr(resp.CodeBadRequest, "windowTime 格式应为 HH:MM 单时点, 不再支持时段")
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return resp.NewErr(resp.CodeBadRequest, "windowTime 时刻无效: %s", str)
	}
	if mm%15 != 0 {
		return resp.NewErr(resp.CodeBadRequest, "非交易时点须为 15 分钟间隔: %s", str)
	}
	if !(hh >= 15 || hh < 9 || (hh == 9 && mm == 0)) {
		return resp.NewErr(resp.CodeBadRequest, "非交易时点须在 15:00 至次日 09:00 之间: %s", str)
	}
	return nil
}

// DeleteScript 删除脚本（被未归档流程引用时禁止删除; 版本随 FK CASCADE 清理并显式兜底）
func (h *Handler) DeleteScript(c *gin.Context) {
	id := mustInt64(c.Param("id"))
	var script persistence.PythonScript
	if err := h.db.First(&script, id).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "脚本不存在"))
		return
	}
	var flows []persistence.CollectFlow
	_ = h.db.Where("status <> 'archived'").Find(&flows).Error
	var refs []string
	for i := range flows {
		if flowRefsScript(flows[i].CollectConfig, flows[i].CleanConfig, id) {
			refs = append(refs, flows[i].FlowCode)
		}
	}
	if len(refs) > 0 {
		resp.Fail(c, resp.NewErr(resp.CodeConflict, "脚本被以下流程引用, 不可删除: %s", strings.Join(refs, "、")))
		return
	}
	tx := h.db.Begin()
	_ = tx.Where("script_id = ?", id).Delete(&persistence.PythonScriptVersion{}).Error
	if err := tx.Delete(&script).Error; err != nil {
		tx.Rollback()
		resp.Fail(c, err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "script", "script.delete", script.Name, "")
	resp.OK(c, nil)
}

// flowRefsScript 流程是否在采集(python: scriptId)或清洗(script.scriptId)配置中引用该脚本
func flowRefsScript(collectCfg, cleanCfg string, scriptID int64) bool {
	var cc struct {
		ScriptID int64 `json:"scriptId"`
	}
	if json.Unmarshal([]byte(collectCfg), &cc) == nil && cc.ScriptID == scriptID {
		return true
	}
	var kc struct {
		Script *struct {
			ScriptID int64 `json:"scriptId"`
		} `json:"script"`
	}
	return json.Unmarshal([]byte(cleanCfg), &kc) == nil && kc.Script != nil && kc.Script.ScriptID == scriptID
}

// ---------- 任务监控 ----------

type TaskHandler struct {
	db *gorm.DB
	az *middleware.AuthZ
}

func NewTaskHandler(db *gorm.DB, az *middleware.AuthZ) *TaskHandler {
	return &TaskHandler{db: db, az: az}
}

func (h *TaskHandler) List(c *gin.Context) {
	q := h.db.Model(&persistence.CollectTaskLog{})
	if sys := scope.Systems(c.Request.Context(), h.db, h.az, scope.Username(c)); sys != nil {
		q = q.Joins("JOIN capacity_metric m ON m.metric_code = collect_task_log.metric_code")
		q = scope.Apply(q, sys)
	}
	if v := c.Query("flowId"); v != "" {
		q = q.Where("flow_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("source"); v != "" {
		q = q.Where("source = ?", v)
	}
	if v := c.Query("date"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			q = q.Where("triggered_at >= ? AND triggered_at < ?", t, t.AddDate(0, 0, 1))
		}
	}
	var total int64
	_ = q.Count(&total).Error
	page, size := 1, 20
	if v := c.Query("page"); v != "" {
		if n, err := atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("pageSize"); v != "" {
		if n, err := atoi(v); err == nil && n > 0 && n <= 200 {
			size = n
		}
	}
	var items []persistence.CollectTaskLog
	_ = q.Order("triggered_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error
	resp.OK(c, resp.Page(items, total, page, size))
}

func (h *TaskHandler) Get(c *gin.Context) {
	var t persistence.CollectTaskLog
	if err := h.db.First(&t, c.Param("id")).Error; err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "任务不存在"))
		return
	}
	var traces []persistence.CleaningTrace
	_ = h.db.Where("task_log_id = ?", t.ID).Order("id").Find(&traces).Error
	resp.OK(c, gin.H{"task": t, "cleaningTraces": traces})
}

func (h *TaskHandler) CleaningTraces(c *gin.Context) {
	var traces []persistence.CleaningTrace
	_ = h.db.Where("task_log_id = ?", c.Param("id")).Order("id").Find(&traces).Error
	resp.OK(c, traces)
}

// ---------- helpers ----------

func atoi(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func mustInt64(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func ptrTime(t time.Time) *time.Time { return &t }
