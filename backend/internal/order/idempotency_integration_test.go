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

func TestCreateOrderGroupIdempotencyReplayConflictAndConcurrentDuplicate(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单组幂等集成测试")
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

	addressID := insertOrderTestAddress(t, database, 77)
	shopOne, skuOne := insertOrderServiceCatalog(t, database, 301, "幂等店一", "IDEMPOTENT-SKU-1", 1000, 5)
	shopTwo, skuTwo := insertOrderServiceCatalog(t, database, 302, "幂等店二", "IDEMPOTENT-SKU-2", 2000, 5)
	input := idempotentOrderInput(77, "idem-sequential", addressID, shopOne, skuOne, shopTwo, skuTwo)

	first, err := service.CreateOrderGroup(ctx, input)
	if err != nil {
		t.Fatalf("CreateOrderGroup(first) error = %v", err)
	}
	reordered := input
	reordered.Drafts = []orderdomain.CreateShopOrderInput{input.Drafts[1], input.Drafts[0]}
	replayed, err := service.CreateOrderGroup(ctx, reordered)
	if err != nil {
		t.Fatalf("CreateOrderGroup(replay) error = %v", err)
	}
	assertSameOrderGroup(t, first, replayed)
	assertOrderTransactionCounts(t, database, 1, 2, 2, 2, 4)
	assertOrderSKUStock(t, database, skuOne, 4, 1, 0, 1)
	assertOrderSKUStock(t, database, skuTwo, 4, 1, 0, 1)

	conflicting := input
	conflicting.Drafts = cloneOrderDrafts(input.Drafts)
	conflicting.Drafts[0].Items[0].Quantity = 2
	if group, err := service.CreateOrderGroup(ctx, conflicting); group != nil || !errors.Is(err, orderdomain.ErrIdempotencyConflict) {
		t.Fatalf("CreateOrderGroup(conflict) = %#v, %v", group, err)
	}
	assertOrderTransactionCounts(t, database, 1, 2, 2, 2, 4)

	concurrentInput := idempotentOrderInput(77, "idem-concurrent", addressID, shopOne, skuOne, shopTwo, skuTwo)
	start := make(chan struct{})
	results := make([]*orderdomain.OrderGroup, 2)
	errorsByIndex := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByIndex[index] = service.CreateOrderGroup(ctx, concurrentInput)
		}(index)
	}
	close(start)
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("concurrent request %d error = %v", index, err)
		}
	}
	assertSameOrderGroup(t, results[0], results[1])
	assertOrderTransactionCounts(t, database, 2, 4, 4, 4, 8)
	assertOrderSKUStock(t, database, skuOne, 3, 2, 0, 2)
	assertOrderSKUStock(t, database, skuTwo, 3, 2, 0, 2)
}

func idempotentOrderInput(buyerID uint64, key string, addressID, shopOne, skuOne, shopTwo, skuTwo int64) orderdomain.CreateOrderGroupInput {
	return orderdomain.CreateOrderGroupInput{
		BuyerID: buyerID, IdempotencyKey: key, AddressID: uint64(addressID),
		Drafts: []orderdomain.CreateShopOrderInput{
			{ShopID: uint64(shopOne), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuOne), Quantity: 1, ExpectedPriceCent: 1000}}},
			{ShopID: uint64(shopTwo), Items: []orderdomain.CreateOrderItemInput{{SKUID: uint64(skuTwo), Quantity: 1, ExpectedPriceCent: 2000}}},
		},
	}
}

func cloneOrderDrafts(values []orderdomain.CreateShopOrderInput) []orderdomain.CreateShopOrderInput {
	result := make([]orderdomain.CreateShopOrderInput, len(values))
	for index, draft := range values {
		result[index] = orderdomain.CreateShopOrderInput{ShopID: draft.ShopID, Items: append([]orderdomain.CreateOrderItemInput(nil), draft.Items...)}
	}
	return result
}

func assertSameOrderGroup(t *testing.T, left, right *orderdomain.OrderGroup) {
	t.Helper()
	if left == nil || right == nil || left.Submission.ID != right.Submission.ID || len(left.Orders) != len(right.Orders) {
		t.Fatalf("order groups differ: left=%#v right=%#v", left, right)
	}
	for index := range left.Orders {
		if left.Orders[index].ID != right.Orders[index].ID || left.Orders[index].OrderNo != right.Orders[index].OrderNo ||
			left.Orders[index].TotalCent != right.Orders[index].TotalCent {
			t.Fatalf("order groups differ at %d: left=%#v right=%#v", index, left.Orders[index], right.Orders[index])
		}
	}
}
