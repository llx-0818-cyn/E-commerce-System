package checkout_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/analytics"
	"feedsystem_video_go/internal/checkout"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCartAddMergeReplaceDeleteListValidationAndOwnership(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行购物车 Service 集成测试")
	}
	database, cleanup := openCheckoutTestDatabase(t, adminDSN)
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
	shopID, productID, skuID := seedCheckoutSKU(t, database, "CART-SERVICE-SKU", 10)
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := checkout.NewRepository(gormDB)
	behavior, _ := analytics.NewRecorder(gormDB)
	service, _ := checkout.NewService(repository, behavior)

	item, err := service.AddCartItem(ctx, checkout.AddCartInput{AccountID: 7, SKUID: skuID, Quantity: 2, Mode: checkout.CartQuantityMerge, SourcePostID: 88})
	if err != nil || item.Quantity != 2 || item.AccountID != 7 || item.SKUID != skuID {
		t.Fatalf("AddCartItem(first) = %#v, %v", item, err)
	}
	merged, err := service.AddCartItem(ctx, checkout.AddCartInput{AccountID: 7, SKUID: skuID, Quantity: 3, Mode: checkout.CartQuantityMerge, SourcePostID: 88})
	if err != nil || merged.ID != item.ID || merged.Quantity != 5 {
		t.Fatalf("AddCartItem(merge) = %#v, %v", merged, err)
	}
	replaced, err := service.AddCartItem(ctx, checkout.AddCartInput{AccountID: 7, SKUID: skuID, Quantity: 4, Mode: checkout.CartQuantityReplace})
	if err != nil || replaced.ID != item.ID || replaced.Quantity != 4 {
		t.Fatalf("AddCartItem(replace) = %#v, %v", replaced, err)
	}
	assertCheckoutCartRow(t, database, 7, skuID, 1, 4)
	updated, err := service.UpdateCartQuantity(ctx, 7, item.ID, 6)
	if err != nil || updated.Quantity != 6 {
		t.Fatalf("UpdateCartQuantity(owner) = %#v, %v", updated, err)
	}

	for label, input := range map[string]checkout.AddCartInput{
		"zero quantity": {AccountID: 7, SKUID: skuID, Quantity: 0, Mode: checkout.CartQuantityMerge},
		"too large":     {AccountID: 7, SKUID: skuID, Quantity: 1000, Mode: checkout.CartQuantityMerge},
		"bad mode":      {AccountID: 7, SKUID: skuID, Quantity: 1, Mode: "toggle"},
	} {
		if _, err := service.AddCartItem(ctx, input); !errors.Is(err, checkout.ErrCartValidation) {
			t.Fatalf("AddCartItem(%s) error = %v, want validation", label, err)
		}
	}
	if _, err := service.AddCartItem(ctx, checkout.AddCartInput{AccountID: 8, SKUID: skuID, Quantity: 11, Mode: checkout.CartQuantityReplace}); !errors.Is(err, checkout.ErrCartSKUUnavailable) {
		t.Fatalf("AddCartItem(over stock) error = %v, want SKU unavailable", err)
	}
	if _, err := service.UpdateCartQuantity(ctx, 8, item.ID, 1); !errors.Is(err, checkout.ErrCartItemNotFound) {
		t.Fatalf("UpdateCartQuantity(other) error = %v, want not found", err)
	}
	if err := service.DeleteCartItem(ctx, 8, item.ID); !errors.Is(err, checkout.ErrCartItemNotFound) {
		t.Fatalf("DeleteCartItem(other) error = %v, want not found", err)
	}
	otherItems, err := service.ListCart(ctx, 8)
	if err != nil || len(otherItems) != 0 {
		t.Fatalf("ListCart(other) = %#v, %v", otherItems, err)
	}
	ownerItems, err := service.ListCart(ctx, 7)
	if err != nil || len(ownerItems) != 1 || ownerItems[0].ID != item.ID || ownerItems[0].Quantity != 6 {
		t.Fatalf("ListCart(owner) = %#v, %v", ownerItems, err)
	}
	var cartEventCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE event_type=?", analytics.CartItemAddedEvent).Scan(&cartEventCount); err != nil || cartEventCount != 3 {
		t.Fatalf("cart behavior event count = %d, %v; want 3", cartEventCount, err)
	}

	if _, err := database.Exec("UPDATE shops SET status='closed' WHERE id=?", shopID); err != nil {
		t.Fatalf("close cart shop: %v", err)
	}
	if _, err := service.AddCartItem(ctx, checkout.AddCartInput{AccountID: 9, SKUID: skuID, Quantity: 1, Mode: checkout.CartQuantityMerge}); !errors.Is(err, checkout.ErrCartSKUUnavailable) {
		t.Fatalf("AddCartItem(closed shop) error = %v, want unavailable", err)
	}
	if _, err := database.Exec("UPDATE shops SET status='open' WHERE id=?", shopID); err != nil {
		t.Fatalf("reopen cart shop: %v", err)
	}

	failingService, _ := checkout.NewService(repository, failingCartRecorder{})
	failingItem, err := failingService.AddCartItem(ctx, checkout.AddCartInput{AccountID: 10, SKUID: skuID, Quantity: 1, Mode: checkout.CartQuantityMerge, SourcePostID: 99})
	if err != nil || failingItem == nil {
		t.Fatalf("AddCartItem(failing behavior recorder) = %#v, %v", failingItem, err)
	}
	assertCheckoutCartRow(t, database, 10, skuID, 1, 1)

	if err := service.DeleteCartItem(ctx, 7, item.ID); err != nil {
		t.Fatalf("DeleteCartItem(owner) error = %v", err)
	}
	ownerItems, err = service.ListCart(ctx, 7)
	if err != nil || len(ownerItems) != 0 {
		t.Fatalf("ListCart(after delete) = %#v, %v", ownerItems, err)
	}
	_ = productID
}

