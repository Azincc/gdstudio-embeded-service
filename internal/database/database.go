package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Open 初始化内置 SQLite 数据库。
func Open(cfg *config.Config) (*gorm.DB, error) {
	if !strings.EqualFold(strings.TrimSpace(cfg.Database.Driver), "sqlite") {
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Database.Driver)
	}

	if err := ensureSQLiteDir(cfg.Database.DSN); err != nil {
		return nil, err
	}

	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	return db, nil
}

func ensureSQLiteDir(dsn string) error {
	dbPath := sqliteFilePath(dsn)
	if dbPath == "" {
		return nil
	}

	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create sqlite dir %q: %w", dir, err)
	}
	return nil
}

func sqliteFilePath(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}

	if strings.HasPrefix(dsn, "file:") {
		dsn = strings.TrimPrefix(dsn, "file:")
	}
	if idx := strings.Index(dsn, "?"); idx >= 0 {
		dsn = dsn[:idx]
	}

	dsn = strings.TrimSpace(dsn)
	if dsn == ":memory:" || dsn == "" {
		return ""
	}

	return filepath.Clean(dsn)
}
