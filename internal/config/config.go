package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Bot      BotConfig
	Database DatabaseConfig
	App      AppConfig
	Admin    AdminConfig
}

type BotConfig struct {
	Token       string
	AdminID     int64
	WorkGroupID int64 // 可选:历史 .env 兼容字段, 实际工作群以 bot_settings 表为准
}

type DatabaseConfig struct {
	Path     string // 主 Bot SQLite 文件路径
	CloneDir string // 克隆 Bot SQLite 文件存放目录
}

type AppConfig struct {
	LogLevel       string
	CachePrefix    string
	WorkerPoolSize int
}

type AdminConfig struct {
	Port   int
	Domain string
}

func loadDotEnv() {
	for _, p := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
	}
	_ = godotenv.Load()
}

// Load 从环境变量（及仓库根目录 .env）加载配置
func Load() (*Config, error) {
	loadDotEnv()

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("缺少环境变量 BOT_TOKEN")
	}
	adminID, err := parseInt64("BOT_ADMIN_ID")
	if err != nil {
		return nil, err
	}
	// 工作群从必填降级为可选, 缺失或非法都视为 0(未绑定),
	// 实际工作群从每个 Bot 自己的 bot_settings 表读取
	workGroupID := parseInt64Optional("BOT_WORK_GROUP_ID")

	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = "data/chavipbot.db"
	}
	cloneDir := os.Getenv("CLONE_DB_DIR")
	if cloneDir == "" {
		cloneDir = "data/clones"
	}

	workerSize, err := parseInt("APP_WORKER_POOL_SIZE", 100)
	if err != nil {
		return nil, err
	}
	adminPort, err := parseInt("ADMIN_PORT", 4949)
	if err != nil {
		return nil, err
	}

	logLevel := os.Getenv("APP_LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	cachePrefix := os.Getenv("APP_CACHE_PREFIX")
	if cachePrefix == "" {
		cachePrefix = "tgban:"
	}
	adminDomain := os.Getenv("ADMIN_DOMAIN")
	if adminDomain == "" {
		adminDomain = "localhost"
	}

	return &Config{
		Bot: BotConfig{
			Token:       token,
			AdminID:     adminID,
			WorkGroupID: workGroupID,
		},
		Database: DatabaseConfig{
			Path:     dbPath,
			CloneDir: cloneDir,
		},
		App: AppConfig{
			LogLevel:       logLevel,
			CachePrefix:    cachePrefix,
			WorkerPoolSize: workerSize,
		},
		Admin: AdminConfig{
			Port:   adminPort,
			Domain: adminDomain,
		},
	}, nil
}

func parseInt64(key string) (int64, error) {
	s := os.Getenv(key)
	if s == "" {
		return 0, fmt.Errorf("缺少环境变量 %s", key)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("环境变量 %s 应为整数: %w", key, err)
	}
	return v, nil
}

// parseInt64Optional 可选 int64 环境变量, 缺失或非法返回 0
func parseInt64Optional(key string) int64 {
	s := os.Getenv(key)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseInt(key string, defaultVal int) (int, error) {
	s := os.Getenv(key)
	if s == "" {
		return defaultVal, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("环境变量 %s 应为整数: %w", key, err)
	}
	return v, nil
}
