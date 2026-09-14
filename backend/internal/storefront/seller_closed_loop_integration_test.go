package storefront_test

import (
	"context"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMinimalSellerCatalogInventoryFulfillmentAndDashboardClosedLoop(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行最小卖家闭环集成测试")
	}
	database, cleanup := openStorefrontTestDatabase(t, adminDSN)
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
	storeRepository, _ := storefront.NewRepository(gormDB)
	storeService, _ := storefront.NewService(storeRepository)
	orderRepository, _ := orderdomain.NewRepository(gormDB)
	orderService, _ := orderdomain.NewService(orderRepository, 30*time.Minute)

	shop, err := storeService.CreateShop(ctx, 77, storefront.ShopProfileInput{Name: "最小卖家闭环店"})
	if err != nil {
		t.Fatalf("CreateShop() error = %v", err)
	}
	if _, err := storeService.OpenShop(ctx, 77, shop.ID); err != nil {
		t.Fatalf("OpenShop() error = %v", err)
	}
	product, err := storeService.CreateProduct(ctx, 77, storefront.ProductProfileInput{Name: "闭环商品", Description: "无需复杂运营模块"}, nil)
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}
	sku, err := storeService.CreateSKU(ctx, 77, product.ID, storefront.CreateSKUInput{
		SKUCode: "SELLER-CLOSED-LOOP", Specs: storefront.SKUSpecs{"规格": "标准"}, PriceCent: 2500, AvailableStock: 3,
	})
	if err != nil {
		t.Fatalf("CreateSKU() error = %v", err)
	}
	if _, err := storeService.SetSKUStatus(ctx, 77, sku.ID, storefront.SKUStatusActive); err != nil {
		t.Fatalf("SetSKUStatus() error = %v", err)
	}
	if _, err := storeService.AdjustSKUStock(ctx, 77, sku.ID, 5); err != nil {
		t.Fatalf("AdjustSKUStock() error = %v", err)
	}
	if _, err := storeService.PublishProduct(ctx, 77, product.ID); err != nil {
		t.Fatalf("PublishProduct() error = %v", err)
	}

	addressResult, err := database.Exec(`
INSERT INTO addresses (account_id,recipient_name,phone,province,city,detail,is_default)
VALUES (88,'闭环买家','13800000088','浙江省','杭州市','文三路 88 号',TRUE)`)
	if err != nil {
		t.Fatalf("seed buyer address: %v", err)
	}
	addressID, _ := addressResult.LastInsertId()
	group, err := orderService.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: 88, IdempotencyKey: "seller-closed-loop", AddressID: uint64(addressID),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: shop.ID, Items: []orderdomain.CreateOrderItemInput{{SKUID: sku.ID, Quantity: 1, ExpectedPriceCent: 2500}}}},
	})
	if err != nil {
		t.Fatalf("CreateOrderGroup() error = %v", err)
	}
	orderID := group.Orders[0].ID

	dashboard, err := storeService.GetSellerDashboard(ctx, 77)
	if err != nil || dashboard.ProductCount != 1 || dashboard.SKUCount != 1 || dashboard.AvailableStock != 7 || dashboard.LockedStock != 1 || dashboard.SoldStock != 0 || dashboard.PendingShipmentOrders != 0 {
		t.Fatalf("dashboard after order = %#v, %v", dashboard, err)
	}
	if _, err := orderService.MarkOrderPaid(ctx, orderID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkOrderPaid() error = %v", err)
	}
	dashboard, _ = storeService.GetSellerDashboard(ctx, 77)
	if dashboard.AvailableStock != 7 || dashboard.LockedStock != 0 || dashboard.SoldStock != 1 || dashboard.PendingShipmentOrders != 1 {
		t.Fatalf("dashboard after payment = %#v", dashboard)
	}
	if _, err := orderService.ShipOrder(ctx, 77, orderID); err != nil {
		t.Fatalf("ShipOrder() error = %v", err)
	}
	if _, err := orderService.ConfirmReceipt(ctx, 88, orderID); err != nil {
		t.Fatalf("ConfirmReceipt() error = %v", err)
	}
	dashboard, _ = storeService.GetSellerDashboard(ctx, 77)
	if dashboard.PendingShipmentOrders != 0 || dashboard.CompletedOrders != 1 {
		t.Fatalf("dashboard after fulfillment = %#v", dashboard)
	}
	if _, err := storeService.GetSellerDashboard(ctx, 999); err != storefront.ErrShopNotFound {
		t.Fatalf("dashboard without shop error = %v", err)
	}
}
