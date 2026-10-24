package openapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

// SetArchive 注入归档服务（由 app 层构造并共享给调度器）。
func (h *Handler) SetArchive(svc *ArchiveService) { h.archive = svc }

// ArchiveService 返回归档服务实例，便于调度器复用。
func (h *Handler) ArchiveService() *ArchiveService { return h.archive }

// ListArchives 归档运行列表。
func (h *Handler) ListArchives(c *gin.Context) {
	q := h.db.Model(&persistence.MetricArchiveRun{})
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		q = q.Where("status = ?", v)
	}
	if page, size, ok := resp.ParsePage(c, 20, 200); ok {
		var total int64
		if err := q.Count(&total).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		var rows []persistence.MetricArchiveRun
		if err := q.Order("from_date DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			resp.Fail(c, err)
			return
		}
		resp.OK(c, gin.H{"items": rows, "count": len(rows), "total": total, "page": page, "pageSize": size})
		return
	}
	var rows []persistence.MetricArchiveRun
	if err := q.Order("from_date DESC, id DESC").Limit(200).Find(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows)})
}

// RunArchive 手工触发一次超期归档+下线；默认同时处理日内与日数据，
// 可用 table=metric_value_rt|metric_value_day 只处理单表，
// retainDays / rtRetainDays 覆盖系统默认保留期。
func (h *Handler) RunArchive(c *gin.Context) {
	if h.archive == nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "归档服务未启用"))
		return
	}
	dayRetainDays := h.paramInt("day_retain_days", 1095)
	if v := strings.TrimSpace(c.Query("retainDays")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			dayRetainDays = n
		}
	}
	rtRetainDays := h.paramInt("rt_retain_days", 90)
	if v := strings.TrimSpace(c.Query("rtRetainDays")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			rtRetainDays = n
		}
	}
	var rtCount, dayCount int
	var err error
	if table := strings.TrimSpace(c.Query("table")); table != "" {
		if !supportedArchiveTable(table) {
			resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "不支持的归档表: %s", table))
			return
		}
		retain := dayRetainDays
		if table == TableRT {
			retain = rtRetainDays
		}
		count, err := h.archive.ArchiveDue(c.Request.Context(), table, retain)
		if err == nil && table == TableRT {
			rtCount = count
		} else if err == nil {
			dayCount = count
		}
	} else {
		rtCount, dayCount, err = h.archive.RetentionSweep(c.Request.Context(), rtRetainDays, dayRetainDays)
	}
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "archive_run", "metric_value_day",
		fmt.Sprintf("day=%d rt=%d retainDays=%d rtRetainDays=%d", dayCount, rtCount, dayRetainDays, rtRetainDays))
	resp.OK(c, gin.H{
		"archivedPartitions":   dayCount,
		"rtArchivedPartitions": rtCount,
		"retainDays":           dayRetainDays,
		"rtRetainDays":         rtRetainDays,
	})
}

// RestoreArchive 把归档文件按需回填到在线分区（幂等，主键冲突跳过）。
func (h *Handler) RestoreArchive(c *gin.Context) {
	if h.archive == nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "归档服务未启用"))
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "归档记录 ID 非法"))
		return
	}
	inserted, err := h.archive.Restore(c.Request.Context(), id)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "archive_restore", "metric_archive_run",
		fmt.Sprintf("id=%d restored=%d", id, inserted))
	resp.OK(c, gin.H{"restoredRows": inserted})
}

// QueryArchivedData 按需查询归档数据（超期在线分区已压缩）。
func (h *Handler) QueryArchivedData(c *gin.Context) {
	if h.archive == nil {
		resp.Fail(c, resp.NewErr(resp.CodeDepUnavailable, "归档服务未启用"))
		return
	}
	from, to, ok := parseRange(c, 365*3)
	if !ok {
		return
	}
	rows, err := h.archive.QueryArchived(c.Request.Context(), strings.TrimSpace(c.Query("metricCode")), from, to)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"items": rows, "count": len(rows), "from": from, "to": to, "source": "archive"})
}

// RetentionView 返回在线保留期配置与归档概况。
func (h *Handler) RetentionView(c *gin.Context) {
	type stat struct {
		Status        string `json:"status"`
		Count         int64  `json:"count"`
		Bytes         int64  `json:"bytes"`
		OnlineDropped int64  `json:"onlineDropped"`
	}
	var rows []stat
	if err := h.db.Model(&persistence.MetricArchiveRun{}).
		Select("status, count(*) AS count, coalesce(sum(bytes),0) AS bytes, count(*) FILTER (WHERE online_dropped) AS online_dropped").
		Group("status").Scan(&rows).Error; err != nil {
		resp.Fail(c, err)
		return
	}
	var last *persistence.MetricArchiveRun
	var lastRun persistence.MetricArchiveRun
	if err := h.db.Order("from_date DESC, id DESC").First(&lastRun).Error; err == nil {
		last = &lastRun
	}
	resp.OK(c, gin.H{
		"rtRetainDays":  h.paramInt("rt_retain_days", 90),
		"dayRetainDays": h.paramInt("day_retain_days", 1095),
		"archiveStats":  rows, "lastRun": last,
	})
}

func (h *Handler) paramInt(name string, fallback int) int {
	var row struct{ Value string }
	if err := h.db.Table("sys_param").Select("value").Where("key = ?", name).Scan(&row).Error; err != nil || row.Value == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(row.Value))
	if err != nil {
		return fallback
	}
	return n
}

// UpdateRetention 调整在线保留期（写入 sys_param，热生效）。
func (h *Handler) UpdateRetention(c *gin.Context) {
	var in struct {
		RTRetainDays  *int `json:"rtRetainDays"`
		DayRetainDays *int `json:"dayRetainDays"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "参数错误"))
		return
	}
	apply := func(name string, value *int) error {
		if value == nil {
			return nil
		}
		if *value < 0 || *value > 36500 {
			return resp.NewErr(resp.CodeBadRequest, "%s 超出 [0, 36500]", name)
		}
		return h.db.Exec(`INSERT INTO sys_param(key, value, category, updated_at)
			VALUES (?, ?, 'retention', now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
			name, strconv.Itoa(*value)).Error
	}
	if err := apply("rt_retain_days", in.RTRetainDays); err != nil {
		resp.Fail(c, err)
		return
	}
	if err := apply("day_retain_days", in.DayRetainDays); err != nil {
		resp.Fail(c, err)
		return
	}
	middleware.Log(c, "openapi", "retention_update", "retention", time.Now().Format(time.RFC3339))
	resp.OK(c, gin.H{
		"rtRetainDays":  h.paramInt("rt_retain_days", 90),
		"dayRetainDays": h.paramInt("day_retain_days", 1095),
	})
}

