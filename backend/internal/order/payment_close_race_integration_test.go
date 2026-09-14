package order_test

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

func TestPaymentSuccessRacesExpirationWithSingleCASWinnerAndConservedStock(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行支付与关单竞争测试")
	}
	database, cleanup := openOrderTestDatabase(t, adminDSN)
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
	repository, _ := orderdomain.NewRepository(gormDB)
	service, _ := orderdomain.NewService(repository, 30*time.Minute)

	addressID := insertOrderTestAddress(t, database, 7)
	shopID, skuID := insertOrderServiceCatalog(t, database, 801, "支付关单竞争店", "PAY-CLOSE-SKU", 1000, 5)
	group := createSingleOrderGroup(t, service, ctx, 7, "pay-close-race", addressID, shopID, skuID, 1)
	orderID := group.Orders[0].ID
	if _, err := database.Exec("UPDATE orders SET created_at=CURRENT_TIMESTAMP(6)-INTERVAL 2 SECOND,expire_at=CURRENT_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", orderID); err != nil {
		t.Fatalf("make race order expired: %v", err)
	}

	start := make(chan struct{})
	var paidOrder, expiredOrder *orderdomain.Order
	var payErr, expireErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		paidOrder, payErr = service.MarkOrderPaid(ctx, orderID, time.Now().UTC())
	}()
	go func() {
		defer wait.Done()
		<-start
		expiredOrder, expireErr = service.ExpireOrder(ctx, orderID, time.Now().UTC())
	}()
	close(start)
	wait.Wait()

	loaded, err := service.GetOrderForActor(ctx, 7, orderID)
	if err != nil {
		t.Fatalf("load race order: %v", err)
	}
	switch loaded.Status {
	case orderdomain.StatusPaid:
		if payErr != nil || paidOrder == nil || expireErr != nil || expiredOrder == nil || expiredOrder.Status != orderdomain.StatusPaid {
			t.Fatalf("paid winner results paid=%#v/%v expired=%#v/%v", paidOrder, payErr, expiredOrder, expireErr)
		}
		assertOrderSKUStock(t, database, skuID, 4, 0, 1, 2)
		assertTerminalInventoryAndEvents(t, database, orderID, 0, 1, 0, 1)
	case orderdomain.StatusCanceled:
		if expireErr != nil || expiredOrder == nil || !errors.Is(payErr, orderdomain.ErrOrderStateConflict) || paidOrder != nil {
			t.Fatalf("cancel winner results paid=%#v/%v expired=%#v/%v", paidOrder, payErr, expiredOrder, expireErr)
		}
		assertOrderSKUStock(t, database, skuID, 5, 0, 0, 2)
		assertTerminalInventoryAndEvents(t, database, orderID, 1, 0, 1, 0)
	default:
		t.Fatalf("race final status = %s", loaded.Status)
	}
	var available, locked, sold int64
	if err := database.QueryRow("SELECT available_stock,locked_stock,sold_stock FROM product_skus WHERE id=?", skuID).Scan(&available, &locked, &sold); err != nil {
		t.Fatalf("query final stock: %v", err)
	}
	if available < 0 || locked < 0 || sold < 0 || available+locked+sold != 5 {
		t.Fatalf("stock conservation = %d+%d+%d, want 5 and non-negative", available, locked, sold)
	}
}

func assertTerminalInventoryAndEvents(t *testing.T, database *sql.DB, orderID uint64, wantRelease, wantSell, wantCanceled, wantPaid int) {
	t.Helper()
	var releaseCount, sellCount, canceledCount, paidCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='release'", orderID).Scan(&releaseCount); err != nil {
		t.Fatalf("count release ledger: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='sell'", orderID).Scan(&sellCount); err != nil {
		t.Fatalf("count sell ledger: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=? AND event_type='order.canceled'", orderID).Scan(&canceledCount); err != nil {
		t.Fatalf("count canceled event: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=? AND event_type='order.paid'", orderID).Scan(&paidCount); err != nil {
		t.Fatalf("count paid event: %v", err)
	}
	if releaseCount != wantRelease || sellCount != wantSell || canceledCount != wantCanceled || paidCount != wantPaid || releaseCount+sellCount != 1 {
		t.Fatalf("terminal effects release/sell/canceled/paid = %d/%d/%d/%d, want %d/%d/%d/%d", releaseCount, sellCount, canceledCount, paidCount, wantRelease, wantSell, wantCanceled, wantPaid)
	}
}
