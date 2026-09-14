package event_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type recordingPublisher struct {
	mu       sync.Mutex
	counts   map[string]int
	failOnce map[string]error
}

func newRecordingPublisher() *recordingPublisher {
	return &recordingPublisher{counts: make(map[string]int), failOnce: make(map[string]error)}
}

func (p *recordingPublisher) Publish(_ context.Context, item event.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[item.EventID]++
	if err, ok := p.failOnce[item.EventID]; ok {
		delete(p.failOnce, item.EventID)
		return err
	}
	return nil
}

func (p *recordingPublisher) count(eventID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[eventID]
}

func TestConcurrentPollersAllowRedeliveryWithoutLosingEvents(t *testing.T) {
	adminDSN := migrationTestDSN(t)
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}

	for index := 1; index <= 20; index++ {
		insertOutbox(t, database, fmt.Sprintf("evt-concurrent-%02d", index), "pending", time.Now().UTC(), nil)
	}
	publisher := newRecordingPublisher()
	configA := event.DefaultPollerConfig("poller-a")
	configA.BatchSize = 10
	configA.BaseBackoff = 2 * time.Millisecond
	configA.MaxBackoff = 20 * time.Millisecond
	configA.PublishedRetention = time.Hour
	configB := configA
	configB.Name = "poller-b"
	pollerA, err := event.NewPoller(gormDB, publisher, configA)
	if err != nil {
		t.Fatalf("NewPoller(A) error = %v", err)
	}
	pollerB, err := event.NewPoller(gormDB, publisher, configB)
	if err != nil {
		t.Fatalf("NewPoller(B) error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, poller := range []*event.Poller{pollerA, pollerB} {
		wait.Add(1)
		go func(current *event.Poller) {
			defer wait.Done()
			<-start
			var pollErr error
			for attempt := 0; attempt < 4; attempt++ {
				result, err := current.PollOnce(ctx)
				if err != nil {
					pollErr = err
					break
				}
				if result.Claimed == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			results <- pollErr
		}(poller)
	}
	close(start)
	wait.Wait()
	close(results)
	for pollErr := range results {
		if pollErr != nil {
			t.Fatalf("concurrent PollOnce() error = %v", pollErr)
		}
	}
	assertOutboxStatusCount(t, database, event.OutboxStatusPublished, 20)
	for index := 1; index <= 20; index++ {
		eventID := fmt.Sprintf("evt-concurrent-%02d", index)
		if got := publisher.count(eventID); got != 1 {
			t.Fatalf("publisher count %s = %d, want 1", eventID, got)
		}
	}

	uncertainID := "evt-uncertain-confirm"
	insertOutbox(t, database, uncertainID, event.OutboxStatusPending, time.Now().UTC(), nil)
	publisher.failOnce[uncertainID] = errors.New("connection closed after broker may have accepted message")
	configUncertain := configA
	configUncertain.Name = "poller-uncertain"
	configUncertain.BatchSize = 1
	pollerUncertain, err := event.NewPoller(gormDB, publisher, configUncertain)
	if err != nil {
		t.Fatalf("NewPoller(uncertain) error = %v", err)
	}
	first, err := pollerUncertain.PollOnce(ctx)
	if err != nil || first.Retried != 1 || publisher.count(uncertainID) != 1 {
		t.Fatalf("first uncertain PollOnce() = %#v, %v, publishes=%d", first, err, publisher.count(uncertainID))
	}
	assertOutboxEventState(t, database, uncertainID, event.OutboxStatusPending, 1)
	time.Sleep(5 * time.Millisecond)
	second, err := pollerUncertain.PollOnce(ctx)
	if err != nil || second.Published != 1 || publisher.count(uncertainID) != 2 {
		t.Fatalf("second uncertain PollOnce() = %#v, %v, publishes=%d", second, err, publisher.count(uncertainID))
	}
	assertOutboxEventState(t, database, uncertainID, event.OutboxStatusPublished, 2)

	oldPublishedAt := time.Now().UTC().Add(-2 * time.Hour)
	recentPublishedAt := time.Now().UTC()
	insertOutbox(t, database, "evt-retention-old", event.OutboxStatusPublished, time.Now().UTC(), &oldPublishedAt)
	insertOutbox(t, database, "evt-retention-new", event.OutboxStatusPublished, time.Now().UTC(), &recentPublishedAt)
	deleted, err := pollerA.CleanupPublished(ctx)
	if err != nil {
		t.Fatalf("CleanupPublished() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("CleanupPublished() deleted = %d, want 1", deleted)
	}
	assertOutboxEventExists(t, database, "evt-retention-old", false)
	assertOutboxEventExists(t, database, "evt-retention-new", true)
}

func migrationTestDSN(t *testing.T) string {
	t.Helper()
	// openEventTestDatabase 会再次验证 DSN；这里复用同一环境开关，避免普通单元测试依赖 MySQL。
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行并发 Outbox Poller 集成测试")
	}
	return adminDSN
}

func insertOutbox(t *testing.T, database *sql.DB, eventID, status string, nextAttempt time.Time, publishedAt *time.Time) {
	t.Helper()
	_, err := database.Exec(`
INSERT INTO outbox_events
    (event_id, aggregate_type, aggregate_id, event_type, payload_json, status, next_attempt_at, published_at)
VALUES
    (?, 'probe', ?, 'probe.created', JSON_OBJECT('event_id', ?), ?, ?, ?)`,
		eventID,
		eventID,
		eventID,
		status,
		nextAttempt,
		publishedAt,
	)
	if err != nil {
		t.Fatalf("insert outbox %s: %v", eventID, err)
	}
}

func assertOutboxStatusCount(t *testing.T, database *sql.DB, status string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE status = ?", status).Scan(&count); err != nil {
		t.Fatalf("count outbox status %s: %v", status, err)
	}
	if count != want {
		t.Fatalf("outbox status %s count = %d, want %d", status, count, want)
	}
}

func assertOutboxEventState(t *testing.T, database *sql.DB, eventID, wantStatus string, wantAttempts int) {
	t.Helper()
	var status string
	var attempts int
	if err := database.QueryRow("SELECT status, attempts FROM outbox_events WHERE event_id = ?", eventID).Scan(&status, &attempts); err != nil {
		t.Fatalf("query outbox %s: %v", eventID, err)
	}
	if status != wantStatus || attempts != wantAttempts {
		t.Fatalf("outbox %s state = %s/%d, want %s/%d", eventID, status, attempts, wantStatus, wantAttempts)
	}
}

func assertOutboxEventExists(t *testing.T, database *sql.DB, eventID string, want bool) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE event_id = ?", eventID).Scan(&count); err != nil {
		t.Fatalf("count outbox %s: %v", eventID, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("outbox %s exists = %v, want %v", eventID, got, want)
	}
}
