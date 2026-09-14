package storefront_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestProductManagementOwnershipPublishValidationPartialSKUOffSaleAndStock(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行商品管理集成测试")
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
	repository, _ := storefront.NewRepository(gormDB)
	service, _ := storefront.NewService(repository)
	ownerShop, err := service.CreateShop(ctx, 1, storefront.ShopProfileInput{Name: "店主一"})
	if err != nil {
		t.Fatalf("CreateShop(owner) error = %v", err)
	}
	if _, err := service.OpenShop(ctx, 1, ownerShop.ID); err != nil {
		t.Fatalf("OpenShop(owner) error = %v", err)
	}
	otherShop, err := service.CreateShop(ctx, 2, storefront.ShopProfileInput{Name: "店主二"})
	if err != nil {
		t.Fatalf("CreateShop(other) error = %v", err)
	}
	if _, err := service.OpenShop(ctx, 2, otherShop.ID); err != nil {
		t.Fatalf("OpenShop(other) error = %v", err)
	}
	categoryResult, err := database.Exec("INSERT INTO categories (name,slug) VALUES ('服饰','management-apparel')")
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}
	categoryIDValue, _ := categoryResult.LastInsertId()
	categoryID := uint64(categoryIDValue)

	product, err := service.CreateProduct(ctx, 1, storefront.ProductProfileInput{
		CategoryID: &categoryID, Name: "纯棉 T 恤", Subtitle: "基础款", Description: "管理测试商品", CoverURL: "/cover.jpg",
	}, []storefront.ProductImageInput{
		{StorageKey: "products/manage-front.jpg", ImageURL: "/front.jpg", MIMEType: "image/jpeg", ByteSize: 100, Width: 800, Height: 800},
		{StorageKey: "products/manage-back.jpg", ImageURL: "/back.jpg", MIMEType: "image/jpeg", ByteSize: 100, Width: 800, Height: 800},
	})
	if err != nil || product.Status != storefront.ProductStatusDraft || product.ShopID != ownerShop.ID {
		t.Fatalf("CreateProduct() = %#v, %v", product, err)
	}
	assertProductImageOrders(t, database, product.ID, []int{0, 1})
	if _, err := service.UpdateProduct(ctx, 2, product.ID, storefront.ProductProfileInput{Name: "越权商品", Description: "x"}); !errors.Is(err, storefront.ErrProductForbidden) {
		t.Fatalf("UpdateProduct(other owner) error = %v, want forbidden", err)
	}
	assertProductName(t, database, product.ID, "纯棉 T 恤")
	if _, err := service.PublishProduct(ctx, 1, product.ID); !errors.Is(err, storefront.ErrProductNoSellableSKU) {
		t.Fatalf("PublishProduct(no SKU) error = %v, want no sellable SKU", err)
	}

	whiteSKU, err := service.CreateSKU(ctx, 1, product.ID, storefront.CreateSKUInput{
		SKUCode: " tshirt-white-l ", Specs: storefront.SKUSpecs{"颜色": " 白色 ", "尺寸": "L"}, PriceCent: 9900, AvailableStock: 5,
	})
	if err != nil || whiteSKU.SKUCode != "TSHIRT-WHITE-L" || whiteSKU.Status != storefront.SKUStatusInactive || whiteSKU.PriceCent != 9900 {
		t.Fatalf("CreateSKU(white) = %#v, %v", whiteSKU, err)
	}
	if _, err := service.CreateSKU(ctx, 2, product.ID, storefront.CreateSKUInput{SKUCode: "OTHER", PriceCent: 100, AvailableStock: 1}); !errors.Is(err, storefront.ErrProductForbidden) {
		t.Fatalf("CreateSKU(other owner) error = %v, want forbidden", err)
	}
	if _, err := service.PublishProduct(ctx, 1, product.ID); !errors.Is(err, storefront.ErrProductNoSellableSKU) {
		t.Fatalf("PublishProduct(inactive SKU) error = %v, want no sellable SKU", err)
	}
	if _, err := service.SetSKUStatus(ctx, 1, whiteSKU.ID, storefront.SKUStatusActive); err != nil {
		t.Fatalf("SetSKUStatus(white active) error = %v", err)
	}
	published, err := service.PublishProduct(ctx, 1, product.ID)
	if err != nil || published.Status != storefront.ProductStatusOnSale {
		t.Fatalf("PublishProduct(valid SKU) = %#v, %v", published, err)
	}

	blackSKU, err := service.CreateSKU(ctx, 1, product.ID, storefront.CreateSKUInput{
		SKUCode: "TSHIRT-BLACK-L", Specs: storefront.SKUSpecs{"颜色": "黑色", "尺寸": "L"}, PriceCent: 10900, AvailableStock: 3,
	})
	if err != nil {
		t.Fatalf("CreateSKU(black) error = %v", err)
	}
	if _, err := service.SetSKUStatus(ctx, 1, blackSKU.ID, storefront.SKUStatusActive); err != nil {
		t.Fatalf("SetSKUStatus(black active) error = %v", err)
	}
	updatedBlack, err := service.UpdateSKU(ctx, 1, blackSKU.ID, storefront.UpdateSKUInput{
		SKUCode: "TSHIRT-BLACK-L", Specs: storefront.SKUSpecs{"颜色": "黑色", "尺寸": "L"}, PriceCent: 11900,
	})
	if err != nil || updatedBlack.PriceCent != 11900 {
		t.Fatalf("UpdateSKU(integer cents) = %#v, %v", updatedBlack, err)
	}
	if _, err := service.CreateSKU(ctx, 1, product.ID, storefront.CreateSKUInput{SKUCode: "TSHIRT-BLACK-L", PriceCent: 100, AvailableStock: 1}); !errors.Is(err, storefront.ErrSKUCodeConflict) {
		t.Fatalf("CreateSKU(duplicate code) error = %v, want conflict", err)
	}
	if _, err := service.UpdateSKU(ctx, 1, blackSKU.ID, storefront.UpdateSKUInput{SKUCode: "TSHIRT-BLACK-L", PriceCent: 100, OriginalPriceCent: int64Pointer(99)}); !errors.Is(err, storefront.ErrSKUValidation) {
		t.Fatalf("UpdateSKU(invalid original price) error = %v, want validation", err)
	}

	if _, err := service.SetSKUStatus(ctx, 2, whiteSKU.ID, storefront.SKUStatusInactive); !errors.Is(err, storefront.ErrProductForbidden) {
		t.Fatalf("SetSKUStatus(other owner) error = %v, want forbidden", err)
	}
	if _, err := service.SetSKUStatus(ctx, 1, whiteSKU.ID, storefront.SKUStatusInactive); err != nil {
		t.Fatalf("SetSKUStatus(partial off sale) error = %v", err)
	}
	assertProductStatus(t, database, product.ID, storefront.ProductStatusOnSale)
	detail, err := service.GetPublicProduct(ctx, product.ID)
	if err != nil || !detail.CanPurchase || countPurchasableSKUs(detail.SKUs) != 1 {
		t.Fatalf("detail after partial SKU off sale = %#v, %v", detail, err)
	}

	if _, err := service.ReplaceProductImages(ctx, 2, product.ID, nil); !errors.Is(err, storefront.ErrProductForbidden) {
		t.Fatalf("ReplaceProductImages(other owner) error = %v, want forbidden", err)
	}
	replaced, err := service.ReplaceProductImages(ctx, 1, product.ID, []storefront.ProductImageInput{
		{StorageKey: "products/manage-new.jpg", ImageURL: "/new.jpg", MIMEType: "image/jpeg", ByteSize: 200, Width: 1000, Height: 1000},
	})
	if err != nil || len(replaced) != 1 || replaced[0].SortOrder != 0 {
		t.Fatalf("ReplaceProductImages(owner) = %#v, %v", replaced, err)
	}
	if _, err := service.AdjustSKUStock(ctx, 2, blackSKU.ID, -1); !errors.Is(err, storefront.ErrProductForbidden) {
		t.Fatalf("AdjustSKUStock(other owner) error = %v, want forbidden", err)
	}
	stockZero, err := service.AdjustSKUStock(ctx, 1, blackSKU.ID, -3)
	if err != nil || stockZero.AvailableStock != 0 || stockZero.Version <= blackSKU.Version {
		t.Fatalf("AdjustSKUStock(to zero) = %#v, %v", stockZero, err)
	}
	if _, err := service.AdjustSKUStock(ctx, 1, blackSKU.ID, -1); !errors.Is(err, storefront.ErrStockInsufficient) {
		t.Fatalf("AdjustSKUStock(negative) error = %v, want insufficient", err)
	}
	detail, err = service.GetPublicProduct(ctx, product.ID)
	if err != nil || detail.CanPurchase {
		t.Fatalf("detail with all SKUs unavailable = %#v, %v", detail, err)
	}
	if _, err := service.AdjustSKUStock(ctx, 1, blackSKU.ID, 5); err != nil {
		t.Fatalf("AdjustSKUStock(restock) error = %v", err)
	}
	offSale, err := service.OffSaleProduct(ctx, 1, product.ID)
	if err != nil || offSale.Status != storefront.ProductStatusOffSale {
		t.Fatalf("OffSaleProduct() = %#v, %v", offSale, err)
	}
	if _, err := service.GetPublicProduct(ctx, product.ID); !errors.Is(err, storefront.ErrProductNotFound) {
		t.Fatalf("GetPublicProduct(off sale) error = %v, want not found", err)
	}
	if _, err := service.CloseShop(ctx, 1, ownerShop.ID); err != nil {
		t.Fatalf("CloseShop before publish validation: %v", err)
	}
	if _, err := service.PublishProduct(ctx, 1, product.ID); !errors.Is(err, storefront.ErrShopNotOpen) {
		t.Fatalf("PublishProduct(closed shop) error = %v, want shop not open", err)
	}
}