type failingCartRecorder struct{}

func (failingCartRecorder) RecordCartItemAddedFromPost(context.Context, uint64, uint64, uint64, uint64, int64) error {
	return errors.New("forced analytics failure")
}

func seedCheckoutSKU(t *testing.T, database *sql.DB, code string, stock int64) (uint64, uint64, uint64) {
	t.Helper()
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (1,'结算店','open')")
	if err != nil {
		t.Fatalf("seed checkout shop: %v", err)
	}
	shopIDValue, _ := shopResult.LastInsertId()
	shopID := uint64(shopIDValue)
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'结算商品','详情','on_sale')", shopID)
	if err != nil {
		t.Fatalf("seed checkout product: %v", err)
	}
	productIDValue, _ := productResult.LastInsertId()
	productID := uint64(productIDValue)
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,?,JSON_OBJECT(),1000,?,'active')", productID, code, stock)
	if err != nil {
		t.Fatalf("seed checkout SKU: %v", err)
	}
	skuIDValue, _ := skuResult.LastInsertId()
	return shopID, productID, uint64(skuIDValue)
}

func assertCheckoutCartRow(t *testing.T, database *sql.DB, accountID, skuID uint64, wantCount, wantQuantity int) {
	t.Helper()
	var count, quantity int
	if err := database.QueryRow("SELECT COUNT(*),COALESCE(MAX(quantity),0) FROM cart_items WHERE account_id=? AND sku_id=?", accountID, skuID).Scan(&count, &quantity); err != nil {
		t.Fatalf("query cart row: %v", err)
	}
	if count != wantCount || quantity != wantQuantity {
		t.Fatalf("cart row count/quantity = %d/%d, want %d/%d", count, quantity, wantCount, wantQuantity)
	}
}

func openCheckoutTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_checkout_test_%d", time.Now().UnixNano())
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
