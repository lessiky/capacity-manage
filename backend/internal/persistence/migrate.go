package persistence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"capacity-manage/backend/internal/pkg/clock"

	"gorm.io/gorm"
)

// Migrate 按序执行版本化 DDL: db/migrations 目录下全部 *.sql(按文件名升序)或单个迁移文件。
// 迁移 SQL 含多语句与 $$ 函数体, 须切分后在单个事务内顺序执行（pgx 扩展协议不支持一条 SQL 多语句）。
// 幂等性由 migration_state 表管理: 已应用的迁移文件直接跳过。
func Migrate(ctx context.Context, db *gorm.DB, path string) error {
	if err := db.Exec("CREATE TABLE IF NOT EXISTS migration_state (name varchar(128) PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())").Error; err != nil {
		return err
	}
	entries, err := migrationEntries(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		var n int64
		if err := db.Model(&struct{}{}).Table("migration_state").Where("name = ?", e.name).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		raw, err := os.ReadFile(e.file)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.file, err)
		}
		stmts := splitSQLStatements(string(raw))
		err = db.Transaction(func(tx *gorm.DB) error {
			for i, stmt := range stmts {
				one := strings.TrimSpace(stmt)
				if strings.EqualFold(one, "BEGIN") || strings.EqualFold(one, "COMMIT") {
					continue // 事务由 GORM Transaction 管理
				}
				if isCommentOnly(one) {
					continue
				}
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("stmt %d: %w", i+1, err)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("apply %s: %w", e.name, err)
		}
		if err := db.Exec("INSERT INTO migration_state(name) VALUES (?)", e.name).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrationEntries 返回有序迁移文件列表: path 为目录时取全部 *.sql(按文件名升序), 为文件时单条。
func migrationEntries(path string) ([]struct {
	name, file string
}, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return []struct {
			name, file string
		}{{name, path}}, nil
	}
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []struct {
		name, file string
	}
	for _, e := range dirEntries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".sql")
		out = append(out, struct {
			name, file string
		}{name, filepath.Join(path, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// splitSQLStatements 将 SQL 文本切分为可执行语句。
// 识别 $$ 美元引号体（函数/DO 块，内部分号不切分）、单引号字符串（'' 转义）、
// 行注释 -- 与块注释 /* */（注释内分号不切分）。
func splitSQLStatements(src string) []string {
	var (
		stmts []string
		cur   strings.Builder
		inDL  bool
		inStr bool
		inLC  bool
		inBC  bool
	)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		var next byte
		if i+1 < len(src) {
			next = src[i+1]
		}
		switch {
		case inLC:
			cur.WriteByte(c)
			if c == '\n' {
				inLC = false
			}
		case inBC:
			if c == '*' && next == '/' {
				cur.WriteString("*/")
				i++
				inBC = false
			} else {
				cur.WriteByte(c)
			}
		case inStr:
			if c == '\'' {
				if next == '\'' {
					cur.WriteString("''")
					i++
				} else {
					cur.WriteByte(c)
					inStr = false
				}
			} else {
				cur.WriteByte(c)
			}
		case inDL:
			if c == '$' && next == '$' {
				cur.WriteString("$$")
				i++
				inDL = false
			} else {
				cur.WriteByte(c)
			}
		default:
			switch {
			case c == '-' && next == '-':
				cur.WriteString("--")
				i++
				inLC = true
			case c == '/' && next == '*':
				cur.WriteString("/*")
				i++
				inBC = true
			case c == '\'':
				cur.WriteByte(c)
				inStr = true
			case c == '$' && next == '$':
				cur.WriteString("$$")
				i++
				inDL = true
			case c == ';':
				flush()
			default:
				cur.WriteByte(c)
			}
		}
	}
	flush()
	return stmts
}

// isCommentOnly 判断语句是否只含空白与注释（可安全跳过）。
// 扫描时同样识别 $$ 体与单引号字符串，避免误判字符串内的注释符号。
func isCommentOnly(s string) bool {
	var (
		inDL, inStr, inLC, inBC bool
		found                    bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		var next byte
		if i+1 < len(s) {
			next = s[i+1]
		}
		switch {
		case inLC:
			if c == '\n' {
				inLC = false
			}
		case inBC:
			if c == '*' && next == '/' {
				i++
				inBC = false
			}
		case inStr:
			if c == '\'' {
				if next == '\'' {
					i++
				} else {
					inStr = false
				}
			}
		case inDL:
			if c == '$' && next == '$' {
				i++
				inDL = false
			}
		default:
			switch {
			case c == '-' && next == '-':
				inLC = true
				i++
			case c == '/' && next == '*':
				inBC = true
				i++
			case c == '\'':
				inStr = true
			case c == '$' && next == '$':
				inDL = true
				i++
			default:
				if !unicode.IsSpace(rune(c)) {
					found = true
				}
			}
		}
	}
	return !found
}

// EnsurePartitions 确保 rt/day 分区表在 [now, now+1month] 存在分区（调度每日调用）
func EnsurePartitions(ctx context.Context, db *gorm.DB) error {
	from := clock.DayStart(time.Now())
	to := from.AddDate(0, 1, 0)
	if err := db.Exec("SELECT ensure_partitions('metric_value_rt', ?, ?)", from, to).Error; err != nil {
		return err
	}
	return db.Exec("SELECT ensure_partitions('metric_value_day', ?, ?)", from, to).Error
}
