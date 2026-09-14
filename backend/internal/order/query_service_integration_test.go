package order_test

import (
	"context"
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

func TestBuyerAndShopOrderQueriesStablePaginationAndAccessControl(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单查询访问控制集成测试")
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
	shopOne, skuOne := insertOrderServiceCatalog(t, database, 401, "查询店一", "QUERY-SKU-1", 1000, 10)
	shopTwo, skuTwo := insertOrderServiceCatalog(t, database, 402, "查询店二", "QUERY-SKU-2", 2000, 10)
	groupSeven, err := service.CreateOrderGroup(ctx, idempotentOrderInput(7, "query-buyer-7", addressSeven, shopOne, skuOne, shopTwo, skuTwo))
	if err != nil {
		t.Fatalf("create buyer seven group: %v", err)
	}
	groupEight, err := service.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: 8, IdempotencyKey: "query-buyer-8", AddressID: uint64(addressEight),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopOne), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuOne), Quantity: 1, ExpectedPriceCent: 1000}}}},
	})
	if err != nil {
		t.Fatalf("create buyer eight group: %v", err)
	}
	if _, err := database.Exec("UPDATE orders SET created_at=CURRENT_TIMESTAMP(6)-INTERVAL 1 MINUTE,updated_at=CURRENT_TIMESTAMP(6)-INTERVAL 1 MINUTE"); err != nil {
		t.Fatalf("fix order timestamps: %v", err)
	}

	firstPage, err := service.ListBuyerOrders(ctx, 7, orderdomain.StatusPendingPayment, nil, 1)
	if err != nil || len(firstPage.Orders) != 1 || !firstPage.HasMore || firstPage.NextCursor == nil {
		t.Fatalf("buyer first page = %#v, %v", firstPage, err)
	}
	secondPage, err := service.ListBuyerOrders(ctx, 7, orderdomain.StatusPendingPayment, firstPage.NextCursor, 1)
	if err != nil || len(secondPage.Orders) != 1 || secondPage.HasMore || secondPage.Orders[0].ID == firstPage.Orders[0].ID {
		t.Fatalf("buyer second page = %#v, %v", secondPage, err)
	}
	if len(firstPage.Orders[0].Items) != 1 || len(secondPage.Orders[0].Items) != 1 {
		t.Fatalf("buyer pages did not preload items: %#v %#v", firstPage, secondPage)
	}
	empty, err := service.ListBuyerOrders(ctx, 999, "", nil, 20)
	if err != nil || empty.Orders == nil || len(empty.Orders) != 0 {
		t.Fatalf("empty buyer page = %#v, %v", empty, err)
	}

	shopPage, err := service.ListShopOrders(ctx, 401, uint64(shopOne), orderdomain.StatusPendingPayment, nil, 20)
	if err != nil || len(shopPage.Orders) != 2 {
		t.Fatalf("owner shop page = %#v, %v", shopPage, err)
	}
	if _, err := service.ListShopOrders(ctx, 402, uint64(shopOne), "", nil, 20); !errors.Is(err, orderdomain.ErrOrderForbidden) {
		t.Fatalf("other owner shop list error = %v", err)
	}

	shopOneOrder := groupSeven.Orders[0]
	if shopOneOrder.ShopID != uint64(shopOne) {
		shopOneOrder = groupSeven.Orders[1]
	}
	if value, err := service.GetOrderForActor(ctx, 7, shopOneOrder.ID); err != nil || value.ID != shopOneOrder.ID {
		t.Fatalf("buyer detail = %#v, %v", value, err)
	}
	if value, err := service.GetOrderForActor(ctx, 401, shopOneOrder.ID); err != nil || value.ID != shopOneOrder.ID {
		t.Fatalf("shop owner detail = %#v, %v", value, err)
	}
	if value, err := service.GetOrderForActor(ctx, 402, shopOneOrder.ID); value != nil || !errors.Is(err, orderdomain.ErrOrderNotFound) {
		t.Fatalf("unrelated detail = %#v, %v", value, err)
	}
	if value, err := service.GetOrderForActor(ctx, 7, groupEight.Orders[0].ID); value != nil || !errors.Is(err, orderdomain.ErrOrderNotFound) {
		t.Fatalf("other buyer detail = %#v, %v", value, err)
	}
}
