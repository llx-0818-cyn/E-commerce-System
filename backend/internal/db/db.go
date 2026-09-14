package db

import (
	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/message"
	"feedsystem_video_go/internal/social"
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func NewDB(dbcfg config.DatabaseConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		dbcfg.User, dbcfg.Password, dbcfg.Host, dbcfg.Port, dbcfg.DBName)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	return db, nil
}

// LegacyAutoMigrateModels 仅返回仍由 GORM 管理的兼容表。
//
// 新增的 content、shop、catalog、cart、inventory、order、payment、promotion
// 和通用 event 表不得加入此列表；它们的唯一 Schema 事实源是 migrations/ SQL。
func LegacyAutoMigrateModels() []any {
	return []any{
		&account.Account{}, &social.Social{}, &message.Message{},
	}
}

// AutoMigrateLegacy 兼容尚未纳入 SQL migration 的非媒体基础表。
func AutoMigrateLegacy(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("legacy auto migrate database is nil")
	}
	return db.AutoMigrate(LegacyAutoMigrateModels()...)
}

func CloseDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
