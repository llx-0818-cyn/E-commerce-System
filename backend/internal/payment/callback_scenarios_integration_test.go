package payment

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCallbackFirstSuccessRepeatTamperLateAndExpirationRace(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行五类支付回调集成测试")
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
	shopID, skuID := seedCallbackCatalog(t, database, 20)

	first := createCallbackFixture(t, ctx, database, orderService, paymentService, 2001, "callback-first", shopID, skuID)
	firstPayload, _ := provider.Simulate(ctx, first.payment.Payment.PaymentNo, CallbackSucceeded)
	firstOutcome, err := paymentService.ProcessCallback(ctx, firstPayload)
	if err != nil || firstOutcome == nil || !firstOutcome.Applied || firstOutcome.Duplicate {
		t.Fatalf("first success = %#v, %v", firstOutcome, err)
	}
	repeatedPayload, _ := provider.RepeatCallback(ctx, first.payment.Payment.PaymentNo)
	repeatedOutcome, err := paymentService.ProcessCallback(ctx, repeatedPayload)
	if err != nil || repeatedOutcome == nil || !repeatedOutcome.Duplicate {
		t.Fatalf("repeated success = %#v, %v", repeatedOutcome, err)
	}
	assertCallbackScenario(t, database, first.payment.Payment.ID, first.order.ID, firstPayload.CallbackEventID, StatusSucceeded, orderdomain.StatusPaid, "applied", 0, 1, 0)

	tampered := createCallbackFixture(t, ctx, database, orderService, paymentService, 2002, "callback-tamper", shopID, skuID)
	tamperedPayload, _ := provider.Simulate(ctx, tampered.payment.Payment.PaymentNo, CallbackSucceeded)
	tamperedPayload.AmountCent = 1
	tamperedPayload = provider.signedCallback(tamperedPayload)
	tamperedOutcome, err := paymentService.ProcessCallback(ctx, tamperedPayload)
	if !errors.Is(err, ErrPaymentAmountMismatch) || tamperedOutcome == nil || !tamperedOutcome.Rejected {
		t.Fatalf("tampered callback = %#v, %v", tamperedOutcome, err)
	}
	assertCallbackScenario(t, database, tampered.payment.Payment.ID, tampered.order.ID, tamperedPayload.CallbackEventID, StatusPending, orderdomain.StatusPendingPayment, "rejected", 0, 0, 1)

	late := createCallbackFixture(t, ctx, database, orderService, paymentService, 2003, "callback-late", shopID, skuID)
	if _, err := orderService.CancelOrder(ctx, 2003, late.order.ID); err != nil {
		t.Fatalf("cancel late callback order: %v", err)
	}
	latePayload, _ := provider.Simulate(ctx, late.payment.Payment.PaymentNo, CallbackSucceeded)
	lateOutcome, err := paymentService.ProcessCallback(ctx, latePayload)
	if !errors.Is(err, orderdomain.ErrOrderStateConflict) || lateOutcome == nil || !lateOutcome.Rejected || lateOutcome.Status != StatusClosed {
		t.Fatalf("late callback = %#v, %v", lateOutcome, err)
	}
	assertCallbackScenario(t, database, late.payment.Payment.ID, late.order.ID, latePayload.CallbackEventID, StatusClosed, orderdomain.StatusCanceled, "rejected", 1, 0, 1)

	race := createCallbackFixture(t, ctx, database, orderService, paymentService, 2004, "callback-race", shopID, skuID)
	if _, err := database.Exec("UPDATE orders SET created_at=CURRENT_TIMESTAMP(6)-INTERVAL 2 SECOND,expire_at=CURRENT_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", race.order.ID); err != nil {
		t.Fatalf("expire race order: %v", err)
	}
	racePayload, _ := provider.Simulate(ctx, race.payment.Payment.PaymentNo, CallbackSucceeded)
	start := make(chan struct{})
	var callbackOutcome *CallbackOutcome
	var callbackErr error
	var expirationOrder *orderdomain.Order
	var expirationErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		callbackOutcome, callbackErr = paymentService.ProcessCallback(ctx, racePayload)
	}()
	go func() {
		defer wait.Done()
		<-start
		expirationOrder, expirationErr = orderService.ExpireOrder(ctx, race.order.ID, time.Now().UTC())
	}()
	close(start)
	wait.Wait()
	var finalOrderStatus orderdomain.Status
	var finalPaymentStatus Status
	if err := database.QueryRow("SELECT status FROM orders WHERE id=?", race.order.ID).Scan(&finalOrderStatus); err != nil {
		t.Fatalf("query race order status: %v", err)
	}
	if err := database.QueryRow("SELECT status FROM payment_records WHERE id=?", race.payment.Payment.ID).Scan(&finalPaymentStatus); err != nil {
		t.Fatalf("query race payment status: %v", err)
	}
	switch finalOrderStatus {
	case orderdomain.StatusPaid:
		if callbackErr != nil || callbackOutcome == nil || !callbackOutcome.Applied || expirationErr != nil || expirationOrder.Status != orderdomain.StatusPaid || finalPaymentStatus != StatusSucceeded {
			t.Fatalf("callback race paid winner callback=%#v/%v expiration=%#v/%v payment=%s", callbackOutcome, callbackErr, expirationOrder, expirationErr, finalPaymentStatus)
		}
		assertCallbackScenario(t, database, race.payment.Payment.ID, race.order.ID, racePayload.CallbackEventID, StatusSucceeded, orderdomain.StatusPaid, "applied", 0, 1, 0)
	case orderdomain.StatusCanceled:
		if !errors.Is(callbackErr, orderdomain.ErrOrderStateConflict) || callbackOutcome == nil || !callbackOutcome.Rejected || expirationErr != nil || expirationOrder.Status != orderdomain.StatusCanceled || finalPaymentStatus != StatusClosed {
			t.Fatalf("callback race cancel winner callback=%#v/%v expiration=%#v/%v payment=%s", callbackOutcome, callbackErr, expirationOrder, expirationErr, finalPaymentStatus)
		}
		assertCallbackScenario(t, database, race.payment.Payment.ID, race.order.ID, racePayload.CallbackEventID, StatusClosed, orderdomain.StatusCanceled, "rejected", 1, 0, 1)
	default:
		t.Fatalf("callback race final order status = %s", finalOrderStatus)
	}
	assertPaymentInventoryConserved(t, database, skuID, 20)
}

