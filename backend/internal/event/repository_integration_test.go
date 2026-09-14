package event_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestRepositoryBusinessStateAndOutboxShareTransaction(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Outbox 事务集成测试")
	}

	database, cleanup := openEventTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	migrationItems, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(migrationItems) {
		t.Fatalf("migration Up() = %d, %v; want %d, nil", count, err, len(migrationItems))
	}
	if _, err := database.ExecContext(ctx, `
CREATE TABLE transaction_probe (
    id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    state VARCHAR(32) NOT NULL
) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create transaction probe: %v", err)
	}

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, err := event.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	forcedRollback := errors.New("forced business rollback")
	err = repository.Transact(ctx, func(tx *gorm.DB, outbox *event.Writer) error {
		if err := tx.Exec("INSERT INTO transaction_probe (id, state) VALUES (?, ?)", 1, "created").Error; err != nil {
			return err
		}
		if _, err := outbox.Append(ctx, event.DomainEvent{
			EventID:       "evt-rollback-1",
			AggregateType: "probe",
			AggregateID:   "1",
			EventType:     "probe.created",
			Payload:       map[string]any{"probe_id": 1},
		}); err != nil {
			return err
		}
		return forcedRollback
	})
	if !errors.Is(err, forcedRollback) {
		t.Fatalf("rollback Transact() error = %v, want %v", err, forcedRollback)
	}
	assertRowCount(t, database, "transaction_probe", 0)
	assertRowCount(t, database, "outbox_events", 0)

	err = repository.Transact(ctx, func(tx *gorm.DB, outbox *event.Writer) error {
		if err := tx.Exec("INSERT INTO transaction_probe (id, state) VALUES (?, ?)", 2, "created").Error; err != nil {
			return err
		}
		_, err := outbox.Append(ctx, event.DomainEvent{
			EventID:       "evt-commit-1",
			AggregateType: "probe",
			AggregateID:   "2",
			EventType:     "probe.created",
			Payload:       map[string]any{"probe_id": 2},
		})
		return err
	})
	if err != nil {
		t.Fatalf("commit Transact() error = %v", err)
	}
	assertRowCount(t, database, "transaction_probe", 1)
	assertRowCount(t, database, "outbox_events", 1)

	var status, payload string
	if err := database.QueryRowContext(ctx, "SELECT status, payload_json FROM outbox_events WHERE event_id = ?", "evt-commit-1").Scan(&status, &payload); err != nil {
		t.Fatalf("query committed outbox: %v", err)
	}
	if status != event.OutboxStatusPending || payload != `{"probe_id": 2}` && payload != `{"probe_id":2}` {
		t.Fatalf("committed outbox status/payload = %q / %q", status, payload)
	}
}

func openEventTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
	t.Helper()
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

	databaseName := fmt.Sprintf("feedsystem_outbox_tx_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create temporary database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open temporary database: %v", err)
	}
	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop temporary database: %v", err)
		}
		adminDB.Close()
	}
	return database, cleanup
}

func assertRowCount(t *testing.T, database *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM `" + table + "`").Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s row count = %d, want %d", table, count, want)
	}
}
