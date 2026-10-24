package config

import (
	"os"
	"strconv"
	"time"
)

// Config 平台运行配置（全部环境变量可覆盖，见 deploy/env/*.env）
type Config struct {
	Env      string // dev/sit/uat/prod
	HTTPPort int

	JWTSecret    string
	JWTAccessTTL time.Duration

	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// CredKey 32 字节 hex（AES-256-GCM 凭据加密）
	CredKey string

	// FileRoot 导出/报表/脚本文件本地存储根目录（MinIO 适配预留）
	FileRoot string

	WorkerURL     string
	WorkerTimeout time.Duration

	AIEnabled bool
	AIBaseURL string
	AIModel   string
	AIKey     string
	AITimeout time.Duration

	SchedulerEnabled bool

	MigrationFile string
	LogLevel       string
}

func Get() *Config {
	return &Config{
		Env:              getEnv("APP_ENV", "dev"),
		HTTPPort:         getEnvInt("HTTP_PORT", 8080),
		JWTSecret:        getEnv("JWT_SECRET", "dev-only-secret-change-me"),
		JWTAccessTTL:     time.Duration(getEnvInt("JWT_ACCESS_TTL_MIN", 120)) * time.Minute,
		DBHost:           getEnv("DB_HOST", "localhost"),
		DBPort:           getEnvInt("DB_PORT", 5432),
		DBUser:           getEnv("DB_USER", "capacity"),
		DBPassword:       getEnv("DB_PASSWORD", "capacity123"),
		DBName:           getEnv("DB_NAME", "capacity"),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:    getEnv("REDIS_PASSWORD", ""),
		RedisDB:          getEnvInt("REDIS_DB", 0),
		CredKey:          getEnv("APP_CRED_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		FileRoot:         getEnv("FILE_ROOT", "./data/files"),
		WorkerURL:        getEnv("WORKER_URL", "http://127.0.0.1:9100"),
		WorkerTimeout:    time.Duration(getEnvInt("WORKER_TIMEOUT_SEC", 310)) * time.Second,
		AIEnabled:        getEnvBool("AI_ENABLED", false),
		AIBaseURL:        getEnv("AI_BASE_URL", ""),
		AIModel:          getEnv("AI_MODEL", ""),
		AIKey:            getEnv("AI_API_KEY", ""),
		AITimeout:        time.Duration(getEnvInt("AI_TIMEOUT_SEC", 60)) * time.Second,
		SchedulerEnabled: getEnvBool("SCHEDULER_ENABLED", true),
		MigrationFile:    getEnv("DB_MIGRATION_FILE", "../db/migrations"),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
	}
}

func (c *Config) DSN() string {
	return "host=" + c.DBHost + " port=" + strconv.Itoa(c.DBPort) +
		" user=" + c.DBUser + " password=" + c.DBPassword +
		" dbname=" + c.DBName + " sslmode=disable TimeZone=Asia/Shanghai"
}

func getEnv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(k string, def bool) bool {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}