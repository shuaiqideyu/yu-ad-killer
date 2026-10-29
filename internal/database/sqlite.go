package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"telegram-ban-bot/internal/config"
	"telegram-ban-bot/internal/database/models"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const sqliteCacheKiB = 20000
const sqliteMmapBytes = 128 << 20

// InitOptions 控制不同 Bot 实例的初始化差异
type InitOptions struct {
	// IsMain 表示当前数据库属于主 Bot,需要额外迁移 clone_bots 表
	IsMain bool
}

// InitSQLite 兼容旧调用,默认按主 Bot 初始化(包含 clone_bots 表)
func InitSQLite(cfg *config.DatabaseConfig) (*gorm.DB, error) {
	return InitSQLiteWithOptions(cfg.Path, InitOptions{IsMain: true})
}

// InitSQLiteWithOptions 按指定路径打开 SQLite,并按角色迁移所需表
func InitSQLiteWithOptions(dbPath string, opts InitOptions) (*gorm.DB, error) {
	if dbPath == "" {
		dbPath = "data/chavipbot.db"
	}

	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("创建SQLite数据目录失败: %w", err)
		}
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Silent),
		PrepareStmt:            true,
		SkipDefaultTransaction: true,
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("连接SQLite失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取数据库连接失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		fmt.Sprintf("PRAGMA cache_size=-%d", sqliteCacheKiB),
		"PRAGMA temp_store=MEMORY",
		fmt.Sprintf("PRAGMA mmap_size=%d", sqliteMmapBytes),
	}
	for _, pragma := range pragmas {
		if err := db.Exec(pragma).Error; err != nil {
			return nil, fmt.Errorf("执行 %s 失败: %w", pragma, err)
		}
	}

	if err := autoMigrate(db, opts); err != nil {
		return nil, fmt.Errorf("数据库表结构迁移失败: %w", err)
	}

	log.Printf("[SQLite] 数据库连接成功: %s (主Bot=%v)", dbPath, opts.IsMain)
	return db, nil
}

func autoMigrate(db *gorm.DB, opts InitOptions) error {
	commonTables := []interface{}{
		&models.User{},
		&models.BioHistory{},
		&models.Keyword{},
		&models.BanRecord{},
		&models.Admin{},
		&models.BotSettings{},
		&models.Whitelist{},
	}
	if err := db.AutoMigrate(commonTables...); err != nil {
		return err
	}
	if opts.IsMain {
		if err := db.AutoMigrate(&models.CloneBot{}); err != nil {
			return err
		}
	}
	return nil
}
