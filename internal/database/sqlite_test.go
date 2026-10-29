package database

import (
	"path/filepath"
	"telegram-ban-bot/internal/config"
	"telegram-ban-bot/internal/database/models"
	"testing"
)

func TestInitSQLiteCreatesDatabaseAndTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chavipbot.db")

	db, err := InitSQLite(&config.DatabaseConfig{Path: dbPath})
	if err != nil {
		t.Fatalf("InitSQLite() error = %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	defer sqlDB.Close()

	for _, table := range []string{"users", "bio_history", "keywords", "ban_records", "admins"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %q to exist", table)
		}
	}
	if !db.Migrator().HasIndex(&models.BioHistory{}, "idx_bio_user_detected") {
		t.Fatal("bio_history 缺少 (user_id, detected_at) 索引")
	}
}
