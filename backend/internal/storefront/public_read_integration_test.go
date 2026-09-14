package storefront_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"feedsystem_video_go/internal/entitycache"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPublicShopProductPaginationDetailAndOperatingStates(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行公开店铺商品读取集成测试")
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

	openShop := seedPublicShop(t, database, 1, "开放店", "open")
	closedShop := seedPublicShop(t, database, 2, "闭店", "closed")
	disabledShop := seedPublicShop(t, database, 3, "禁用店", "disabled")
	categoryResult, err := database.Exec("INSERT INTO categories (name,slug) VALUES ('服饰','apparel')")
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}
	categoryID, _ := categoryResult.LastInsertId()
	sameTime := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	openProductOne := seedPublicProduct(t, database, openShop, uint64(categoryID), "开放商品一", "on_sale", sameTime)
	openProductTwo := seedPublicProduct(t, database, openShop, uint64(categoryID), "开放商品二", "on_sale", sameTime)
	offSaleProduct := seedPublicProduct(t, database, openShop, uint64(categoryID), "已下架商品", "off_sale", sameTime.Add(time.Minute))
	closedProduct := seedPublicProduct(t, database, closedShop, uint64(categoryID), "闭店商品", "on_sale", sameTime.Add(2*time.Minute))
	disabledProduct := seedPublicProduct(t, database, disabledShop, uint64(categoryID), "禁用店商品", "on_sale", sameTime.Add(3*time.Minute))

	activeSKUID := seedPublicSKU(t, database, openProductOne, "OPEN-ONE-ACTIVE", `{"颜色":"白色","尺寸":"L"}`, 9900, 5, "active")
	seedPublicSKU(t, database, openProductOne, "OPEN-ONE-INACTIVE", `{"颜色":"黑色","尺寸":"L"}`, 10900, 9, "inactive")
	seedPublicSKU(t, database, openProductTwo, "OPEN-TWO-SOLDOUT", `{"颜色":"蓝色"}`, 8900, 0, "active")
	seedPublicSKU(t, database, closedProduct, "CLOSED-ACTIVE", `{"规格":"标准"}`, 5900, 10, "active")
	seedPublicSKU(t, database, disabledProduct, "DISABLED-ACTIVE", `{"规格":"标准"}`, 4900, 10, "active")
	seedPublicProductImages(t, database, openProductOne)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := storefront.NewRepository(gormDB)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rawRedis := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rawRedis.Close()
	cacheClient := rediscache.NewClient(rawRedis, "v1:")
	publicCache, err := storefront.NewPublicEntityCache(repository, cacheClient)
	if err != nil {
		t.Fatalf("NewPublicEntityCache() error = %v", err)
	}
	service, _ := storefront.NewService(repository, publicCache)

	openSummary, err := service.GetPublicShop(ctx, openShop)
	if err != nil || openSummary.ProductCount != 2 || !openSummary.AcceptingOrders || openSummary.Shop.Status != storefront.ShopStatusOpen {
		t.Fatalf("open shop summary = %#v, %v", openSummary, err)
	}
	firstPage, err := service.ListPublicProducts(ctx, openShop, 1, nil)
	if err != nil {
		t.Fatalf("ListPublicProducts(first) error = %v", err)
	}
	if got := publicProductIDs(firstPage.Products); !reflect.DeepEqual(got, []uint64{openProductTwo}) || !firstPage.HasMore || firstPage.NextCursor == nil || firstPage.Products[0].CanPurchase {
		t.Fatalf("first product page = %#v ids=%v", firstPage, got)
	}
	secondPage, err := service.ListPublicProducts(ctx, openShop, 1, firstPage.NextCursor)
	if err != nil {
		t.Fatalf("ListPublicProducts(second) error = %v", err)
	}
	if got := publicProductIDs(secondPage.Products); !reflect.DeepEqual(got, []uint64{openProductOne}) || secondPage.HasMore || !secondPage.Products[0].CanPurchase {
		t.Fatalf("second product page = %#v ids=%v", secondPage, got)
	}

	detail, err := service.GetPublicProduct(ctx, openProductOne)
	if err != nil {
		t.Fatalf("GetPublicProduct(open) error = %v", err)
	}
	if !detail.CanPurchase || len(detail.Images) != 2 || detail.Images[0].SortOrder != 0 || detail.Images[1].SortOrder != 1 || len(detail.SKUs) != 2 {
		t.Fatalf("open product detail = %#v", detail)
	}
	if !detail.SKUs[0].CanPurchase || detail.SKUs[1].CanPurchase || detail.SKUs[0].Specs["颜色"] != "白色" {
		t.Fatalf("open product SKU states = %#v", detail.SKUs)
	}
	for _, key := range []string{
		entitycache.ProductDetailKey(cacheClient, openProductOne),
		entitycache.ProductSKUKey(cacheClient, activeSKUID),
	} {
		payload, err := cacheClient.GetBytes(ctx, key)
		if err != nil {
			t.Fatalf("read stable cache %s: %v", key, err)
		}
		if bytes.Contains(payload, []byte(`"available_stock"`)) {
			t.Fatalf("stable cache %s embeds realtime inventory: %s", key, payload)
		}
	}
	inventoryPayload, err := cacheClient.GetBytes(ctx, entitycache.ProductInventoryKey(cacheClient, activeSKUID))
	if err != nil || !bytes.Contains(inventoryPayload, []byte(`"available_stock":5`)) {
		t.Fatalf("inventory cache payload = %s, %v", inventoryPayload, err)
	}

	closedSummary, err := service.GetPublicShop(ctx, closedShop)
	if err != nil || closedSummary.AcceptingOrders || closedSummary.ProductCount != 1 {
		t.Fatalf("closed shop summary = %#v, %v", closedSummary, err)
	}
	closedPage, err := service.ListPublicProducts(ctx, closedShop, 10, nil)
	if err != nil || len(closedPage.Products) != 1 || closedPage.Products[0].CanPurchase {
		t.Fatalf("closed shop products = %#v, %v", closedPage, err)
	}
	closedDetail, err := service.GetPublicProduct(ctx, closedProduct)
	if err != nil || closedDetail.CanPurchase || closedDetail.SKUs[0].CanPurchase {
		t.Fatalf("closed product detail = %#v, %v", closedDetail, err)
	}

	if _, err := service.GetPublicShop(ctx, disabledShop); !errors.Is(err, storefront.ErrShopNotFound) {
		t.Fatalf("GetPublicShop(disabled) error = %v, want not found", err)
	}
	if _, err := service.ListPublicProducts(ctx, disabledShop, 10, nil); !errors.Is(err, storefront.ErrShopNotFound) {
		t.Fatalf("ListPublicProducts(disabled) error = %v, want not found", err)
	}
	if _, err := service.GetPublicProduct(ctx, disabledProduct); !errors.Is(err, storefront.ErrProductNotFound) {
		t.Fatalf("GetPublicProduct(disabled shop) error = %v, want product not found", err)
	}
	if _, err := service.GetPublicProduct(ctx, offSaleProduct); !errors.Is(err, storefront.ErrProductNotFound) {
		t.Fatalf("GetPublicProduct(off sale) error = %v, want product not found", err)
	}
}

