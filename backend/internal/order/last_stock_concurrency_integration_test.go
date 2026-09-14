package order_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestConcurrentBuyersCompeteForLastNormalStockWithoutOversell(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行最后一件普通库存并发测试")
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
	shopID, skuID := insertOrderServiceCatalog(t, database, 701, "最后库存店", "LAST-STOCK-SKU", 1000, 1)
	inputs := []orderdomain.CreateOrderGroupInput{
		{
			BuyerID: 7, IdempotencyKey: "last-stock-7", AddressID: uint64(addressSeven),
			Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1000}}}},
		},
		{
			BuyerID: 8, IdempotencyKey: "last-stock-8", AddressID: uint64(addressEight),
			Drafts: []orderdomain.CreateShopOrderInput{{ShopID: uint64(shopID), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuID), Quantity: 1, ExpectedPriceCent: 1000}}}},
		},
	}
	start := make(chan struct{})
	results := make([]*orderdomain.OrderGroup, len(inputs))
	errorsByIndex := make([]error, len(inputs))
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByIndex[index] = service.CreateOrderGroup(ctx, inputs[index])
		}(index)
	}
	close(start)
	wait.Wait()

	successes, insufficient := 0, 0
	for index, err := range errorsByIndex {
		switch {
		case err == nil:
			successes++
			if results[index] == nil || len(results[index].Orders) != 1 {
				t.Fatalf("successful result %d = %#v", index, results[index])
			}
		case errors.Is(err, orderdomain.ErrStockInsufficient):
			insufficient++
		default:
			t.Fatalf("concurrent result %d unexpected error = %v", index, err)
		}
	}
	if successes != 1 || insufficient != 1 {
		t.Fatalf("success/insufficient = %d/%d, want 1/1", successes, insufficient)
	}
	assertOrderTransactionCounts(t, database, 1, 1, 1, 1, 2)
	assertOrderSKUStock(t, database, skuID, 0, 1, 0, 1)
	var negativeCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM product_skus WHERE id=? AND (available_stock<0 OR locked_stock<0 OR sold_stock<0)", skuID).Scan(&negativeCount); err != nil || negativeCount != 0 {
		t.Fatalf("negative inventory rows = %d, %v", negativeCount, err)
	}
}
