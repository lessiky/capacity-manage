package openapi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/clock"
	"capacity-manage/backend/internal/pkg/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 归档服务支持的两张在线数据表（FR-DAY-06）。
const (
	TableRT  = "metric_value_rt"
	TableDay = "metric_value_day"

	// archivePayloadVersion 2: 日内数据带 ts、日数据带修正字段；
	// 1 为旧版仅日数据载荷，遇到 v1 归档会重新导出后再下线，避免只剩归档时丢字段。
	archivePayloadVersion = 2
	archiveBatchSize      = 1000
)

var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func validIdent(name string) bool { return identPattern.MatchString(name) }

func supportedArchiveTable(table string) bool {
	return table == TableRT || table == TableDay
}

func archiveKey(table, partition string) string {
	return fmt.Sprintf("archive/%s/%s.jsonl.gz", table, partition)
}

// cutoffMonthStart 返回在线保留期边界所在月份的首日：早于该月首日的完整月分区视为超期。
func cutoffMonthStart(now time.Time, retainDays int) time.Time {
	base := clock.DayStart(now)
	if retainDays > 0 {
		base = base.AddDate(0, 0, -retainDays)
	}
	return time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.Local)
}

// isDuePartition 判断整月分区 [from,to) 是否已完整早于保留边界。
func isDuePartition(to, cutoffMonth time.Time) bool {
	return !to.After(cutoffMonth)
}

