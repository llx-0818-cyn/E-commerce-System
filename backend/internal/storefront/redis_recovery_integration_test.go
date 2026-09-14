package storefront_test

import (
	"context"
	"os"
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

func TestPublicProductFallsBackWhileRedisIsDownAndRefillsAfterRecovery(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行商品 Redis 故障恢复测试")
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
	shopID := seedPublicShop(t, database, 81, "恢复店铺", "open")
	categoryResult, _ := database.Exec("INSERT INTO categories (name,slug) VALUES ('恢复分类','recovery')")
	categoryID, _ := categoryResult.LastInsertId()
	productID := seedPublicProduct(t, database, shopID, uint64(categoryID), "恢复商品", "on_sale", time.Now().UTC())
	skuID := seedPublicSKU(t, database, productID, "RECOVERY-SKU", `{"规格":"标准"}`, 8800, 6, "active")

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	address := mini.Addr()
	rawRedis := goredis.NewClient(&goredis.Options{Addr: address, MaxRetries: 0})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	repository, _ := storefront.NewRepository(gormDB)
	publicCache, _ := storefront.NewPublicEntityCache(repository, cache)
	service, _ := storefront.NewService(repository, publicCache)

	mini.Close()
	detail, err := service.GetPublicProduct(ctx, productID)
	if err != nil || detail.Product.ID != productID || len(detail.SKUs) != 1 || detail.SKUs[0].AvailableStock != 6 {
		t.Fatalf("GetPublicProduct() while Redis is down = %#v, %v", detail, err)
	}

	recovered := miniredis.NewMiniRedis()
	if err := recovered.StartAddr(address); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
	defer recovered.Close()
	detail, err = service.GetPublicProduct(ctx, productID)
	if err != nil || detail.Product.ID != productID || detail.SKUs[0].AvailableStock != 6 {
		t.Fatalf("GetPublicProduct() after Redis recovery = %#v, %v", detail, err)
	}
	for _, key := range []string{
		entitycache.ShopSummaryKey(cache, shopID),
		entitycache.ProductDetailKey(cache, productID),
		entitycache.ProductSKUKey(cache, skuID),
		entitycache.ProductInventoryKey(cache, skuID),
	} {
		if !recovered.Exists(key) {
			t.Fatalf("recovered Redis missing backfilled key %s", key)
		}
	}
}