type callbackFixture struct {
	order   orderdomain.Order
	payment *PaymentSession
}

func createCallbackFixture(t *testing.T, ctx context.Context, database *sql.DB, orderService *orderdomain.Service, paymentService *Service, buyerID uint64, key string, shopID, skuID int64) callbackFixture {
	t.Helper()
	addressID := seedCallbackAddress(t, database, buyerID)
	group, err := orderService.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: buyerID, IdempotencyKey: key, AddressID: uint64(addressID),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1000}}}},
	})
	if err != nil {
		t.Fatalf("create callback fixture order: %v", err)
	}
	session, err := paymentService.CreatePayment(ctx, CreatePaymentInput{BuyerID: buyerID, OrderID: group.Orders[0].ID, AmountCent: 1000})
	if err != nil {
		t.Fatalf("create callback fixture payment: %v", err)
	}
	return callbackFixture{order: group.Orders[0], payment: session}
}

func assertCallbackScenario(t *testing.T, database *sql.DB, paymentID, orderID uint64, callbackEventID string, paymentStatus Status, orderStatus orderdomain.Status, processStatus string, wantRelease, wantSell, wantAnomaly int) {
	t.Helper()
	var gotPayment Status
	var gotOrder orderdomain.Status
	var gotProcess string
	if err := database.QueryRow("SELECT status FROM payment_records WHERE id=?", paymentID).Scan(&gotPayment); err != nil {
		t.Fatalf("query scenario payment: %v", err)
	}
	if err := database.QueryRow("SELECT status FROM orders WHERE id=?", orderID).Scan(&gotOrder); err != nil {
		t.Fatalf("query scenario order: %v", err)
	}
	if err := database.QueryRow("SELECT process_status FROM payment_callback_events WHERE callback_event_id=?", callbackEventID).Scan(&gotProcess); err != nil {
		t.Fatalf("query scenario callback: %v", err)
	}
	var releaseCount, sellCount, anomalyCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='release'", orderID).Scan(&releaseCount); err != nil {
		t.Fatalf("count scenario release: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='sell'", orderID).Scan(&sellCount); err != nil {
		t.Fatalf("count scenario sell: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM payment_anomalies WHERE callback_event_id=?", callbackEventID).Scan(&anomalyCount); err != nil {
		t.Fatalf("count scenario anomaly: %v", err)
	}
	if gotPayment != paymentStatus || gotOrder != orderStatus || gotProcess != processStatus || releaseCount != wantRelease || sellCount != wantSell || anomalyCount != wantAnomaly {
		t.Fatalf("scenario payment/order/process/release/sell/anomaly = %s/%s/%s/%d/%d/%d, want %s/%s/%s/%d/%d/%d", gotPayment, gotOrder, gotProcess, releaseCount, sellCount, anomalyCount, paymentStatus, orderStatus, processStatus, wantRelease, wantSell, wantAnomaly)
	}
}

func assertPaymentInventoryConserved(t *testing.T, database *sql.DB, skuID, total int64) {
	t.Helper()
	var available, locked, sold int64
	if err := database.QueryRow("SELECT available_stock,locked_stock,sold_stock FROM product_skus WHERE id=?", skuID).Scan(&available, &locked, &sold); err != nil {
		t.Fatalf("query payment inventory: %v", err)
	}
	if available < 0 || locked < 0 || sold < 0 || available+locked+sold != total {
		t.Fatalf("payment inventory = %d/%d/%d, want non-negative sum %d", available, locked, sold, total)
	}
}