// parsePartitionRange 解析分区名并返回 [起始月, 次月首日)。
// 规范命名为 ensure_partitions 生成的 metric_value_day_yYYYYmMM；
// 同时兼容早期文档中的 metric_value_day_pYYYYMM 写法。
func parsePartitionRange(name string) (time.Time, time.Time, bool) {
	if idx := strings.LastIndex(name, "_y"); idx >= 0 {
		raw := name[idx+2:]
		if len(raw) == 7 && raw[4] == 'm' {
			if parsed, err := time.ParseInLocation("200601", raw[:4]+raw[5:], time.Local); err == nil {
				return parsed, parsed.AddDate(0, 1, 0), true
			}
		}
	}
	idx := strings.LastIndex(name, "_p")
	if idx < 0 || idx+7 > len(name) {
		return time.Time{}, time.Time{}, false
	}
	raw := name[idx+2:]
	if len(raw) != 6 {
		return time.Time{}, time.Time{}, false
	}
	parsed, err := time.ParseInLocation("200601", raw, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return parsed, parsed.AddDate(0, 1, 0), true
}

// archivePayload 是归档文件内的行格式（gzip + JSON Lines）。
// 日数据用 bizDate，日内数据用 ts；v2 起补齐日数据修正字段，保证“只剩归档”时不丢信息。
type archivePayload struct {
	MetricCode string     `json:"metricCode"`
	BizDate    string     `json:"bizDate,omitempty"`
	TS         *time.Time `json:"ts,omitempty"`

	// 日数据字段
	DayMax           *float64   `json:"dayMax"`
	DayMin           *float64   `json:"dayMin"`
	DayAvg           *float64   `json:"dayAvg"`
	DayLast          *float64   `json:"dayLast"`
	StatValue        *float64   `json:"statValue"`
	WaterLevel       *float64   `json:"waterLevel"`
	Level            string     `json:"level,omitempty"`
	PointCount       int        `json:"pointCount"`
	Source           string     `json:"source,omitempty"`
	Corrected        bool       `json:"corrected,omitempty"`
	CorrectReason    string     `json:"correctReason,omitempty"`
	CorrectedBy      string     `json:"correctedBy,omitempty"`
	CorrectedAt      *time.Time `json:"correctedAt,omitempty"`
	OriginalSnapshot *string    `json:"originalSnapshot,omitempty"`
	UpdatedAt        *time.Time `json:"updatedAt,omitempty"`

	// 日内数据字段
	RawValue      *float64 `json:"rawValue,omitempty"`
	CleanValue    *float64 `json:"cleanValue,omitempty"`
	IsAnomaly     bool     `json:"isAnomaly,omitempty"`
	AnomalyReason string   `json:"anomalyReason,omitempty"`
	JudgeLevel    string   `json:"judgeLevel,omitempty"`

	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// payloadIndexDate 取出用于归档索引的日期：日内数据用 ts，日数据用 bizDate。
func payloadIndexDate(p archivePayload) time.Time {
	if p.TS != nil {
		return *p.TS
	}
	if t, err := time.ParseInLocation("2006-01-02", p.BizDate, time.Local); err == nil {
		return t
	}
	return time.Time{}
}

// payloadsToDayRows 把归档载荷映射为日数据行，供恢复写入在线分区。
func payloadsToDayRows(ps []archivePayload) []persistence.MetricValueDay {
	out := make([]persistence.MetricValueDay, 0, len(ps))
	for _, p := range ps {
		bizDate, err := time.ParseInLocation("2006-01-02", p.BizDate, time.Local)
		if err != nil {
			continue
		}
		row := persistence.MetricValueDay{
			MetricCode: p.MetricCode, BizDate: bizDate,
			DayMax: p.DayMax, DayMin: p.DayMin, DayAvg: p.DayAvg, DayLast: p.DayLast,
			StatValue: p.StatValue, WaterLevel: p.WaterLevel, Level: p.Level,
			PointCount: p.PointCount, Source: p.Source, Corrected: p.Corrected,
			CorrectReason: p.CorrectReason, CorrectedBy: p.CorrectedBy,
			CorrectedAt: p.CorrectedAt, OriginalSnapshot: p.OriginalSnapshot,
		}
		if p.CreatedAt != nil {
			row.CreatedAt = *p.CreatedAt
		}
		if p.UpdatedAt != nil {
			row.UpdatedAt = *p.UpdatedAt
		}
		out = append(out, row)
	}
	return out
}

// payloadsToRTRows 把归档载荷映射为日内数据行；缺少 ts 的旧版载荷会被跳过。
func payloadsToRTRows(ps []archivePayload) []persistence.MetricValueRT {
	out := make([]persistence.MetricValueRT, 0, len(ps))
	for _, p := range ps {
		if p.TS == nil {
			continue
		}
		row := persistence.MetricValueRT{
			MetricCode: p.MetricCode, Ts: *p.TS,
			RawValue: p.RawValue, CleanValue: p.CleanValue,
			IsAnomaly: p.IsAnomaly, AnomalyReason: p.AnomalyReason,
			JudgeLevel: p.JudgeLevel, Source: p.Source,
		}
		if p.CreatedAt != nil {
			row.CreatedAt = *p.CreatedAt
		}
		out = append(out, row)
	}
	return out
}

// ArchiveService 负责超期分区的压缩归档、在线下线与按需恢复（FR-DAY-06）。
// 归档文件写入对象存储，索引写入 metric_archive_index；只有文件与索引都写入成功后
// 才会 DETACH+DROP 在线分区，任何一步失败都保留在线数据。
type ArchiveService struct {
	db    *gorm.DB
	files store.Store
}

func NewArchiveService(db *gorm.DB, files store.Store) *ArchiveService {
	return &ArchiveService{db: db, files: files}
}

func (s *ArchiveService) available() bool {
	return s != nil && s.db != nil && s.files != nil
}

type duePartition struct {
	Table     string
	Partition string
	From      time.Time
	To        time.Time
}

// ArchiveDue 归档指定表超过在线保留期的完整月分区，成功后立即下线在线分区。
// 返回本次处理（归档并下线）的分区数；已下线的分区会被幂等跳过。
func (s *ArchiveService) ArchiveDue(ctx context.Context, table string, retainDays int) (int, error) {
	if !supportedArchiveTable(table) {
		return 0, fmt.Errorf("不支持的归档表: %s", table)
	}
	if !s.available() {
		return 0, nil
	}
	if retainDays < 0 {
		retainDays = 0
	}
	parts, err := s.duePartitions(ctx, table, cutoffMonthStart(time.Now(), retainDays))
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, p := range parts {
		done, err := s.archiveAndDrop(ctx, p)
		if err != nil {
			return handled, err
		}
		if done {
			handled++
		}
	}
	return handled, nil
}

// RetentionSweep 按各自在线保留期分别处理日内数据与日数据（调度器每日调用）。
func (s *ArchiveService) RetentionSweep(ctx context.Context, rtRetainDays, dayRetainDays int) (rtCount, dayCount int, err error) {
	if rtCount, err = s.ArchiveDue(ctx, TableRT, rtRetainDays); err != nil {
		return rtCount, 0, err
	}
	dayCount, err = s.ArchiveDue(ctx, TableDay, dayRetainDays)
	return rtCount, dayCount, err
}

func (s *ArchiveService) duePartitions(ctx context.Context, table string, cutoffMonth time.Time) ([]duePartition, error) {
	type row struct {
		Partition string
	}
	var rows []row
	err := s.db.WithContext(ctx).Raw(`
		SELECT c.relname AS partition
		FROM pg_class c
		JOIN pg_inherits i ON i.inhrelid = c.oid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = ?
		ORDER BY c.relname`, table).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]duePartition, 0, len(rows))
	for _, r := range rows {
		if !strings.HasPrefix(r.Partition, table+"_") || !validIdent(r.Partition) {
			continue
		}
		from, to, ok := parsePartitionRange(r.Partition)
		if !ok || !isDuePartition(to, cutoffMonth) {
			continue
		}
		out = append(out, duePartition{Table: table, Partition: r.Partition, From: from, To: to})
	}
	return out, nil
}

