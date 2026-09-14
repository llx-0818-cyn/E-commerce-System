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

func TestCreateOrderGroupLocksSortedStockAndRollsBackWholeCrossShopFailure(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单组事务集成测试")
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
	shopOne, skuOne := insertOrderServiceCatalog(t, database, 201, "事务店一", "TX-SKU-1", 1100, 10)
	shopTwo, skuTwo := insertOrderServiceCatalog(t, database, 202, "事务店二", "TX-SKU-2", 2500, 1)
	failedInput := orderdomain.CreateOrderGroupInput{
		BuyerID: 7, IdempotencyKey: "transaction-key", AddressID: uint64(addressID),
		Drafts: []orderdomain.CreateShopOrderInput{
			{ShopID: uint64(shopTwo), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuTwo), Quantity: 2, ExpectedPriceCent: 2500}}},
			{ShopID: uint64(shopOne), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuOne), Quantity: 2, ExpectedPriceCent: 1100}}},
		},
	}
	if group, err := service.CreateOrderGroup(ctx, failedInput); group != nil || !errors.Is(err, orderdomain.ErrStockInsufficient) {
		t.Fatalf("CreateOrderGroup(insufficient) = %#v, %v", group, err)
	}
	assertOrderTransactionCounts(t, database, 0, 0, 0, 0, 0)
	assertOrderSKUStock(t, database, skuOne, 10, 0, 0, 0)
	assertOrderSKUStock(t, database, skuTwo, 1, 0, 0, 0)

	failedInput.Drafts[0].Items[0].Quantity = 1
	group, err := service.CreateOrderGroup(ctx, failedInput)
	if err != nil {
		t.Fatalf("CreateOrderGroup(success) error = %v", err)
	}
	if group.Submission.GrandTotalCent != 4700 || len(group.Orders) != 2 || group.Orders[0].ShopID != uint64(shopOne) || group.Orders[1].ShopID != uint64(shopTwo) {
		t.Fatalf("created order group = %#v", group)
	}
	if group.Orders[0].TotalCent != 2200 || group.Orders[1].TotalCent != 2500 || len(group.Orders[0].Items) != 1 || len(group.Orders[1].Items) != 1 {
		t.Fatalf("created order totals/items = %#v", group.Orders)
	}
	assertOrderTransactionCounts(t, database, 1, 2, 2, 2, 4)
	assertOrderSKUStock(t, database, skuOne, 8, 2, 0, 1)
	assertOrderSKUStock(t, database, skuTwo, 0, 1, 0, 1)
}

func insertOrderTestAddress(t *testing.T, database *sql.DB, buyerID uint64) int64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO addresses (account_id,recipient_name,phone,province,city,district,detail,postal_code,is_default)
VALUES (?,'订单买家','13800000000','浙江省','杭州市','西湖区','文三路 1 号','310000',TRUE)`, buyerID)
	if err != nil {
		t.Fatalf("insert order test address: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func insertOrderServiceCatalog(t *testing.T, database *sql.DB, ownerID uint64, shopName, skuCode string, price, stock int64) (int64, int64) {
	t.Helper()
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?,?,'open')", ownerID, shopName)
	if err != nil {
		t.Fatalf("insert order service shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,cover_url,status) VALUES (?,?, '订单事务商品','/cover.jpg','on_sale')", shopID, shopName+"商品")
	if err != nil {
		t.Fatalf("insert order service product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,?,JSON_OBJECT('规格',?),?,?,'active')", productID, skuCode, skuCode, price, stock)
	if err != nil {
		t.Fatalf("insert order service SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	return shopID, skuID
}

func assertOrderTransactionCounts(t *testing.T, database *sql.DB, submissions, orders, items, ledger, outbox int) {
	t.Helper()
	for table, want := range map[string]int{
		"order_submissions": submissions, "orders": orders, "order_items": items,
		"inventory_ledger": ledger, "outbox_events": outbox,
	} {
		var got int
		query := "SELECT COUNT(*) FROM " + table
		if table == "outbox_events" {
			query += " WHERE event_type IN ('order.created','order.expire.requested')"
		}
		if err := database.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s count = %d, %v; want %d", table, got, err, want)
		}
	}
}

func assertOrderSKUStock(t *testing.T, database *sql.DB, skuID int64, available, locked, sold int64, wantVersion uint64) {
	t.Helper()
	var gotAvailable, gotLocked, gotSold int64
	var version uint64
	if err := database.QueryRow("SELECT available_stock,locked_stock,sold_stock,version FROM product_skus WHERE id=?", skuID).
		Scan(&gotAvailable, &gotLocked, &gotSold, &version); err != nil {
		t.Fatalf("query SKU stock: %v", err)
	}
	if gotAvailable != available || gotLocked != locked || gotSold != sold || version != wantVersion {
		t.Fatalf("SKU %d stock/version = %d/%d/%d/%d, want %d/%d/%d/%d", skuID, gotAvailable, gotLocked, gotSold, version, available, locked, sold, wantVersion)
	}
}