func assertProductImageOrders(t *testing.T, database *sql.DB, productID uint64, want []int) {
	t.Helper()
	rows, err := database.Query("SELECT sort_order FROM product_images WHERE product_id=? ORDER BY sort_order,id", productID)
	if err != nil {
		t.Fatalf("query product image orders: %v", err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var order int
		if err := rows.Scan(&order); err != nil {
			t.Fatalf("scan product image order: %v", err)
		}
		got = append(got, order)
	}
	if len(got) != len(want) {
		t.Fatalf("product image orders = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("product image orders = %v, want %v", got, want)
		}
	}
}

func assertProductName(t *testing.T, database *sql.DB, productID uint64, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow("SELECT name FROM products WHERE id=?", productID).Scan(&got); err != nil {
		t.Fatalf("query product name: %v", err)
	}
	if got != want {
		t.Fatalf("product %d name = %q, want %q", productID, got, want)
	}
}

func assertProductStatus(t *testing.T, database *sql.DB, productID uint64, want storefront.ProductStatus) {
	t.Helper()
	var got string
	if err := database.QueryRow("SELECT status FROM products WHERE id=?", productID).Scan(&got); err != nil {
		t.Fatalf("query product status: %v", err)
	}
	if storefront.ProductStatus(got) != want {
		t.Fatalf("product %d status = %q, want %q", productID, got, want)
	}
}

func countPurchasableSKUs(skus []storefront.PublicSKU) int {
	count := 0
	for _, sku := range skus {
		if sku.CanPurchase {
			count++
		}
	}
	return count
}

func int64Pointer(value int64) *int64 { return &value }