// archiveAndDrop 保证分区“先归档成功、再下线”；只归档但不删任何东西的场景不会被走到。
func (s *ArchiveService) archiveAndDrop(ctx context.Context, p duePartition) (bool, error) {
	run, err := s.loadRun(ctx, p)
	if err != nil {
		return false, err
	}
	if run != nil && run.OnlineDropped {
		return false, nil // 已下线，幂等跳过
	}
	if run != nil && run.Status == "success" && run.PayloadVersion >= archivePayloadVersion &&
		run.FileRef != "" && s.files.Exists(run.FileRef) {
		// 上次已归档成功但下线失败/未执行，这里补下线，不重复导出。
		if err := s.dropPartition(ctx, p); err != nil {
			s.markDropError(ctx, run.ID, err)
			return false, err
		}
		s.markDropped(ctx, run.ID)
		return true, nil
	}
	exists, attached, err := s.partitionInfo(ctx, p.Partition)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if !attached {
		// 残留的已 DETACH 表：无法再归档，直接清理，归档记录若存在则标记已下线。
		if err := s.dropDetachedTable(ctx, p.Partition); err != nil {
			return false, err
		}
		if run != nil {
			s.markDropped(ctx, run.ID)
		}
		return true, nil
	}
	runID, err := s.archivePartition(ctx, p)
	if err != nil {
		return false, err
	}
	if err := s.dropPartition(ctx, p); err != nil {
		s.markDropError(ctx, runID, err)
		return false, err
	}
	s.markDropped(ctx, runID)
	return true, nil
}