func seedPublicShop(t *testing.T, database *sql.DB, ownerID uint64, name, status string) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?, ?, ?)", ownerID, name, status)
	if err != nil {
		t.Fatalf("seed shop %q: %v", name, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func seedPublicProduct(t *testing.T, database *sql.DB, shopID, categoryID uint64, name, status string, createdAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO products (shop_id,category_id,name,subtitle,description,cover_url,status,created_at)
VALUES (?, ?, ?, '公开摘要', '公开商品详情', '/products/cover.jpg', ?, ?)`, shopID, categoryID, name, status, createdAt)
	if err != nil {
		t.Fatalf("seed product %q: %v", name, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func seedPublicSKU(t *testing.T, database *sql.DB, productID uint64, code, specs string, price, stock int64, status string) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status)
VALUES (?, ?, ?, ?, ?, ?)`, productID, code, specs, price, stock, status)
	if err != nil {
		t.Fatalf("seed SKU %s: %v", code, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func seedPublicProductImages(t *testing.T, database *sql.DB, productID uint64) {
	t.Helper()
	for order := 1; order >= 0; order-- {
		if _, err := database.Exec(`
INSERT INTO product_images (product_id,storage_key,image_url,mime_type,byte_size,width,height,sort_order)
VALUES (?, ?, ?, 'image/jpeg', 100, 800, 800, ?)`, productID, "products/detail-"+strconv.Itoa(order), "/products/detail", order); err != nil {
			t.Fatalf("seed product image %d: %v", order, err)
		}
	}
}

func publicProductIDs(items []storefront.PublicProductItem) []uint64 {
	ids := make([]uint64, len(items))
	for index := range items {
		ids[index] = items[index].Product.ID
	}
	return ids
}
