package promotion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestConcurrentDuplicateSeckillMessagesCreateOneOrderAndLockStockOnce(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀订单并发幂等测试")
	}
	database, cleanup := openPromotionTestDatabase(t, adminDSN)
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
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	rawRedis := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "test:")
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	orderRepository, _ := orderdomain.NewRepository(gormDB)
	orderService, _ := orderdomain.NewService(orderRepository, 30*time.Minute)
	promotionRepository, _ := NewRepository(gormDB)
	service, _ := NewService(promotionRepository, cache, orderService)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	_, skuID := seedPromotionCatalog(t, database, 2101, 2000, 1)
	seedPromotionAddress(t, database, 7)
	campaign, _ := service.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 2101, SKUID: uint64(skuID), Title: "并发消息活动",
		PriceCent: 1000, TotalStock: 1, PerUserLimit: 1,
		StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	_, _ = service.SetCampaignEnabled(ctx, 2101, campaign.ID, true)
	_, _ = service.PreheatCampaign(ctx, 2101, campaign.ID)
	accepted, err := service.ReserveCampaign(ctx, campaign.ID, 7, "concurrent-consumer", 1)
	if err != nil || accepted.Code != ReservationAccepted {
		t.Fatalf("reserve concurrent consumer request = %#v, %v", accepted, err)
	}
	var payload string
	if err := database.QueryRow("SELECT payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", accepted.RequestID, SeckillOrderRequestedEvent).Scan(&payload); err != nil {
		t.Fatalf("load concurrent consumer payload: %v", err)
	}

	const messages = 20
	errorsByIndex := make([]error, messages)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < messages; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			digest := sha256.Sum256([]byte(accepted.RequestID + ":event:" + string(rune(index))))
			errorsByIndex[index] = service.ProcessOrderRequest(ctx, hex.EncodeToString(digest[:]), []byte(payload))
		}(index)
	}
	close(start)
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("concurrent message %d error = %v", index, err)
		}
	}
	var orderID uint64
	if err := database.QueryRow("SELECT order_id FROM flash_sale_requests WHERE request_id=?", accepted.RequestID).Scan(&orderID); err != nil {
		t.Fatalf("load concurrent request order: %v", err)
	}
	var orders, locks, processed int
	if err := database.QueryRow("SELECT COUNT(*) FROM orders WHERE submission_id=(SELECT submission_id FROM orders WHERE id=?)", orderID).Scan(&orders); err != nil {
		t.Fatalf("count concurrent orders: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='lock'", orderID).Scan(&locks); err != nil {
		t.Fatalf("count concurrent locks: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM processed_events WHERE consumer_name='seckill-order-worker'").Scan(&processed); err != nil {
		t.Fatalf("count concurrent processed events: %v", err)
	}
	if orders != 1 || locks != 1 || processed != messages {
		t.Fatalf("concurrent orders/locks/processed = %d/%d/%d, want 1/1/%d", orders, locks, processed, messages)
	}
	assertPromotionSKUStock(t, database, skuID, 0, 1, 0)
}