func (s *ArchiveService) loadRun(ctx context.Context, p duePartition) (*persistence.MetricArchiveRun, error) {
	var run persistence.MetricArchiveRun
	err := s.db.WithContext(ctx).Where("table_name = ? AND partition = ?", p.Table, p.Partition).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// partitionInfo 返回分区是否存在、是否仍挂在父表上。
func (s *ArchiveService) partitionInfo(ctx context.Context, partition string) (exists, attached bool, err error) {
	if !validIdent(partition) {
		return false, false, fmt.Errorf("非法分区标识: %s", partition)
	}
	if err = s.db.WithContext(ctx).Raw(
		`SELECT to_regclass(?) IS NOT NULL`, "public."+partition).Scan(&exists).Error; err != nil {
		return false, false, err
	}
	if !exists {
		return false, false, nil
	}
	if err = s.db.WithContext(ctx).Raw(
		`SELECT EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid WHERE c.relname = ?)`,
		partition).Scan(&attached).Error; err != nil {
		return true, false, err
	}
	return true, attached, nil
}

func (s *ArchiveService) dropPartition(ctx context.Context, p duePartition) error {
	if !validIdent(p.Table) || !validIdent(p.Partition) {
		return fmt.Errorf("非法分区标识: %s.%s", p.Table, p.Partition)
	}
	exists, attached, err := s.partitionInfo(ctx, p.Partition)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if attached {
		if err := s.db.WithContext(ctx).Exec(
			fmt.Sprintf("ALTER TABLE %s DETACH PARTITION %s", p.Table, p.Partition)).Error; err != nil {
			return fmt.Errorf("detach 分区失败: %w", err)
		}
	}
	return s.dropDetachedTable(ctx, p.Partition)
}

func (s *ArchiveService) dropDetachedTable(ctx context.Context, partition string) error {
	if !validIdent(partition) {
		return fmt.Errorf("非法分区标识: %s", partition)
	}
	return s.db.WithContext(ctx).Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", partition)).Error
}

func (s *ArchiveService) markDropped(ctx context.Context, id int64) {
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&persistence.MetricArchiveRun{}).Where("id = ?", id).
		Updates(map[string]any{"online_dropped": true, "dropped_at": now, "error_msg": ""}).Error
}

func (s *ArchiveService) markDropError(ctx context.Context, id int64, cause error) {
	_ = s.db.WithContext(ctx).Model(&persistence.MetricArchiveRun{}).Where("id = ?", id).
		Updates(map[string]any{"error_msg": "在线分区下线失败: " + cause.Error()}).Error
}

// countWriter 统计写入对象存储的压缩字节数（store 接口只提供文件流，不提供大小）。
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}

// archivePartition 将整月分区流式导出为 gzip JSON Lines 归档文件并分批写入索引，
// 返回归档运行记录 ID。重复归档同一分区会覆盖旧文件与索引（用于 v1 升级或恢复后再次归档）。
func (s *ArchiveService) archivePartition(ctx context.Context, p duePartition) (int64, error) {
	if !validIdent(p.Table) || !validIdent(p.Partition) {
		return 0, fmt.Errorf("非法分区标识: %s.%s", p.Table, p.Partition)
	}
	if !supportedArchiveTable(p.Table) {
		return 0, fmt.Errorf("不支持的归档表: %s", p.Table)
	}
	run, err := s.upsertRun(ctx, p, time.Now())
	if err != nil {
		return 0, err
	}
	if err := s.db.WithContext(ctx).Where("archive_id = ?", run.ID).
		Delete(&persistence.MetricArchiveIndex{}).Error; err != nil {
		s.failArchive(ctx, run.ID, err)
		return run.ID, err
	}

	key := archiveKey(p.Table, p.Partition)
	pr, pw := io.Pipe()
	cw := &countWriter{w: pw}
	type streamResult struct {
		rows  int64
		bytes int64
		err   error
	}
	done := make(chan streamResult, 1)
	go func() {
		res := streamResult{}
		defer func() { done <- res }()
		gz := gzip.NewWriter(cw)
		offset := int64(0)
		indexes := make([]persistence.MetricArchiveIndex, 0, archiveBatchSize)
		flush := func() error {
			if len(indexes) == 0 {
				return nil
			}
			if err := s.db.WithContext(ctx).CreateInBatches(&indexes, archiveBatchSize).Error; err != nil {
				return err
			}
			indexes = indexes[:0]
			return nil
		}
		scanErr := s.scanPartitionRows(ctx, p, func(payload archivePayload) error {
			line, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			line = append(line, '\n')
			if _, err := gz.Write(line); err != nil {
				return err
			}
			indexes = append(indexes, persistence.MetricArchiveIndex{
				ArchiveID: run.ID, MetricCode: payload.MetricCode,
				BizDate: clock.DayStart(payloadIndexDate(payload)), FileRef: key,
				ByteOffset: offset, RowBytes: len(line),
			})
			offset += int64(len(line))
			res.rows++
			if len(indexes) >= archiveBatchSize {
				return flush()
			}
			return nil
		})
		if scanErr == nil {
			scanErr = flush()
		}
		if scanErr == nil {
			scanErr = gz.Close()
		} else {
			_ = gz.Close()
		}
		res.bytes = cw.n
		if scanErr != nil {
			res.err = scanErr
			_ = pw.CloseWithError(scanErr)
			return
		}
		_ = pw.Close()
	}()

	saveErr := s.files.Save(key, pr)
	res := <-done
	if saveErr != nil {
		s.cleanupFailedArchive(ctx, run.ID)
		s.failArchive(ctx, run.ID, saveErr)
		return run.ID, saveErr
	}
	if res.err != nil {
		s.cleanupFailedArchive(ctx, run.ID)
		s.failArchive(ctx, run.ID, res.err)
		return run.ID, res.err
	}
	if err := s.db.WithContext(ctx).Model(&persistence.MetricArchiveRun{}).Where("id = ?", run.ID).
		Updates(map[string]any{
			"status": "success", "file_ref": key, "row_count": res.rows, "bytes": res.bytes,
			"finished_at": time.Now(), "payload_version": archivePayloadVersion, "error_msg": "",
			"online_dropped": false,
		}).Error; err != nil {
		return run.ID, err
	}
	return run.ID, nil
}

