package order_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestOrderCancelShipConfirmCASIdempotencyAndPermissions(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单状态流转集成测试")
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

	addressSeven := insertOrderTestAddress(t, database, 7)
	addressEight := insertOrderTestAddress(t, database, 8)
	shopID, skuID := insertOrderServiceCatalog(t, database, 501, "流转店", "TRANSITION-SKU", 1000, 8)
	cancelGroup := createSingleOrderGroup(t, service, ctx, 7, "transition-cancel", addressSeven, shopID, skuID, 2)
	cancelOrder := cancelGroup.Orders[0]
	if value, err := service.CancelOrder(ctx, 8, cancelOrder.ID); value != nil || !errors.Is(err, orderdomain.ErrOrderNotFound) {
		t.Fatalf("other buyer cancel = %#v, %v", value, err)
	}
	canceled, err := service.CancelOrder(ctx, 7, cancelOrder.ID)
	if err != nil || canceled.Status != orderdomain.StatusCanceled || canceled.CanceledAt == nil {
		t.Fatalf("buyer cancel = %#v, %v", canceled, err)
	}
	replayedCancel, err := service.CancelOrder(ctx, 7, cancelOrder.ID)
	if err != nil || replayedCancel.Status != orderdomain.StatusCanceled {
		t.Fatalf("repeat cancel = %#v, %v", replayedCancel, err)
	}
	assertOrderSKUStock(t, database, skuID, 8, 0, 0, 2)
	assertOrderTransitionEffects(t, database, cancelOrder.ID, "release", orderdomain.OrderCanceledEvent, 1, 1)
	if value, err := service.ShipOrder(ctx, 501, cancelOrder.ID); value != nil || !errors.Is(err, orderdomain.ErrOrderStateConflict) {
		t.Fatalf("ship canceled order = %#v, %v", value, err)
	}

	shipGroup := createSingleOrderGroup(t, service, ctx, 8, "transition-ship", addressEight, shopID, skuID, 1)
	shipOrder := shipGroup.Orders[0]
	if _, err := database.Exec("UPDATE orders SET status='paid',paid_at=CURRENT_TIMESTAMP(6) WHERE id=? AND status='pending_payment'", shipOrder.ID); err != nil {
		t.Fatalf("seed paid order: %v", err)
	}
	if value, err := service.ShipOrder(ctx, 999, shipOrder.ID); value != nil || !errors.Is(err, orderdomain.ErrOrderNotFound) {
		t.Fatalf("unrelated seller ship = %#v, %v", value, err)
	}
	shipped, err := service.ShipOrder(ctx, 501, shipOrder.ID)
	if err != nil || shipped.Status != orderdomain.StatusShipped || shipped.ShippedAt == nil {
		t.Fatalf("owner ship = %#v, %v", shipped, err)
	}
	if replayed, err := service.ShipOrder(ctx, 501, shipOrder.ID); err != nil || replayed.Status != orderdomain.StatusShipped {
		t.Fatalf("repeat ship = %#v, %v", replayed, err)
	}
	if value, err := service.ConfirmReceipt(ctx, 7, shipOrder.ID); value != nil || !errors.Is(err, orderdomain.ErrOrderNotFound) {
		t.Fatalf("other buyer confirm = %#v, %v", value, err)
	}
	completed, err := service.ConfirmReceipt(ctx, 8, shipOrder.ID)
	if err != nil || completed.Status != orderdomain.StatusCompleted || completed.CompletedAt == nil {
		t.Fatalf("buyer confirm = %#v, %v", completed, err)
	}
	if replayed, err := service.ConfirmReceipt(ctx, 8, shipOrder.ID); err != nil || replayed.Status != orderdomain.StatusCompleted {
		t.Fatalf("repeat confirm = %#v, %v", replayed, err)
	}
	assertOrderTransitionEffects(t, database, shipOrder.ID, "release", orderdomain.OrderShippedEvent, 0, 1)
	assertOrderTransitionEffects(t, database, shipOrder.ID, "release", orderdomain.OrderCompletedEvent, 0, 1)
}

func createSingleOrderGroup(t *testing.T, service *orderdomain.Service, ctx context.Context, buyerID uint64, key string, addressID, shopID, skuID int64, quantity uint) *orderdomain.OrderGroup {
	t.Helper()
	group, err := service.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: buyerID, IdempotencyKey: key, AddressID: uint64(addressID),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: quantity, ExpectedPriceCent: 1000}}}},
	})
	if err != nil {
		t.Fatalf("create single order group: %v", err)
	}
	return group
}

func assertOrderTransitionEffects(t *testing.T, database *sql.DB, orderID uint64, movement, eventType string, wantLedger, wantEvent int) {
	t.Helper()
	var ledgerCount, eventCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type=?", orderID, movement).Scan(&ledgerCount); err != nil {
		t.Fatalf("count transition ledger: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_type='order' AND aggregate_id=? AND event_type=?", orderID, eventType).Scan(&eventCount); err != nil {
		t.Fatalf("count transition event: %v", err)
	}
	if ledgerCount != wantLedger || eventCount != wantEvent {
		t.Fatalf("transition effects ledger/event = %d/%d, want %d/%d", ledgerCount, eventCount, wantLedger, wantEvent)
	}
}
