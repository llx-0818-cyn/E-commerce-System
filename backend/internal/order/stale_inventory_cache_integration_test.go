package order_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/entitycache"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestStaleInventoryCacheCannotOversellNormalOrder(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行旧库存缓存下单测试")
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

	addressOne := insertOrderTestAddress(t, database, 31)
	addressTwo := insertOrderTestAddress(t, database, 32)
	shopID, skuID := insertOrderServiceCatalog(t, database, 901, "旧缓存店", "STALE-CACHE-SKU", 1999, 1)

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rawRedis := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	stalePayload := []byte(fmt.Sprintf(`{"sku_id":%d,"available_stock":99,"locked_stock":0,"sold_stock":0,"version":0}`, skuID))
	if err := cache.SetBytes(ctx, entitycache.ProductInventoryKey(cache, uint64(skuID)), stalePayload, time.Hour); err != nil {
		t.Fatalf("seed stale inventory cache: %v", err)
	}

	first, err := service.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: 31, IdempotencyKey: "stale-cache-first", AddressID: uint64(addressOne),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1999}}}},
	})
	if err != nil || first == nil || len(first.Orders) != 1 {
		t.Fatalf("first order = %#v, %v", first, err)
	}
	second, err := service.CreateOrderGroup(ctx, orderdomain.CreateOrderGroupInput{
		BuyerID: 32, IdempotencyKey: "stale-cache-second", AddressID: uint64(addressTwo),
		Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1999}}}},
	})
	if second != nil || !errors.Is(err, orderdomain.ErrStockInsufficient) {
		t.Fatalf("second order with stale cache = %#v, %v; want stock insufficient", second, err)
	}

	cached, err := cache.GetBytes(ctx, entitycache.ProductInventoryKey(cache, uint64(skuID)))
	if err != nil || !bytes.Contains(cached, []byte(`"available_stock":99`)) {
		t.Fatalf("stale cache was not present during assertion: %s, %v", cached, err)
	}
	assertOrderTransactionCounts(t, database, 1, 1, 1, 1, 2)
	assertOrderSKUStock(t, database, skuID, 0, 1, 0, 1)
	var negativeCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_skus WHERE id=? AND (available_stock<0 OR locked_stock<0 OR sold_stock<0)", skuID).Scan(&negativeCount); err != nil || negativeCount != 0 {
		t.Fatalf("negative inventory rows = %d, %v", negativeCount, err)
	}
}