func (s *ArchiveService) cleanupFailedArchive(ctx context.Context, archiveID int64) {
	_ = s.db.WithContext(ctx).Where("archive_id = ?", archiveID).
		Delete(&persistence.MetricArchiveIndex{}).Error
}

func (s *ArchiveService) upsertRun(ctx context.Context, p duePartition, now time.Time) (*persistence.MetricArchiveRun, error) {
	run, err := s.loadRun(ctx, p)
	if err != nil {
		return nil, err
	}
	toDate := p.To.AddDate(0, 0, -1)
	if run == nil {
		run = &persistence.MetricArchiveRun{
			TableName: p.Table, Partition: p.Partition,
			FromDate: p.From, ToDate: toDate, Status: "running",
			StartedAt: &now, CreatedAt: now,
		}
		if err := s.db.WithContext(ctx).Create(run).Error; err != nil {
			return nil, err
		}
		return run, nil
	}
	if err := s.db.WithContext(ctx).Model(&persistence.MetricArchiveRun{}).Where("id = ?", run.ID).
		Updates(map[string]any{
			"from_date": p.From, "to_date": toDate, "status": "running", "error_msg": "",
			"started_at": now, "finished_at": nil, "row_count": 0, "bytes": 0,
			"online_dropped": false, "payload_version": 1,
		}).Error; err != nil {
		return nil, err
	}
	run.Status = "running"
	run.OnlineDropped = false
	return run, nil
}

