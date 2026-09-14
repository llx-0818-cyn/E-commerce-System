package storefront_test

import (
	"context"
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

func TestProductMinimumSellablePriceMultiSpecsPriceChangesAndAllUnavailable(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行最低可售价格集成测试")
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
	shop, err := service.CreateShop(ctx, 11, storefront.ShopProfileInput{Name: "多规格店"})
	if err != nil {
		t.Fatalf("CreateShop() error = %v", err)
	}
	if _, err := service.OpenShop(ctx, 11, shop.ID); err != nil {
		t.Fatalf("OpenShop() error = %v", err)
	}
	product, err := service.CreateProduct(ctx, 11, storefront.ProductProfileInput{Name: "多规格卫衣", Description: "最低价测试"}, nil)
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}
	large, err := service.CreateSKU(ctx, 11, product.ID, storefront.CreateSKUInput{
		SKUCode: "HOODIE-WHITE-L", Specs: storefront.SKUSpecs{"颜色": "白色", "尺寸": "L"}, PriceCent: 12000, AvailableStock: 5,
	})
	if err != nil {
		t.Fatalf("CreateSKU(large) error = %v", err)
	}
	small, err := service.CreateSKU(ctx, 11, product.ID, storefront.CreateSKUInput{
		SKUCode: "HOODIE-BLACK-S", Specs: storefront.SKUSpecs{"颜色": "黑色", "尺寸": "S"}, PriceCent: 8000, AvailableStock: 3,
	})
	if err != nil {
		t.Fatalf("CreateSKU(small) error = %v", err)
	}
	for _, skuID := range []uint64{large.ID, small.ID} {
		if _, err := service.SetSKUStatus(ctx, 11, skuID, storefront.SKUStatusActive); err != nil {
			t.Fatalf("activate SKU %d: %v", skuID, err)
		}
	}
	if _, err := service.PublishProduct(ctx, 11, product.ID); err != nil {
		t.Fatalf("PublishProduct() error = %v", err)
	}

	detail, err := service.GetPublicProduct(ctx, product.ID)
	if err != nil || detail.MinPriceCent == nil || *detail.MinPriceCent != 8000 || !detail.CanPurchase {
		t.Fatalf("initial product detail = %#v, %v", detail, err)
	}
	if len(detail.SKUs) != 2 || len(detail.SKUs[0].Specs) != 2 || len(detail.SKUs[1].Specs) != 2 {
		t.Fatalf("multi-spec SKU response = %#v", detail.SKUs)
	}
	page, err := service.ListPublicProducts(ctx, shop.ID, 10, nil)
	if err != nil || len(page.Products) != 1 || page.Products[0].MinPriceCent == nil || *page.Products[0].MinPriceCent != 8000 {
		t.Fatalf("initial product page = %#v, %v", page, err)
	}

	updatedSmall, err := service.UpdateSKU(ctx, 11, small.ID, storefront.UpdateSKUInput{
		SKUCode: small.SKUCode, Specs: storefront.SKUSpecs{"颜色": "黑色", "尺寸": "S"}, PriceCent: 15000,
	})
	if err != nil || updatedSmall.PriceCent != 15000 {
		t.Fatalf("UpdateSKU(price change) = %#v, %v", updatedSmall, err)
	}
	detail, err = service.GetPublicProduct(ctx, product.ID)
	if err != nil || detail.MinPriceCent == nil || *detail.MinPriceCent != 12000 {
		t.Fatalf("detail after price change = %#v, %v", detail, err)
	}
	if _, err := service.SetSKUStatus(ctx, 11, large.ID, storefront.SKUStatusInactive); err != nil {
		t.Fatalf("deactivate large SKU: %v", err)
	}
	detail, err = service.GetPublicProduct(ctx, product.ID)
	if err != nil || detail.MinPriceCent == nil || *detail.MinPriceCent != 15000 || countPurchasableSKUs(detail.SKUs) != 1 {
		t.Fatalf("detail after partial SKU unavailable = %#v, %v", detail, err)
	}
	if _, err := service.SetSKUStatus(ctx, 11, small.ID, storefront.SKUStatusInactive); err != nil {
		t.Fatalf("deactivate final SKU: %v", err)
	}
	detail, err = service.GetPublicProduct(ctx, product.ID)
	if err != nil || detail.MinPriceCent != nil || detail.CanPurchase || countPurchasableSKUs(detail.SKUs) != 0 {
		t.Fatalf("detail with all SKUs unavailable = %#v, %v", detail, err)
	}
	page, err = service.ListPublicProducts(ctx, shop.ID, 10, nil)
	if err != nil || page.Products[0].MinPriceCent != nil || page.Products[0].CanPurchase {
		t.Fatalf("product page with all SKUs unavailable = %#v, %v", page, err)
	}

	if _, err := service.CreateSKU(ctx, 11, product.ID, storefront.CreateSKUInput{
		SKUCode: "BAD-DUP-SPECS", Specs: storefront.SKUSpecs{"颜色": "白色", " 颜色 ": "黑色"}, PriceCent: 100, AvailableStock: 1,
	}); !errors.Is(err, storefront.ErrSKUValidation) {
		t.Fatalf("CreateSKU(duplicate normalized spec) error = %v, want validation", err)
	}
	if _, err := service.CreateSKU(ctx, 11, product.ID, storefront.CreateSKUInput{
		SKUCode: "BAD-EMPTY-SPEC", Specs: storefront.SKUSpecs{"尺寸": "  "}, PriceCent: 100, AvailableStock: 1,
	}); !errors.Is(err, storefront.ErrSKUValidation) {
		t.Fatalf("CreateSKU(empty spec value) error = %v, want validation", err)
	}
}
