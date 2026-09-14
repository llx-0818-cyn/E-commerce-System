package checkout_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/checkout"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCheckoutPreviewGroupsShopsRepricesAndMarksStatusStockFailures(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行结算预览集成测试")
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := checkout.NewRepository(gormDB)
	service, _ := checkout.NewService(repository)
	address, err := service.CreateAddress(ctx, 7, checkout.CreateAddressInput{AddressInput: validAddressInput("结算人", "默认结算地址"), IsDefault: true})
	if err != nil {
		t.Fatalf("CreateAddress(default) error = %v", err)
	}
	otherAddress, err := service.CreateAddress(ctx, 8, checkout.CreateAddressInput{AddressInput: validAddressInput("其他人", "其他地址"), IsDefault: true})
	if err != nil {
		t.Fatalf("CreateAddress(other) error = %v", err)
	}

	shopOne, productOne, skuOne := seedPreviewSKU(t, database, 31, "预览店一", "PREVIEW-SKU-1", 1000, 10)
	shopTwo, productTwo, skuTwo := seedPreviewSKU(t, database, 32, "预览店二", "PREVIEW-SKU-2", 2000, 10)
	_, productThree, skuThree := seedPreviewProductSKU(t, database, shopOne, "预览商品三", "PREVIEW-SKU-3", 3000, 10)
	cartOne := mustAddPreviewCart(t, service, ctx, 7, skuOne, 2)
	cartTwo := mustAddPreviewCart(t, service, ctx, 7, skuTwo, 1)
	cartThree := mustAddPreviewCart(t, service, ctx, 7, skuThree, 3)
	otherCart := mustAddPreviewCart(t, service, ctx, 8, skuOne, 1)
	if cartOne.ObservedPriceCent != 1000 || cartTwo.ObservedPriceCent != 2000 || cartThree.ObservedPriceCent != 3000 {
		t.Fatalf("observed cart prices = %d/%d/%d", cartOne.ObservedPriceCent, cartTwo.ObservedPriceCent, cartThree.ObservedPriceCent)
	}

	if _, err := database.Exec("UPDATE product_skus SET price_cent=1200 WHERE id=?", skuOne); err != nil {
		t.Fatalf("change SKU one price: %v", err)
	}
	if _, err := database.Exec("UPDATE products SET status='off_sale' WHERE id=?", productTwo); err != nil {
		t.Fatalf("off sale product two: %v", err)
	}
	if _, err := database.Exec("UPDATE product_skus SET available_stock=2 WHERE id=?", skuThree); err != nil {
		t.Fatalf("reduce SKU three stock: %v", err)
	}
	preview, err := service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7})
	if err != nil {
		t.Fatalf("PreviewCheckout(failures) error = %v", err)
	}
	if preview.Address.ID != address.ID || len(preview.Shops) != 2 || preview.TotalCent != 2400 || !preview.HasPriceChanges || !preview.HasUnavailableItems || preview.CanSubmit {
		t.Fatalf("failure preview = %#v", preview)
	}
	assertCheckoutIssue(t, preview, skuOne, "", true)
	assertCheckoutIssue(t, preview, skuTwo, checkout.CheckoutIssueProductUnavailable, false)
	assertCheckoutIssue(t, preview, skuThree, checkout.CheckoutIssueStockInsufficient, false)
	if preview.Shops[0].ShopID != shopOne || preview.Shops[1].ShopID != shopTwo {
		t.Fatalf("checkout shop order = %d/%d, want %d/%d", preview.Shops[0].ShopID, preview.Shops[1].ShopID, shopOne, shopTwo)
	}

	if _, err := database.Exec("UPDATE products SET status='on_sale' WHERE id=?", productTwo); err != nil {
		t.Fatalf("restore product two: %v", err)
	}
	if _, err := database.Exec("UPDATE product_skus SET available_stock=10 WHERE id=?", skuThree); err != nil {
		t.Fatalf("restore SKU three stock: %v", err)
	}
	preview, err = service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7})
	if err != nil || preview.TotalCent != 13400 || preview.HasUnavailableItems || !preview.HasPriceChanges || preview.CanSubmit {
		t.Fatalf("unaccepted price-change preview = %#v, %v", preview, err)
	}
	accepted, err := service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7, AddressID: address.ID, AcceptPriceChanges: true})
	if err != nil || !accepted.CanSubmit || accepted.TotalCent != 13400 {
		t.Fatalf("accepted checkout preview = %#v, %v", accepted, err)
	}
	selected, err := service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7, AddressID: address.ID, CartItemIDs: []uint64{cartTwo.ID}, AcceptPriceChanges: true})
	if err != nil || len(selected.Shops) != 1 || selected.TotalCent != 2000 || !selected.CanSubmit {
		t.Fatalf("selected checkout preview = %#v, %v", selected, err)
	}
	if _, err := service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7, AddressID: otherAddress.ID}); !errors.Is(err, checkout.ErrAddressNotFound) {
		t.Fatalf("PreviewCheckout(other address) error = %v, want address not found", err)
	}
	if _, err := service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7, AddressID: address.ID, CartItemIDs: []uint64{otherCart.ID}}); !errors.Is(err, checkout.ErrCheckoutEmptyCart) && !errors.Is(err, checkout.ErrCheckoutCartItemNotFound) {
		t.Fatalf("PreviewCheckout(other cart item) error = %v, want cart not found", err)
	}

	if _, err := database.Exec("UPDATE shops SET status='closed' WHERE id=?", shopTwo); err != nil {
		t.Fatalf("close shop two: %v", err)
	}
	closed, err := service.PreviewCheckout(ctx, checkout.CheckoutPreviewInput{AccountID: 7, AddressID: address.ID, AcceptPriceChanges: true})
	if err != nil || !closed.HasUnavailableItems || closed.CanSubmit {
		t.Fatalf("closed-shop preview = %#v, %v", closed, err)
	}
	assertCheckoutIssue(t, closed, skuTwo, checkout.CheckoutIssueShopUnavailable, false)
	_ = productOne
	_ = productThree
}

