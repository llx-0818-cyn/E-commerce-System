package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"
)

func TestMockPaymentMigrationNumbersCallbacksAnomaliesAndIndexes(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Mock 支付 Schema Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "mock_payments")
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	items, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(items) {
		t.Fatalf("Up() = %d, %v; want %d, nil", count, err, len(items))
	}

	assertIndex(t, database, schema, "payment_records", "uq_payment_records_payment_no", true, []string{"payment_no"})
	assertIndex(t, database, schema, "payment_records", "uq_payment_records_order_provider", true, []string{"order_id", "provider"})
	assertIndex(t, database, schema, "payment_records", "uq_payment_records_provider_trade", true, []string{"provider", "provider_trade_no"})
	assertIndex(t, database, schema, "payment_records", "idx_payment_records_buyer_page", false, []string{"buyer_id", "created_at", "id"})
	assertIndex(t, database, schema, "payment_callback_events", "uq_payment_callback_event_id", true, []string{"callback_event_id"})
	assertIndex(t, database, schema, "payment_callback_events", "idx_payment_callbacks_trade_page", false, []string{"provider", "provider_trade_no", "received_at", "id"})
	assertIndex(t, database, schema, "payment_anomalies", "uq_payment_anomalies_callback_type", true, []string{"callback_event_id", "anomaly_type"})
	assertIndex(t, database, schema, "payment_anomalies", "idx_payment_anomalies_unresolved", false, []string{"resolved_at", "created_at", "id"})

	shopID := insertPaymentTestShop(t, database)
	orderOne := insertPaymentTestOrder(t, database, 7, shopID, "PAYMENT-ORDER-1")
	orderTwo := insertPaymentTestOrder(t, database, 8, shopID, "PAYMENT-ORDER-2")
	paymentResult, err := database.ExecContext(ctx, `
INSERT INTO payment_records
    (payment_no,order_id,buyer_id,provider,status,amount_cent,session_token,cashier_url,provider_trade_no)
VALUES ('PAY-20260824-0001', ?, 7, 'mock', 'succeeded', 1000, REPEAT('a',64), '/mock/a', 'MOCK-TRADE-1')`, orderOne)
	if err != nil {
		t.Fatalf("insert payment record: %v", err)
	}
	paymentID, _ := paymentResult.LastInsertId()
	_, err = database.ExecContext(ctx, `
INSERT INTO payment_records (payment_no,order_id,buyer_id,amount_cent,session_token,cashier_url)
VALUES ('PAY-20260824-0001', ?, 8, 1000, REPEAT('b',64), '/mock/b')`, orderTwo)
	assertDuplicateKey(t, err, "payment number")
	_, err = database.ExecContext(ctx, `
INSERT INTO payment_records (payment_no,order_id,buyer_id,amount_cent,session_token,cashier_url,provider_trade_no)
VALUES ('PAY-20260824-0002', ?, 8, 1000, REPEAT('b',64), '/mock/b', 'MOCK-TRADE-1')`, orderTwo)
	assertDuplicateKey(t, err, "provider trade number")
	_, err = database.ExecContext(ctx, `
INSERT INTO payment_records (payment_no,order_id,buyer_id,provider,amount_cent,session_token,cashier_url)
VALUES ('PAY-BAD-PROVIDER', ?, 8, 'wechat', 1000, REPEAT('c',64), '/mock/c')`, orderTwo)
	assertMySQLCode(t, err, 3819, "mock-only provider check")

	if _, err := database.ExecContext(ctx, `
INSERT INTO payment_callback_events
    (callback_event_id,payment_id,provider_trade_no,result,amount_cent,payload_json,process_status,processed_at)
VALUES ('callback-1', ?, 'MOCK-TRADE-1', 'succeeded', 1000, JSON_OBJECT('payment_no','PAY-20260824-0001'), 'applied', CURRENT_TIMESTAMP(6))`, paymentID); err != nil {
		t.Fatalf("insert callback event: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO payment_callback_events
    (callback_event_id,payment_id,provider_trade_no,result,amount_cent,payload_json)
VALUES ('callback-1', ?, 'MOCK-TRADE-1', 'succeeded', 1000, JSON_OBJECT())`, paymentID)
	assertDuplicateKey(t, err, "callback event id")

	if _, err := database.ExecContext(ctx, `
INSERT INTO payment_anomalies
    (payment_id,order_id,payment_no,callback_event_id,anomaly_type,expected_amount_cent,actual_amount_cent,detail,payload_json)
VALUES (?, ?, 'PAY-20260824-0001', 'callback-tampered-1', 'amount_mismatch', 1000, 1, 'callback amount mismatch', JSON_OBJECT('amount_cent',1))`, paymentID, orderOne); err != nil {
		t.Fatalf("insert payment anomaly: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO payment_anomalies
    (payment_id,order_id,payment_no,callback_event_id,anomaly_type,detail,payload_json)
VALUES (?, ?, 'PAY-20260824-0001', 'callback-tampered-1', 'amount_mismatch', 'duplicate anomaly', JSON_OBJECT())`, paymentID, orderOne)
	assertDuplicateKey(t, err, "callback anomaly type")
	assertQueryUsesIndex(t, database, "SELECT id FROM payment_anomalies FORCE INDEX (idx_payment_anomalies_unresolved) WHERE resolved_at IS NULL ORDER BY resolved_at,created_at,id LIMIT 100", "idx_payment_anomalies_unresolved")

	rollbackCount := migrationCountFrom(items, 12)
	if rolledBack, err := runner.Down(ctx, rollbackCount); err != nil || rolledBack != rollbackCount {
		t.Fatalf("Down(%d) = %d, %v; want %d, nil", rollbackCount, rolledBack, err, rollbackCount)
	}
	assertTablesAbsent(t, database, schema, []string{"payment_records", "payment_callback_events", "payment_anomalies"})
}

func insertPaymentTestShop(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (901,'支付测试店','open')")
	if err != nil {
		t.Fatalf("insert payment test shop: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func insertPaymentTestOrder(t *testing.T, database *sql.DB, buyerID uint64, shopID int64, orderNo string) int64 {
	t.Helper()
	submissionResult, err := database.Exec(`
INSERT INTO order_submissions (buyer_id,idempotency_key,request_fingerprint,grand_total_cent)
VALUES (?, ?, REPEAT('a',64), 1000)`, buyerID, "payment-submission-"+orderNo)
	if err != nil {
		t.Fatalf("insert payment test submission: %v", err)
	}
	submissionID, _ := submissionResult.LastInsertId()
	orderResult, err := database.Exec(`
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,total_cent,address_snapshot_json,expire_at)
VALUES (?, ?, ?, ?, 1000, JSON_OBJECT('recipient_name','支付买家'), CURRENT_TIMESTAMP(6)+INTERVAL 30 MINUTE)`, submissionID, orderNo, buyerID, shopID)
	if err != nil {
		t.Fatalf("insert payment test order: %v", err)
	}
	orderID, _ := orderResult.LastInsertId()
	return orderID
}