// scanPartitionRows 以游标方式逐行读取分区数据，避免整月数据一次性进内存。
func (s *ArchiveService) scanPartitionRows(ctx context.Context, p duePartition, fn func(archivePayload) error) error {
	switch p.Table {
	case TableDay:
		rows, err := s.db.WithContext(ctx).Table(p.Partition).
			Select("metric_code, biz_date, day_max, day_min, day_avg, day_last, stat_value, water_level, level, point_count, source, corrected, correct_reason, corrected_by, corrected_at, original_snapshot, created_at, updated_at").
			Where("biz_date >= ? AND biz_date < ?", p.From, p.To).
			Order("metric_code, biz_date").Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				metricCode                                        string
				bizDate                                           time.Time
				dayMax, dayMin, dayAvg, dayLast, statValue, water *float64
				level                                             string
				pointCount                                        int
				source                                            string
				corrected                                         bool
				correctReason, correctedBy                        sql.NullString
				correctedAt                                       *time.Time
				snapshot                                          []byte
				createdAt, updatedAt                              time.Time
			)
			if err := rows.Scan(&metricCode, &bizDate, &dayMax, &dayMin, &dayAvg, &dayLast,
				&statValue, &water, &level, &pointCount, &source, &corrected, &correctReason,
				&correctedBy, &correctedAt, &snapshot, &createdAt, &updatedAt); err != nil {
				return err
			}
			payload := archivePayload{
				MetricCode: metricCode, BizDate: bizDate.Format("2006-01-02"),
				DayMax: dayMax, DayMin: dayMin, DayAvg: dayAvg, DayLast: dayLast,
				StatValue: statValue, WaterLevel: water, Level: level, PointCount: pointCount,
				Source: source, Corrected: corrected, CorrectReason: correctReason.String,
				CorrectedBy: correctedBy.String, CorrectedAt: correctedAt,
				CreatedAt: &createdAt, UpdatedAt: &updatedAt,
			}
			if snapshot != nil {
				s := string(snapshot)
				payload.OriginalSnapshot = &s
			}
			if err := fn(payload); err != nil {
				return err
			}
		}
		return rows.Err()
	case TableRT:
		rows, err := s.db.WithContext(ctx).Table(p.Partition).
			Select("metric_code, ts, raw_value, clean_value, is_anomaly, anomaly_reason, judge_level, source, created_at").
			Where("ts >= ? AND ts < ?", p.From, p.To).
			Order("metric_code, ts").Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				metricCode    string
				ts            time.Time
				raw, clean    *float64
				isAnomaly     bool
				anomalyReason sql.NullString
				judgeLevel    string
				source        string
				createdAt     time.Time
			)
			if err := rows.Scan(&metricCode, &ts, &raw, &clean, &isAnomaly, &anomalyReason,
				&judgeLevel, &source, &createdAt); err != nil {
				return err
			}
			payload := archivePayload{
				MetricCode: metricCode, TS: &ts, RawValue: raw, CleanValue: clean,
				IsAnomaly: isAnomaly, AnomalyReason: anomalyReason.String,
				JudgeLevel: judgeLevel, Source: source, CreatedAt: &createdAt,
			}
			if err := fn(payload); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	return fmt.Errorf("不支持的归档表: %s", p.Table)
}

func (s *ArchiveService) failArchive(ctx context.Context, id int64, cause error) {
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&persistence.MetricArchiveRun{}).Where("id = ?", id).
		Updates(map[string]any{"status": "failed", "error_msg": cause.Error(), "finished_at": now}).Error
}

