package constant

// Backend 指定业务仓储使用的数据库方言。
type Backend string

const (
	MySQL      Backend = "mysql"
	PostgreSQL Backend = "postgresql"
)
