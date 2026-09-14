package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestIntentionallyFailingConsumerIsQueryableAndNotSilentlyACKed(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行失败消费者集成测试")
	}
	database, cleanup := openReliableConsumerTestDatabase(t, adminDSN)
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
	failures, err := event.NewFailureRepository(gormDB)
	if err != nil {
		t.Fatalf("NewFailureRepository() error = %v", err)
	}
	deadLetters := &fakeDeadLetterSink{}
	handler, err := NewReliableDeliveryHandler("forced-failure-consumer", "content.queue", 3, failures, deadLetters)
	if err != nil {
		t.Fatalf("NewReliableDeliveryHandler() error = %v", err)
	}
	acknowledger := &fakeAcknowledger{}
	delivery := failedDelivery(acknowledger, "content.queue", 3)
	wantFailure := errors.New("forced permanent consumer failure")

	if err := handler.Handle(ctx, delivery, "handle", func(context.Context, amqp.Delivery) error {
		return wantFailure
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if acknowledger.acks != 1 || acknowledger.nacks != 0 || len(deadLetters.payloads) != 1 {
		t.Fatalf("ack/nack/DLQ = %d/%d/%d, want 1/0/1", acknowledger.acks, acknowledger.nacks, len(deadLetters.payloads))
	}

	items, err := failures.ListUnresolved(ctx, event.FailureQuery{ConsumerName: "forced-failure-consumer", Limit: 10})
	if err != nil {
		t.Fatalf("ListUnresolved() error = %v", err)
	}
	if len(items) != 1 || items[0].EventID != "event-1" || items[0].Attempts != 4 || items[0].LastError != wantFailure.Error() {
		t.Fatalf("unresolved failures = %#v", items)
	}
	var gotPayload, wantPayload any
	if err := json.Unmarshal(items[0].Payload(), &gotPayload); err != nil {
		t.Fatalf("decode failure payload: %v", err)
	}
	if err := json.Unmarshal(delivery.Body, &wantPayload); err != nil {
		t.Fatalf("decode delivery payload: %v", err)
	}
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("failure payload = %v, want %v", gotPayload, wantPayload)
	}

	resolved, err := failures.Resolve(ctx, items[0].ID, "测试补偿完成", time.Now().UTC())
	if err != nil || !resolved {
		t.Fatalf("Resolve() = %v, %v; want true, nil", resolved, err)
	}
	items, err = failures.ListUnresolved(ctx, event.FailureQuery{ConsumerName: "forced-failure-consumer", Limit: 10})
	if err != nil || len(items) != 0 {
		t.Fatalf("resolved ListUnresolved() = %#v, %v; want empty", items, err)
	}
}

func openReliableConsumerTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_failed_consumer_test_%d", time.Now().UnixNano())
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
