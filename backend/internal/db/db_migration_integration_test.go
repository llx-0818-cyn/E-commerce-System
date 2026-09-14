package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	appdb "feedsystem_video_go/internal/db"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	drivermysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestLegacyAutoMigrateDoesNotAlterMigrationOwnedTables(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Schema 所有权集成测试")
	}

	adminConfig, err := drivermysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatalf("parse MIGRATION_TEST_DSN: %v", err)
	}
	adminConfig.DBName = ""
	adminConfig.MultiStatements = true
	adminConfig.ParseTime = true

	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()

	databaseName := fmt.Sprintf("feedsystem_schema_owner_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	defer func() {
		if _, dropErr := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); dropErr != nil {
			t.Errorf("drop test database: %v", dropErr)
		}
	}()

	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()

	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrationItems, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(migrationItems) {
		t.Fatalf("embedded migrations Up() = %d, %v; want %d, nil", count, err, len(migrationItems))
	}

	tables := []string{"orders", "product_skus", "payment_records"}
	before := showCreateTables(t, database, tables)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	if err := appdb.AutoMigrateLegacy(gormDB); err != nil {
		t.Fatalf("first AutoMigrateLegacy() error = %v", err)
	}
	if got := showCreateTables(t, database, tables); !equalCreateTables(got, before) {
		t.Fatalf("migration-owned tables changed after application startup:\nbefore=%v\nafter=%v", before, got)
	}

	if err := appdb.AutoMigrateLegacy(gormDB); err != nil {
		t.Fatalf("second AutoMigrateLegacy() error = %v", err)
	}
	if got := showCreateTables(t, database, tables); !equalCreateTables(got, before) {
		t.Fatalf("migration-owned tables changed after repeated startup:\nbefore=%v\nafter=%v", before, got)
	}
}

func showCreateTables(t *testing.T, database *sql.DB, tables []string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		var name, createSQL string
		if err := database.QueryRow("SHOW CREATE TABLE `"+table+"`").Scan(&name, &createSQL); err != nil {
			t.Fatalf("SHOW CREATE TABLE %s: %v", table, err)
		}
		result[name] = createSQL
	}
	return result
}

func equalCreateTables(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		if right[name] != value {
			return false
		}
	}
	return true
}
