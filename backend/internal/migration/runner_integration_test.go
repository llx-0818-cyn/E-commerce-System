package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

func TestRunnerEmptyDatabaseRepeatRollbackAndForward(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 MySQL migration 集成测试")
	}

	adminConfig, err := mysql.ParseDSN(adminDSN)
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

	databaseName := fmt.Sprintf("feedsystem_migration_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatalf("create empty test database: %v", err)
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
		t.Fatalf("open empty test database: %v", err)
	}
	defer database.Close()

	source := fstest.MapFS{
		"000001_create_probe.up.sql": {Data: []byte(`
CREATE TABLE migration_probe (
    id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    label VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO migration_probe (id, label) VALUES (1, 'baseline');`)},
		"000001_create_probe.down.sql": {Data: []byte("DROP TABLE migration_probe;")},
		"000002_add_note.up.sql":       {Data: []byte("ALTER TABLE migration_probe ADD COLUMN note VARCHAR(64) NULL;")},
		"000002_add_note.down.sql":     {Data: []byte("ALTER TABLE migration_probe DROP COLUMN note;")},
	}
	runner, err := NewRunner(database, source)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	applied, err := runner.Up(ctx)
	if err != nil || applied != 2 {
		t.Fatalf("first Up() = %d, %v; want 2, nil", applied, err)
	}
	assertColumnExists(t, database, databaseName, "migration_probe", "note", true)

	applied, err = runner.Up(ctx)
	if err != nil || applied != 0 {
		t.Fatalf("second Up() = %d, %v; want 0, nil", applied, err)
	}

	rolledBack, err := runner.Down(ctx, 1)
	if err != nil || rolledBack != 1 {
		t.Fatalf("Down(1) = %d, %v; want 1, nil", rolledBack, err)
	}
	assertColumnExists(t, database, databaseName, "migration_probe", "note", false)

	applied, err = runner.Up(ctx)
	if err != nil || applied != 1 {
		t.Fatalf("compensating Up() = %d, %v; want 1, nil", applied, err)
	}
	assertColumnExists(t, database, databaseName, "migration_probe", "note", true)

	statuses, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statuses) != 2 || !statuses[0].Applied || !statuses[1].Applied || statuses[0].Dirty || statuses[1].Dirty {
		t.Fatalf("Status() = %#v", statuses)
	}
}

func assertColumnExists(t *testing.T, database *sql.DB, schema, table, column string, want bool) {
	t.Helper()
	if strings.ContainsAny(schema+table+column, "`'\"") {
		t.Fatal("unsafe integration test identifier")
	}
	var count int
	err := database.QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?",
		schema,
		table,
		column,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if got := count == 1; got != want {
		t.Fatalf("column %s.%s.%s exists = %v, want %v", schema, table, column, got, want)
	}
}
