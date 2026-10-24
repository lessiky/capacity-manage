package datasource

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"capacity-manage/backend/internal/config"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/aesgcm"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Config 数据源配置（凭据解密后的临时对象, 禁止落日志明文）
type Config struct {
	Type string `json:"type"` // db / log / ssh

	Driver   string `json:"driver,omitempty"` // oracle / mysql
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Database string `json:"database,omitempty"`
	Service  string `json:"service,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	TestSQL  string `json:"testSql,omitempty"`

	Scheme      string `json:"scheme,omitempty"` // local / sftp
	PathTemplate string `json:"pathTemplate,omitempty"`
	GlobTemplate string `json:"globTemplate,omitempty"`
	Encoding    string `json:"encoding,omitempty"`

	// ssh 执行目标(远程脚本采集): 脚本与参数经 SFTP 落到 TempDir, 执行后删除
	OS      string `json:"os,omitempty"`      // linux / windows
	TempDir string `json:"tempDir,omitempty"` // 目标机临时目录(SFTP 可见的绝对路径)
}

// TestResult 连接测试结果
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Rows    int    `json:"rows,omitempty"`
	Sample  string `json:"sample,omitempty"`
}

// DataSourceAdapter 数据源适配器接口（扩展点: API/监控系统二期实现）
type DataSourceAdapter interface {
	Test(ctx context.Context, cfg *Config) (TestResult, error)
}

// dbAdapter 数据库适配器（database/sql + 驱动, 只读账号执行）
type dbAdapter struct{}

func (dbAdapter) Test(ctx context.Context, cfg *Config) (TestResult, error) {
	var (
		db  *sql.DB
		err error
	)
	switch cfg.Driver {

	case "mysql":
		// Config + NewConnector 直建, 绕过 DSN 字符串拼接, 凭据含 @/#/? 等特殊字符不影响解析
		// 基于 NewConfig() 构建(默认值完整, 如 AllowNativePasswords=true; 直接用字面量会丢失默认值导致认证插件拒绝)
		conf := mysql.NewConfig()
		conf.User = cfg.Username
		conf.Passwd = cfg.Password
		conf.Net = "tcp"
		conf.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
		conf.DBName = cfg.Database
		conf.Timeout = 5 * time.Second
		conf.InterpolateParams = true
		connector, cerr := mysql.NewConnector(conf)
		if cerr != nil {
			return TestResult{OK: false, Message: "DSN 构建失败: " + cerr.Error()}, nil
		}
		db = sql.OpenDB(connector)

	case "oracle":
		// go-ora URL: oracle://user:pass@host:port/service (必须带 scheme; 用户名/密码自动 percent 编码)
		var svc string
		switch {
		case cfg.Service != "":
			svc = cfg.Service
		case cfg.Database != "":
			svc = cfg.Database
		default:
			return TestResult{OK: false, Message: "Oracle 需配置 service 或 database"}, nil
		}
		oracleURL := &url.URL{
			Scheme: "oracle",
			User:   url.UserPassword(cfg.Username, cfg.Password),
			Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Path:   "/" + svc,
		}
		conn, oerr := sql.Open("oracle", oracleURL.String())
		db, err = conn, oerr
	default:
		return TestResult{OK: false, Message: "不支持的数据库驱动: " + cfg.Driver}, nil
	}
	if err != nil {
		return TestResult{OK: false, Message: "打开连接失败: " + err.Error()}, nil
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return TestResult{OK: false, Message: "连接失败: " + err.Error()}, nil
	}
	testSQL := strings.TrimSpace(cfg.TestSQL)
	if testSQL == "" {
		if cfg.Driver == "oracle" {
			testSQL = "SELECT 1 FROM DUAL"
		} else {
			testSQL = "SELECT 1"
		}
	}
	rows, err := db.QueryContext(pingCtx, testSQL)
	if err != nil {
		return TestResult{OK: false, Message: "测试 SQL 执行失败: " + err.Error()}, nil
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return TestResult{OK: true, Message: "连接成功", Rows: n}, nil
}

// logAdapter 日志源适配器（本地路径 / SFTP）
type logAdapter struct{}

// sshAdapter 远程脚本执行目标: 连接测试 = 认证 + 探测临时目录可写
type sshAdapter struct{}

func (sshAdapter) Test(ctx context.Context, cfg *Config) (TestResult, error) {
	client, err := dialSSH(ctx, cfg)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}, nil
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return TestResult{OK: false, Message: "SSH 会话创建失败: " + err.Error()}, nil
	}
	defer session.Close()
	out, err := session.CombinedOutput(sshProbeCommand(cfg.OS, cfg.TempDir))
	if err != nil {
		return TestResult{OK: false, Message: fmt.Sprintf("临时目录不可写(%s): %v %s",
			cfg.TempDir, err, strings.TrimSpace(string(out)))}, nil
	}
	return TestResult{OK: true, Message: fmt.Sprintf("SSH 连接成功, 临时目录可写: %s (%s)", cfg.TempDir, cfg.OS)}, nil
}

// sshProbeCommand 在目标机建目录再删除, 验证账号对临时目录有写权限
func sshProbeCommand(osName, tempDir string) string {
	dir := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(tempDir), "\\", "/"), "/")
	probe := dir + "/capcollect-probe"
	if osName == "windows" {
		return `mkdir "` + probe + `" && rmdir "` + probe + `"`
	}
	q := "'" + strings.ReplaceAll(probe, "'", `'\''`) + "'"
	return "mkdir -p " + q + " && rmdir " + q
}

func (logAdapter) Test(ctx context.Context, cfg *Config, render func(string) string) (TestResult, error) {
	// 连接测试按当前日期渲染模板变量(如 ${prev_trading_day:YYYYMMDD}), 与采集执行同一变量口径;
	// render 为 nil 或渲染失败时退回原始模板(行为同旧版)
	if render != nil {
		cfg.PathTemplate = render(cfg.PathTemplate)
		cfg.GlobTemplate = render(cfg.GlobTemplate)
	}
	switch cfg.Scheme {
	case "local":
		pattern := cfg.PathTemplate
		if cfg.GlobTemplate != "" {
			pattern = cfg.GlobTemplate
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return TestResult{OK: false, Message: "glob 无效: " + err.Error()}, nil
		}
		if len(matches) == 0 {
			return TestResult{OK: false, Message: "无匹配文件: " + pattern + "（已按当前日期渲染, 文件可能尚未生成）"}, nil
		}
		f, err := os.Open(matches[0])
		if err != nil {
			return TestResult{OK: false, Message: "打开文件失败: " + err.Error()}, nil
		}
		defer f.Close()
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		return TestResult{OK: true, Message: "可读, 匹配 " + fmt.Sprint(len(matches)) + " 个文件", Sample: string(buf[:n])}, nil
	case "sftp":
		client, err := dialSFTP(ctx, cfg)
		if err != nil {
			return TestResult{OK: false, Message: err.Error()}, nil
		}
		defer client.Close()
		dirPattern := cfg.PathTemplate
		if dirPattern == "" {
			dirPattern = cfg.GlobTemplate
		}
		dir := filepath.Dir(dirPattern)
		if dir == "" || dir == "." || dir == "\\" {
			dir = "/"
		}
		entries, err := client.ReadDir(dir)
		if err != nil {
			return TestResult{OK: false, Message: fmt.Sprintf("列目录失败: %s (目录: %s)", err.Error(), dir)}, nil
		}
		return TestResult{OK: true, Message: "连接成功, 目录 " + dir + " 含 " + fmt.Sprint(len(entries)) + " 个条目"}, nil
	default:
		return TestResult{OK: false, Message: "不支持的日志源方案: " + cfg.Scheme}, nil
	}
}



type Registry struct {
	db      *gorm.DB
	cipher  *aesgcm.Cipher
	cfg     *config.Config
	log     *slog.Logger
	byType  map[string]DataSourceAdapter
	// renderTpl 模板变量渲染钩子（app 组装时注入, 按当前日期推导 ${biz_date}/${prev_trading_day} 等）;
	// 供日志数据源连接测试使用, 与采集执行保持同一路径口径
	renderTpl func(ctx context.Context, template string) string
}

func NewRegistry(db *gorm.DB, cipher *aesgcm.Cipher, cfg *config.Config, log *slog.Logger) *Registry {
	r := &Registry{db: db, cipher: cipher, cfg: cfg, log: log, byType: map[string]DataSourceAdapter{}}
	_ = r
	return r
}

// SetTemplateRenderer 注入模板渲染函数（渲染失败时应返回原串, 不阻断测试）
func (r *Registry) SetTemplateRenderer(f func(ctx context.Context, template string) string) {
	r.renderTpl = f
}

func (r *Registry) Test(ctx context.Context, src *persistence.DataSource) (TestResult, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(src.Config), &cfg); err != nil {
		return TestResult{OK: false, Message: "配置解析失败: " + err.Error()}, nil
	}
	if err := r.DecryptForCollect(&cfg); err != nil {
		return TestResult{OK: false, Message: "凭据解密失败: " + err.Error()}, nil
	}
	switch cfg.Type {
	case "db":
		return dbAdapter{}.Test(ctx, &cfg)
	case "log":
		var render func(string) string
		if r.renderTpl != nil {
			render = func(tpl string) string { return r.renderTpl(ctx, tpl) }
		}
		return logAdapter{}.Test(ctx, &cfg, render)
	case "ssh":
		return sshAdapter{}.Test(ctx, &cfg)
	default:
		return TestResult{OK: false, Message: "未知数据源类型: " + cfg.Type}, nil
	}
}

// decryptPassword 逐层解密; 兼容历史版本"编辑时密文被二次加密"的存量数据(读取时自动修复)
func (r *Registry) decryptPassword(p string) (string, error) {
	for strings.HasPrefix(p, "enc.v1.") {
		d, err := r.cipher.Decrypt(p)
		if err != nil {
			return "", err
		}
		p = d
	}
	return p, nil
}

func (r *Registry) DecryptForCollect(cfg *Config) error {
	if cfg.Type == "db" && cfg.Password != "" {
		p, err := r.decryptPassword(cfg.Password)
		if err != nil {
			return err
		}
		cfg.Password = p
	}
	if cfg.Type == "log" && cfg.Scheme == "sftp" && cfg.Password != "" {
		p, err := r.decryptPassword(cfg.Password)
		if err != nil {
			return err
		}
		cfg.Password = p
	}
	// ssh 执行目标(远程脚本采集)同样持密文, 漏解密会导致用密文当密码认证失败
	if cfg.Type == "ssh" && cfg.Password != "" {
		p, err := r.decryptPassword(cfg.Password)
		if err != nil {
			return err
		}
		cfg.Password = p
	}
	return nil
}

func (r *Registry) UpdateHealth(src *persistence.DataSource, ok bool, errMsg string) {
	now := time.Now()
	updates := map[string]any{}
	if ok {
		updates["health_status"] = "ok"
		updates["last_success_at"] = now
		updates["last_error"] = ""
		if src.Status == "abnormal" {
			updates["status"] = "available"
		}
	} else {
		updates["health_status"] = "error"
		updates["last_error"] = errMsg
	}
	_ = r.db.Model(src).Updates(updates).Error
}

