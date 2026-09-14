package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCallbackTransactionFaultInjectionHasNoPartialPaymentOrderInventoryOrEvent(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行统一支付回调故障注入测试")
	}
	database, cleanup := openCallbackTestDatabase(t, adminDSN)
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
	orderRepository, _ := orderdomain.NewRepository(gormDB)
	orderService, _ := orderdomain.NewService(orderRepository, 30*time.Minute)
	provider, _ := NewMockPaymentProvider("/api/mock-payments/cashier", "0123456789abcdef0123456789abcdef")
	paymentRepository, _ := NewRepository(gormDB)
	paymentService, _ := NewService(paymentRepository, provider, orderService)
	shopID, skuID := seedCallbackCatalog(t, database, 30)
	fault := errors.New("forced callback transaction fault")

	for index, stage := range []string{"after_callback", "after_payment", "after_order", "before_commit"} {
		buyerID := uint64(1100 + index)
		addressID := seedCallbackAddress(t, database, buyerID)
		group, err := orderService.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
			BuyerID: buyerID, IdempotencyKey: fmt.Sprintf("callback-fault-%d", index), AddressID: uint64(addressID),
			Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1000}}}},
		})
		if err != nil {
			t.Fatalf("create order for %s: %v", stage, err)
		}
		session, err := paymentService.CreatePayment(ctx, CreatePaymentInput{BuyerID: buyerID, OrderID: group.Orders[0].ID, AmountCent: 1000})
		if err != nil {
			t.Fatalf("create payment for %s: %v", stage, err)
		}
		callback, err := provider.Simulate(ctx, session.Payment.PaymentNo, CallbackSucceeded)
		if err != nil {
			t.Fatalf("simulate callback for %s: %v", stage, err)
		}
		paymentService.callbackFault = func(current string) error {
			if current == stage {
				return fault
			}
			return nil
		}
		if outcome, err := paymentService.ProcessCallback(ctx, callback); outcome != nil || !errors.Is(err, fault) {
			t.Fatalf("ProcessCallback(%s) = %#v, %v", stage, outcome, err)
		}
		assertCallbackTransactionState(t, database, session.Payment.ID, group.Orders[0].ID, callback.CallbackEventID, StatusPending, orderdomain.StatusPendingPayment, 0, 0, 0)
	}

	paymentService.callbackFault = nil
	buyerID := uint64(1200)
	addressID := seedCallbackAddress(t, database, buyerID)
	group, err := orderService.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: buyerID, IdempotencyKey: "callback-success", AddressID: uint64(addressID),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1000}}}},
	})
	if err != nil {
		t.Fatalf("create successful callback order: %v", err)
	}
	session, err := paymentService.CreatePayment(ctx, CreatePaymentInput{BuyerID: buyerID, OrderID: group.Orders[0].ID, AmountCent: 1000})
	if err != nil {
		t.Fatalf("create successful callback payment: %v", err)
	}
	callback, _ := provider.Simulate(ctx, session.Payment.PaymentNo, CallbackSucceeded)
	outcome, err := paymentService.ProcessCallback(ctx, callback)
	if err != nil || outcome == nil || !outcome.Applied || outcome.Status != StatusSucceeded {
		t.Fatalf("ProcessCallback(success) = %#v, %v", outcome, err)
	}
	assertCallbackTransactionState(t, database, session.Payment.ID, group.Orders[0].ID, callback.CallbackEventID, StatusSucceeded, orderdomain.StatusPaid, 1, 1, 1)
	var available, locked, sold int64
	if err := database.QueryRow("SELECT available_stock,locked_stock,sold_stock FROM product_skus WHERE id=?", skuID).Scan(&available, &locked, &sold); err != nil {
		t.Fatalf("query callback stock: %v", err)
	}
	if available != 25 || locked != 4 || sold != 1 || available+locked+sold != 30 {
		t.Fatalf("callback stock = %d/%d/%d, want 25/4/1", available, locked, sold)
	}
}

func assertCallbackTransactionState(t *testing.T, database *sql.DB, paymentID, orderID uint64, callbackEventID string, wantPayment Status, wantOrder orderdomain.Status, wantCallback, wantSell, wantPaidEvent int) {
	t.Helper()
	var paymentStatus Status
	var orderStatus orderdomain.Status
	if err := database.QueryRow("SELECT status FROM payment_records WHERE id=?", paymentID).Scan(&paymentStatus); err != nil {
		t.Fatalf("query payment status: %v", err)
	}
	if err := database.QueryRow("SELECT status FROM orders WHERE id=?", orderID).Scan(&orderStatus); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	var callbackCount, sellCount, eventCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM payment_callback_events WHERE callback_event_id=?", callbackEventID).Scan(&callbackCount); err != nil {
		t.Fatalf("count callback event: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='sell'", orderID).Scan(&sellCount); err != nil {
		t.Fatalf("count callback sell ledger: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=? AND event_type='order.paid'", orderID).Scan(&eventCount); err != nil {
		t.Fatalf("count callback order.paid event: %v", err)
	}
	if paymentStatus != wantPayment || orderStatus != wantOrder || callbackCount != wantCallback || sellCount != wantSell || eventCount != wantPaidEvent {
		t.Fatalf("callback state payment/order/callback/sell/event = %s/%s/%d/%d/%d, want %s/%s/%d/%d/%d", paymentStatus, orderStatus, callbackCount, sellCount, eventCount, wantPayment, wantOrder, wantCallback, wantSell, wantPaidEvent)
	}
}

func openCallbackTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_callback_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create callback test database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open callback test database: %v", err)
	}
	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop callback test database: %v", err)
		}
		adminDB.Close()
	}
	return database, cleanup
}

func seedCallbackCatalog(t *testing.T, database *sql.DB, stock int64) (int64, int64) {
	t.Helper()
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (1300,'回调事务店','open')")
	if err != nil {
		t.Fatalf("seed callback shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'回调商品','详情','on_sale')", shopID)
	if err != nil {
		t.Fatalf("seed callback product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'CALLBACK-SKU',JSON_OBJECT(),1000,?,'active')", productID, stock)
	if err != nil {
		t.Fatalf("seed callback SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	return shopID, skuID
}

func seedCallbackAddress(t *testing.T, database *sql.DB, buyerID uint64) int64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO addresses (account_id,recipient_name,phone,province,city,detail,is_default) VALUES (?,'回调买家','13800000000','浙江省','杭州市','文三路 1 号',TRUE)", buyerID)
	if err != nil {
		t.Fatalf("seed callback address: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}