// Restore 把归档文件回填到在线分区（幂等：主键冲突跳过）。
func (s *ArchiveService) Restore(ctx context.Context, archiveID int64) (int64, error) {
	if !s.available() {
		return 0, fmt.Errorf("归档服务未启用")
	}
	var run persistence.MetricArchiveRun
	if err := s.db.WithContext(ctx).First(&run, archiveID).Error; err != nil {
		return 0, err
	}
	if !supportedArchiveTable(run.TableName) || !validIdent(run.Partition) {
		return 0, fmt.Errorf("归档记录表或分区非法: %s.%s", run.TableName, run.Partition)
	}
	if run.FileRef == "" || !s.files.Exists(run.FileRef) {
		return 0, fmt.Errorf("归档文件不存在: %s", run.FileRef)
	}
	if err := s.ensurePartition(ctx, run.TableName, run.Partition, run.FromDate); err != nil {
		return 0, err
	}

	rc, err := s.files.Open(run.FileRef)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	zr, err := gzip.NewReader(rc)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	dayBatch := make([]persistence.MetricValueDay, 0, archiveBatchSize)
	rtBatch := make([]persistence.MetricValueRT, 0, archiveBatchSize)
	var inserted int64
	flush := func() error {
		if len(dayBatch) > 0 {
			if err := s.db.WithContext(ctx).Table(run.Partition).
				Clauses(clause.OnConflict{DoNothing: true}).
				CreateInBatches(&dayBatch, archiveBatchSize).Error; err != nil {
				return err
			}
			inserted += int64(len(dayBatch))
			dayBatch = dayBatch[:0]
		}
		if len(rtBatch) > 0 {
			if err := s.db.WithContext(ctx).Table(run.Partition).
				Clauses(clause.OnConflict{DoNothing: true}).
				CreateInBatches(&rtBatch, archiveBatchSize).Error; err != nil {
				return err
			}
			inserted += int64(len(rtBatch))
			rtBatch = rtBatch[:0]
		}
		return nil
	}

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var payload archivePayload
		if err := json.Unmarshal(line, &payload); err != nil {
			continue
		}
		if run.TableName == TableDay {
			dayBatch = append(dayBatch, payloadsToDayRows([]archivePayload{payload})...)
			if len(dayBatch) >= archiveBatchSize {
				if err := flush(); err != nil {
					return inserted, err
				}
			}
			continue
		}
		rtBatch = append(rtBatch, payloadsToRTRows([]archivePayload{payload})...)
		if len(rtBatch) >= archiveBatchSize {
			if err := flush(); err != nil {
				return inserted, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return inserted, err
	}
	if err := flush(); err != nil {
		return inserted, err
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&persistence.MetricArchiveRun{}).Where("id = ?", run.ID).
		Updates(map[string]any{
			"status": "restored", "online_dropped": false, "restored_at": now, "error_msg": "",
		}).Error; err != nil {
		return inserted, err
	}
	return inserted, nil
}

// ensurePartition 复用初始化脚本的 ensure_partitions 创建目标月份分区。
func (s *ArchiveService) ensurePartition(ctx context.Context, table, partition string, from time.Time) error {
	if !supportedArchiveTable(table) || !validIdent(partition) {
		return fmt.Errorf("非法分区标识: %s.%s", table, partition)
	}
	expected := fmt.Sprintf("%s_y%sm%s", table, from.Format("2006"), from.Format("01"))
	if expected != partition {
		return fmt.Errorf("仅支持规范命名的分区恢复: %s (期望 %s)", partition, expected)
	}
	return s.db.WithContext(ctx).Exec(
		fmt.Sprintf("SELECT ensure_partitions('%s', ?, ?)", table), from, from).Error
}

// QueryArchived 按指标与日期区间从归档文件按需读取，返回与在线日数据一致的结构。
// 未命中归档时返回空结果。
func (s *ArchiveService) QueryArchived(ctx context.Context, metricCode string, from, to time.Time) ([]archivePayload, error) {
	if !s.available() {
		return nil, nil
	}
	q := s.db.WithContext(ctx).Model(&persistence.MetricArchiveIndex{}).
		Where("biz_date >= ? AND biz_date <= ?", clock.DayStart(from), clock.DayStart(to))
	if metricCode != "" {
		q = q.Where("metric_code = ?", metricCode)
	}
	var indexes []persistence.MetricArchiveIndex
	if err := q.Order("biz_date, metric_code").Limit(50000).Find(&indexes).Error; err != nil {
		return nil, err
	}
	if len(indexes) == 0 {
		return nil, nil
	}
	// 按文件聚合，避免重复打开同一归档。
	byFile := make(map[string][]persistence.MetricArchiveIndex)
	for _, idx := range indexes {
		byFile[idx.FileRef] = append(byFile[idx.FileRef], idx)
	}
	out := make([]archivePayload, 0, len(indexes))
	for fileRef, group := range byFile {
		rc, err := s.files.Open(fileRef)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(zr)
		zr.Close()
		if err != nil {
			return nil, err
		}
		for _, idx := range group {
			if idx.ByteOffset+int64(idx.RowBytes) > int64(len(content)) {
				continue
			}
			line := content[idx.ByteOffset : idx.ByteOffset+int64(idx.RowBytes)]
			var payload archivePayload
			if err := json.Unmarshal(bytes.TrimSpace(line), &payload); err != nil {
				continue
			}
			out = append(out, payload)
		}
	}
	return out, nil
}
