package datasource

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/sijms/go-ora/v2/configurations"
)

// 实环境 MySQL 密码含 @/#/!/+ 等特殊字符。生产路径改为 Config+NewConnector
// (不走 DSN 字符串拼接), 此测试保证特殊字符密码可构建连接器且字段完整。
func TestMySQLSpecialCharPassword(t *testing.T) {
	conf := mysql.NewConfig()
	conf.User = "sjcj"
	conf.Passwd = "Tcl+nftx123!@#"
	conf.Net = "tcp"
	conf.Addr = "128.6.120.31:3306"
	conf.DBName = "coredb"
	if _, err := mysql.NewConnector(conf); err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	if conf.User != "sjcj" || conf.Passwd != "Tcl+nftx123!@#" || conf.Addr != "128.6.120.31:3306" || conf.DBName != "coredb" {
		t.Fatalf("config fields changed: %+v", conf)
	}
}

// 实环境 Oracle 密码尾部带空格 (HTML 实体 &#x20;)! url.UserPassword 百分百 percent
// 编码后, go-ora 的 url.Parse 能完整恢复原始密码与主机/服务名。
func TestOracleDSNSpecialCharPassword(t *testing.T) {
	u := &url.URL{
		Scheme: "oracle",
		User:   url.UserPassword("sjcj", "sjcj "),
		Host:   "128.6.120.65:1521",
		Path:   "/ygtcs",
	}
	parsed, err := url.Parse(u.String())
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if parsed.User.Username() != "sjcj" {
		t.Fatalf("user mismatch: %q", parsed.User.Username())
	}
	if pass, _ := parsed.User.Password(); pass != "sjcj " {
		t.Fatalf("password mismatch: %q", pass)
	}
	if parsed.Host != "128.6.120.65:1521" || parsed.Path != "/ygtcs" {
		t.Fatalf("host/path mismatch: %q %q", parsed.Host, parsed.Path)
	}
}

// 记录 go-ora v2 的强制约束: DSN 必须带 oracle:// scheme, 无 scheme 时 url.Parse 把用户名当方法 (scheme),
// 导致 Host 为空, SplitHostPort 失败 (M0 真实环境复测时交付的 adapter 恰好缺 scheme, 已修复。)
func TestOracleDSNRequiresScheme(t *testing.T) {
	schemeless := fmt.Sprintf("%s:%s@%s:%d/%s", "sjcj", "sjcj", "128.6.120.65", 1521, "ygtcs")
	if _, err := configurations.ParseConfig(schemeless); err == nil {
		t.Fatal("expected parse error for schemeless dsn")
	}
}
