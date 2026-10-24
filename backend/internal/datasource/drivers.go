package datasource

// 数据源驱动注册:
//   "mysql"  - adapter.go 具名导入 github.com/go-sql-driver/mysql (ParseDSN) 时注册
//   "oracle" - go-ora 仅在 sql.Open("oracle", ...) 运行时使用, 必须在此 blank import 注册
import (
	_ "github.com/sijms/go-ora/v2"
)

// RegisteredDrivers 列出本包负责注册的驱动名, 供 Spike 01 校验与生产二进制一致的接线路径。
var RegisteredDrivers = []string{"mysql", "oracle"}
