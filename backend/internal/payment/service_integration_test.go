package payment_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/payment"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPaymentServiceIdempotentCreateQueryAmountBuyerAndOrderState(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行支付单 Service 集成测试")
	}
	database, cleanup := openPaymentTestDatabase(t, adminDSN)
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	provider, _ := payment.NewMockPaymentProvider("/api/mock-payments/cashier", "0123456789abcdef0123456789abcdef")
	repository, _ := payment.NewRepository(gormDB)
	service, _ := payment.NewService(repository, provider)

	shopID := seedPaymentShop(t, database)
	orderOne := seedPaymentOrder(t, database, 7, shopID, "PAY-SERVICE-ORDER-1", "pending_payment")
	orderTwo := seedPaymentOrder(t, database, 7, shopID, "PAY-SERVICE-ORDER-2", "pending_payment")
	canceledOrder := seedPaymentOrder(t, database, 7, shopID, "PAY-SERVICE-ORDER-3", "canceled")
	if _, err := service.CreatePayment(ctx, payment.CreatePaymentInput{BuyerID: 7, OrderID: uint64(orderOne), AmountCent: 1}); !errors.Is(err, payment.ErrPaymentAmountMismatch) {
		t.Fatalf("CreatePayment(amount mismatch) error = %v", err)
	}
	if _, err := service.CreatePayment(ctx, payment.CreatePaymentInput{BuyerID: 8, OrderID: uint64(orderOne), AmountCent: 1000}); !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("CreatePayment(other buyer) error = %v", err)
	}
	if _, err := service.CreatePayment(ctx, payment.CreatePaymentInput{BuyerID: 7, OrderID: uint64(canceledOrder), AmountCent: 1000}); !errors.Is(err, payment.ErrPaymentOrderState) {
		t.Fatalf("CreatePayment(canceled order) error = %v", err)
	}

	created, err := service.CreatePayment(ctx, payment.CreatePaymentInput{BuyerID: 7, OrderID: uint64(orderOne), AmountCent: 1000})
	if err != nil || created.Payment.Status != payment.StatusPending || created.Payment.CashierURL == "" {
		t.Fatalf("CreatePayment() = %#v, %v", created, err)
	}
	replayed, err := service.CreatePayment(ctx, payment.CreatePaymentInput{BuyerID: 7, OrderID: uint64(orderOne), AmountCent: 1000})
	if err != nil || replayed.Payment.ID != created.Payment.ID || replayed.Payment.PaymentNo != created.Payment.PaymentNo || replayed.Payment.SessionToken != created.Payment.SessionToken {
		t.Fatalf("CreatePayment(replay) = %#v, %v", replayed, err)
	}
	queried, err := service.QueryPayment(ctx, 7, created.Payment.PaymentNo)
	if err != nil || queried.Payment.ID != created.Payment.ID || queried.Payment.CashierURL != created.Payment.CashierURL {
		t.Fatalf("QueryPayment() = %#v, %v", queried, err)
	}
	if _, err := service.QueryPayment(ctx, 8, created.Payment.PaymentNo); !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("QueryPayment(other buyer) error = %v", err)
	}

	input := payment.CreatePaymentInput{BuyerID: 7, OrderID: uint64(orderTwo), AmountCent: 1000}
	start := make(chan struct{})
	results := make([]*payment.PaymentSession, 2)
	errorsByIndex := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByIndex[index] = service.CreatePayment(ctx, input)
		}(index)
	}
	close(start)
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("concurrent payment %d error = %v", index, err)
		}
	}
	if results[0].Payment.ID != results[1].Payment.ID || results[0].Payment.PaymentNo != results[1].Payment.PaymentNo {
		t.Fatalf("concurrent payments differ: %#v %#v", results[0], results[1])
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM payment_records").Scan(&count); err != nil || count != 2 {
		t.Fatalf("payment record count = %d, %v; want 2", count, err)
	}
}

func openPaymentTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_payment_test_%d", time.Now().UnixNano())
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

func seedPaymentShop(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (1001,'支付 Service 店','open')")
	if err != nil {
		t.Fatalf("seed payment shop: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedPaymentOrder(t *testing.T, database *sql.DB, buyerID uint64, shopID int64, orderNo, status string) int64 {
	t.Helper()
	submissionResult, err := database.Exec(`
INSERT INTO order_submissions (buyer_id,idempotency_key,request_fingerprint,grand_total_cent)
VALUES (?, ?, REPEAT('a',64), 1000)`, buyerID, "payment-service-"+orderNo)
	if err != nil {
		t.Fatalf("seed payment submission: %v", err)
	}
	submissionID, _ := submissionResult.LastInsertId()
	orderResult, err := database.Exec(`
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,status,total_cent,address_snapshot_json,expire_at,canceled_at)
VALUES (?, ?, ?, ?, ?, 1000, JSON_OBJECT('recipient_name','支付买家'), CURRENT_TIMESTAMP(6)+INTERVAL 30 MINUTE,
        IF(?='canceled',CURRENT_TIMESTAMP(6),NULL))`, submissionID, orderNo, buyerID, shopID, status, status)
	if err != nil {
		t.Fatalf("seed payment order: %v", err)
	}
	orderID, _ := orderResult.LastInsertId()
	return orderID
}