func seedPreviewSKU(t *testing.T, database *sql.DB, ownerID uint64, shopName, code string, price, stock int64) (uint64, uint64, uint64) {
	t.Helper()
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?,?,'open')", ownerID, shopName)
	if err != nil {
		t.Fatalf("seed preview shop: %v", err)
	}
	shopIDValue, _ := shopResult.LastInsertId()
	shopID := uint64(shopIDValue)
	return seedPreviewProductSKU(t, database, shopID, shopName+"商品", code, price, stock)
}

func seedPreviewProductSKU(t *testing.T, database *sql.DB, shopID uint64, productName, code string, price, stock int64) (uint64, uint64, uint64) {
	t.Helper()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,?,'预览详情','on_sale')", shopID, productName)
	if err != nil {
		t.Fatalf("seed preview product: %v", err)
	}
	productIDValue, _ := productResult.LastInsertId()
	productID := uint64(productIDValue)
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,?,JSON_OBJECT(),?,?,'active')", productID, code, price, stock)
	if err != nil {
		t.Fatalf("seed preview SKU: %v", err)
	}
	skuIDValue, _ := skuResult.LastInsertId()
	return shopID, productID, uint64(skuIDValue)
}

func mustAddPreviewCart(t *testing.T, service *checkout.Service, ctx context.Context, accountID, skuID uint64, quantity int64) *checkout.CartItem {
	t.Helper()
	item, err := service.AddCartItem(ctx, checkout.AddCartInput{AccountID: accountID, SKUID: skuID, Quantity: quantity, Mode: checkout.CartQuantityReplace})
	if err != nil {
		t.Fatalf("add preview cart sku %d: %v", skuID, err)
	}
	return item
}

func assertCheckoutIssue(t *testing.T, preview *checkout.CheckoutPreview, skuID uint64, want checkout.CheckoutIssue, canCheckout bool) {
	t.Helper()
	for _, shop := range preview.Shops {
		for _, item := range shop.Items {
			if item.SKUID == skuID {
				if item.Issue != want || item.CanCheckout != canCheckout {
					t.Fatalf("checkout sku %d issue/can = %q/%v, want %q/%v", skuID, item.Issue, item.CanCheckout, want, canCheckout)
				}
				return
			}
		}
	}
	t.Fatalf("checkout sku %d not found", skuID)
}
