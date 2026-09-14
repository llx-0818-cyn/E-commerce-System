package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
)

func TestReliableEventMigrationConstraintsAndIndexes(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行可靠事件 Migration 集成测试")
	}

	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "reliable_events")
	defer cleanup()

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
		t.Fatalf("Up() = %d, %v; want %d, nil", count, err, len(migrationItems))
	}

	assertIndex(t, database, schema, "outbox_events", "uq_outbox_event_id", true, []string{"event_id"})
	assertIndex(t, database, schema, "outbox_events", "idx_outbox_poll", false, []string{"status", "next_attempt_at", "id"})
	assertIndex(t, database, schema, "outbox_events", "idx_outbox_claim", false, []string{"status", "claimed_until", "id"})
	assertIndex(t, database, schema, "outbox_events", "idx_outbox_retention", false, []string{"status", "published_at", "id"})
	assertIndex(t, database, schema, "processed_events", "PRIMARY", true, []string{"consumer_name", "event_id"})
	assertIndex(t, database, schema, "processed_events", "idx_processed_retention", false, []string{"processed_at", "consumer_name", "event_id"})
	assertIndex(t, database, schema, "failed_events", "uq_failed_consumer_event", true, []string{"consumer_name", "event_id"})
	assertIndex(t, database, schema, "failed_events", "idx_failed_unresolved", false, []string{"resolved_at", "last_failed_at", "id"})

	_, err = database.ExecContext(ctx, `
INSERT INTO outbox_events
    (event_id, aggregate_type, aggregate_id, event_type, payload_json)
VALUES
    ('evt-outbox-1', 'post', '42', 'content.published', JSON_OBJECT('post_id', 42))`)
	if err != nil {
		t.Fatalf("insert first outbox event: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO outbox_events
    (event_id, aggregate_type, aggregate_id, event_type, payload_json)
VALUES
    ('evt-outbox-1', 'post', '43', 'content.published', JSON_OBJECT('post_id', 43))`)
	assertDuplicateKey(t, err, "outbox event_id")

	_, err = database.ExecContext(ctx, `
INSERT INTO processed_events
    (consumer_name, event_id, event_type)
VALUES
    ('timeline-consumer', 'evt-processed-1', 'content.published')`)
	if err != nil {
		t.Fatalf("insert first processed event: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO processed_events
    (consumer_name, event_id, event_type)
VALUES
    ('timeline-consumer', 'evt-processed-1', 'content.published')`)
	assertDuplicateKey(t, err, "processed consumer/event")

	_, err = database.ExecContext(ctx, `
INSERT INTO failed_events
    (consumer_name, event_id, event_type, payload_json, failure_stage, last_error)
VALUES
    ('timeline-consumer', 'evt-failed-1', 'content.published', JSON_OBJECT('post_id', 42), 'handle', 'forced failure')`)
	if err != nil {
		t.Fatalf("insert first failed event: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO failed_events
    (consumer_name, event_id, event_type, payload_json, failure_stage, last_error)
VALUES
    ('timeline-consumer', 'evt-failed-1', 'content.published', JSON_OBJECT('post_id', 42), 'handle', 'duplicate failure')`)
	assertDuplicateKey(t, err, "failed consumer/event")

	assertQueryUsesIndex(t, database, "SELECT id FROM outbox_events FORCE INDEX (idx_outbox_poll) WHERE status = 'pending' AND next_attempt_at <= CURRENT_TIMESTAMP(6) ORDER BY status, next_attempt_at, id LIMIT 100", "idx_outbox_poll")
	assertQueryUsesIndex(t, database, "SELECT id FROM failed_events FORCE INDEX (idx_failed_unresolved) WHERE resolved_at IS NULL ORDER BY resolved_at, last_failed_at, id LIMIT 100", "idx_failed_unresolved")
}

func openTemporaryDatabase(t *testing.T, adminDSN, suffix string) (*sql.DB, string, func()) {
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
	schema := fmt.Sprintf("feedsystem_%s_test_%d", suffix, time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create temporary database: %v", err)
	}

	testConfig := *adminConfig
	testConfig.DBName = schema
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + schema + "`")
		adminDB.Close()
		t.Fatalf("open temporary database: %v", err)
	}

	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + schema + "`"); err != nil {
			t.Errorf("drop temporary database: %v", err)
		}
		adminDB.Close()
	}
	return database, schema, cleanup
}

func assertIndex(t *testing.T, database *sql.DB, schema, table, index string, unique bool, wantColumns []string) {
	t.Helper()
	rows, err := database.Query(`
SELECT column_name, non_unique
FROM information_schema.statistics
WHERE table_schema = ? AND table_name = ? AND index_name = ?
ORDER BY seq_in_index`, schema, table, index)
	if err != nil {
		t.Fatalf("query index %s.%s: %v", table, index, err)
	}
	defer rows.Close()

	var columns []string
	var nonUniqueValues []int
	for rows.Next() {
		var column string
		var nonUnique int
		if err := rows.Scan(&column, &nonUnique); err != nil {
			t.Fatalf("scan index %s.%s: %v", table, index, err)
		}
		columns = append(columns, column)
		nonUniqueValues = append(nonUniqueValues, nonUnique)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index %s.%s: %v", table, index, err)
	}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("index %s.%s columns = %v, want %v", table, index, columns, wantColumns)
	}
	wantNonUnique := 1
	if unique {
		wantNonUnique = 0
	}
	for _, value := range nonUniqueValues {
		if value != wantNonUnique {
			t.Fatalf("index %s.%s non_unique values = %v, want %d", table, index, nonUniqueValues, wantNonUnique)
		}
	}
}

func assertDuplicateKey(t *testing.T, err error, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("duplicate %s insert succeeded", label)
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		t.Fatalf("duplicate %s error = %v, want MySQL 1062", label, err)
	}
}

func assertQueryUsesIndex(t *testing.T, database *sql.DB, query, wantIndex string) {
	t.Helper()
	rows, err := database.Query("EXPLAIN " + query)
	if err != nil {
		t.Fatalf("EXPLAIN query: %v", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("EXPLAIN columns: %v", err)
	}
	values := make([]sql.NullString, len(columns))
	destinations := make([]any, len(columns))
	for index := range values {
		destinations[index] = &values[index]
	}
	var used []string
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			t.Fatalf("scan EXPLAIN: %v", err)
		}
		for index, column := range columns {
			if column == "key" && values[index].Valid {
				used = append(used, values[index].String)
			}
		}
	}
	sort.Strings(used)
	for _, index := range used {
		if index == wantIndex {
			return
		}
	}
	t.Fatalf("EXPLAIN indexes = %v, want %s", used, wantIndex)
}

func migrationCountFrom(items []migration.Migration, version uint64) int {
	count := 0
	for _, item := range items {
		if item.Version >= version {
			count++
		}
	}
	return count
}
