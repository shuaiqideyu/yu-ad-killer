package repository

import (
	"path/filepath"
	"testing"

	"telegram-ban-bot/internal/cache"
	"telegram-ban-bot/internal/database"
	"telegram-ban-bot/internal/database/models"
)

func BenchmarkIsBannedSQLite(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{IsMain: false})
	if err != nil {
		b.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		b.Fatal(err)
	}
	defer sqlDB.Close()

	repo := NewUserRepository(db, cache.NewCacheService("bench:"))
	user := &models.User{ID: 42, Username: "u", FirstName: "A"}
	if err := repo.CreateOrUpdate(user); err != nil {
		b.Fatal(err)
	}
	if err := repo.WarmBannedCache(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		banned, err := repo.IsBanned(42)
		if err != nil || banned {
			b.Fatalf("banned=%v err=%v", banned, err)
		}
	}
}

func BenchmarkBotSettingsGet(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "settings.db")
	db, err := database.InitSQLiteWithOptions(dbPath, database.InitOptions{IsMain: false})
	if err != nil {
		b.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		b.Fatal(err)
	}
	defer sqlDB.Close()

	repo := NewBotSettingsRepository(db)
	if err := repo.SetWorkGroup(-100); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := repo.Get()
		if err != nil || got.WorkGroupID != -100 {
			b.Fatalf("got=%v err=%v", got, err)
		}
	}
}
