package event_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestConsumerGuardAppliesDuplicateMessageOnce(t *testing.T) {
	adminDSN := migrationTestDSN(t)
	database, cleanup := openEventTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepareConsumerDatabase(t, ctx, database)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	guard, err := event.NewConsumerGuard(gormDB, "order-projection-consumer")
	if err != nil {
		t.Fatalf("NewConsumerGuard() error = %v", err)
	}
	consumed := event.ConsumedEvent{
		EventID:       "evt-order-created-1",
		EventType:     "order.created",
		AggregateType: "order",
		AggregateID:   "order-1",
	}
	handler := func(tx *gorm.DB) error {
		return tx.Exec("UPDATE consumer_probe SET effect_count = effect_count + 1 WHERE id = 1").Error
	}

	applied, err := guard.Process(ctx, consumed, handler)
	if err != nil || !applied {
		t.Fatalf("first Process() = %v, %v; want true, nil", applied, err)
	}
	applied, err = guard.Process(ctx, consumed, handler)
	if err != nil || applied {
		t.Fatalf("duplicate Process() = %v, %v; want false, nil", applied, err)
	}
	assertConsumerEffect(t, database, 1)
	assertProcessedCount(t, database, "order-projection-consumer", consumed.EventID, 1)
}

func TestConsumerGuardRollsBackMarkerAndBusinessEffect(t *testing.T) {
	adminDSN := migrationTestDSN(t)
	database, cleanup := openEventTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepareConsumerDatabase(t, ctx, database)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	guard, err := event.NewConsumerGuard(gormDB, "inventory-consumer")
	if err != nil {
		t.Fatalf("NewConsumerGuard() error = %v", err)
	}
	consumed := event.ConsumedEvent{EventID: "evt-inventory-1", EventType: "inventory.changed"}
	wantFailure := errors.New("forced handler failure")
	applied, err := guard.Process(ctx, consumed, func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE consumer_probe SET effect_count = effect_count + 1 WHERE id = 1").Error; err != nil {
			return err
		}
		return wantFailure
	})
	if applied || !errors.Is(err, wantFailure) {
		t.Fatalf("failed Process() = %v, %v", applied, err)
	}
	assertConsumerEffect(t, database, 0)
	assertProcessedCount(t, database, "inventory-consumer", consumed.EventID, 0)

	applied, err = guard.Process(ctx, consumed, func(tx *gorm.DB) error {
		return tx.Exec("UPDATE consumer_probe SET effect_count = effect_count + 1 WHERE id = 1").Error
	})
	if err != nil || !applied {
		t.Fatalf("retry Process() = %v, %v; want true, nil", applied, err)
	}
	assertConsumerEffect(t, database, 1)
	assertProcessedCount(t, database, "inventory-consumer", consumed.EventID, 1)
}

func TestConsumerGuardConcurrentDuplicateOnlyRunsOneHandler(t *testing.T) {
	adminDSN := migrationTestDSN(t)
	database, cleanup := openEventTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepareConsumerDatabase(t, ctx, database)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	guard, err := event.NewConsumerGuard(gormDB, "concurrent-consumer")
	if err != nil {
		t.Fatalf("NewConsumerGuard() error = %v", err)
	}
	consumed := event.ConsumedEvent{EventID: "evt-concurrent-duplicate", EventType: "probe.changed"}

	start := make(chan struct{})
	results := make(chan struct {
		applied bool
		err     error
	}, 2)
	var handlerCalls atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			applied, processErr := guard.Process(ctx, consumed, func(tx *gorm.DB) error {
				handlerCalls.Add(1)
				time.Sleep(5 * time.Millisecond)
				return tx.Exec("UPDATE consumer_probe SET effect_count = effect_count + 1 WHERE id = 1").Error
			})
			results <- struct {
				applied bool
				err     error
			}{applied: applied, err: processErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	appliedCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Process() error = %v", result.err)
		}
		if result.applied {
			appliedCount++
		}
	}
	if appliedCount != 1 || handlerCalls.Load() != 1 {
		t.Fatalf("concurrent applied/handler calls = %d/%d, want 1/1", appliedCount, handlerCalls.Load())
	}
	assertConsumerEffect(t, database, 1)
	assertProcessedCount(t, database, "concurrent-consumer", consumed.EventID, 1)
}

func prepareConsumerDatabase(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
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
CREATE TABLE consumer_probe (
    id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    effect_count BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create consumer probe: %v", err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO consumer_probe (id, effect_count) VALUES (1, 0)"); err != nil {
		t.Fatalf("seed consumer probe: %v", err)
	}
}

func assertConsumerEffect(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT effect_count FROM consumer_probe WHERE id = 1").Scan(&count); err != nil {
		t.Fatalf("query consumer effect: %v", err)
	}
	if count != want {
		t.Fatalf("consumer effect_count = %d, want %d", count, want)
	}
}

func assertProcessedCount(t *testing.T, database *sql.DB, consumerName, eventID string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM processed_events WHERE consumer_name = ? AND event_id = ?", consumerName, eventID).Scan(&count); err != nil {
		t.Fatalf("query processed count: %v", err)
	}
	if count != want {
		t.Fatalf("processed count = %d, want %d", count, want)
	}
}
