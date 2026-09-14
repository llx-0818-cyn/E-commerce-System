package order_test

import (
	"context"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestExpirationScanCancelsAndReleasesStockWhenDelayMessageIsMissing(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单扫描关单集成测试")
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
	shopID, skuID := insertOrderServiceCatalog(t, database, 601, "扫描关单店", "EXPIRE-SKU", 1000, 5)
	group := createSingleOrderGroup(t, service, ctx, 7, "expire-scan", addressID, shopID, skuID, 2)
	orderID := group.Orders[0].ID
	now := time.Now().UTC()
	if _, err := service.ExpireOrder(ctx, orderID, now); err != orderdomain.ErrOrderNotExpired {
		t.Fatalf("early ExpireOrder() error = %v, want not expired", err)
	}
	if _, err := database.Exec("UPDATE orders SET created_at=CURRENT_TIMESTAMP(6)-INTERVAL 2 SECOND,expire_at=CURRENT_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", orderID); err != nil {
		t.Fatalf("make order expired: %v", err)
	}

	result, err := service.ScanExpiredOrders(ctx, time.Now().Add(time.Second), 100)
	if err != nil || result.Scanned != 1 || result.Canceled != 1 {
		t.Fatalf("ScanExpiredOrders() = %#v, %v", result, err)
	}
	loaded, err := service.GetOrderForActor(ctx, 7, orderID)
	if err != nil || loaded.Status != orderdomain.StatusCanceled || loaded.CanceledAt == nil {
		t.Fatalf("expired order = %#v, %v", loaded, err)
	}
	assertOrderSKUStock(t, database, skuID, 5, 0, 0, 2)
	assertOrderTransitionEffects(t, database, orderID, "release", orderdomain.OrderCanceledEvent, 1, 1)

	replayed, err := service.ScanExpiredOrders(ctx, time.Now().Add(2*time.Second), 100)
	if err != nil || replayed.Scanned != 0 || replayed.Canceled != 0 {
		t.Fatalf("repeat ScanExpiredOrders() = %#v, %v", replayed, err)
	}
}
