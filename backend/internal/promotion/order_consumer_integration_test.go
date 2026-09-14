package promotion

import (
	"context"
	"database/sql"
	"errors"
	"os"
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

func TestSeckillOrderConsumerCreatesCampaignPriceOrderAndIsMessageIdempotent(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀订单消费者集成测试")
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
	shopID, skuID := seedPromotionCatalog(t, database, 1701, 2000, 20)
	addressID := seedPromotionAddress(t, database, 7)
	_ = addressID
	campaign, err := service.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 1701, SKUID: uint64(skuID), Title: "异步订单活动",
		PriceCent: 1000, TotalStock: 10, PerUserLimit: 1,
		StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create seckill order campaign: %v", err)
	}
	if _, err := service.SetCampaignEnabled(ctx, 1701, campaign.ID, true); err != nil {
		t.Fatalf("enable seckill order campaign: %v", err)
	}
	if _, err := service.PreheatCampaign(ctx, 1701, campaign.ID); err != nil {
		t.Fatalf("preheat seckill order campaign: %v", err)
	}
	accepted, err := service.ReserveCampaign(ctx, campaign.ID, 7, "order-consumer", 1)
	if err != nil || accepted.Code != ReservationAccepted {
		t.Fatalf("ReserveCampaign() = %#v, %v", accepted, err)
	}
	var eventID, payload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", accepted.RequestID, SeckillOrderRequestedEvent).Scan(&eventID, &payload); err != nil {
		t.Fatalf("load seckill order event: %v", err)
	}
	if err := service.ProcessOrderRequest(ctx, eventID, []byte(payload)); err != nil {
		t.Fatalf("ProcessOrderRequest(first) error = %v", err)
	}
	if err := service.ProcessOrderRequest(ctx, eventID, []byte(payload)); err != nil {
		t.Fatalf("ProcessOrderRequest(duplicate) error = %v", err)
	}

	var requestStatus string
	var orderID uint64
	if err := database.QueryRow("SELECT status,order_id FROM flash_sale_requests WHERE request_id=?", accepted.RequestID).Scan(&requestStatus, &orderID); err != nil {
		t.Fatalf("load flash sale request result: %v", err)
	}
	if requestStatus != "order_created" || orderID == 0 {
		t.Fatalf("request status/order = %s/%d", requestStatus, orderID)
	}
	var orderTotal, itemPrice int64
	var orderCount, ledgerCount, processedCount int
	if err := database.QueryRow("SELECT total_cent FROM orders WHERE id=?", orderID).Scan(&orderTotal); err != nil {
		t.Fatalf("load seckill order: %v", err)
	}
	if err := database.QueryRow("SELECT unit_price_cent FROM order_items WHERE order_id=?", orderID).Scan(&itemPrice); err != nil {
		t.Fatalf("load seckill order item: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM orders WHERE submission_id=(SELECT submission_id FROM orders WHERE id=?)", orderID).Scan(&orderCount); err != nil {
		t.Fatalf("count seckill orders: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM inventory_ledger WHERE order_id=? AND movement_type='lock'", orderID).Scan(&ledgerCount); err != nil {
		t.Fatalf("count seckill lock ledger: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM processed_events WHERE consumer_name='seckill-order-worker' AND event_id=?", eventID).Scan(&processedCount); err != nil {
		t.Fatalf("count seckill processed event: %v", err)
	}
	if orderTotal != 1000 || itemPrice != 1000 || orderCount != 1 || ledgerCount != 1 || processedCount != 1 {
		t.Fatalf("seckill order total/item/orders/ledger/processed = %d/%d/%d/%d/%d", orderTotal, itemPrice, orderCount, ledgerCount, processedCount)
	}
	assertPromotionSKUStock(t, database, skuID, 19, 1, 0)
	status, err := service.GetReservationStatus(ctx, 7, accepted.RequestID)
	if err != nil || status.Code != ReservationOrderCreated || status.OrderID != orderID || status.OrderNo == "" || status.PaymentExpiresAt == nil {
		t.Fatalf("GetReservationStatus(final) = %#v, %v", status, err)
	}
	if _, err := service.GetReservationStatus(ctx, 8, accepted.RequestID); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("GetReservationStatus(other buyer) error = %v", err)
	}
	_ = shopID
}

func seedPromotionAddress(t *testing.T, database *sql.DB, buyerID uint64) int64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO addresses (account_id,recipient_name,phone,province,city,detail,is_default) VALUES (?,'秒杀买家','13800000000','浙江省','杭州市','文三路 1 号',TRUE)", buyerID)
	if err != nil {
		t.Fatalf("seed promotion address: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func assertPromotionSKUStock(t *testing.T, database *sql.DB, skuID int64, available, locked, sold int64) {
	t.Helper()
	var gotAvailable, gotLocked, gotSold int64
	if err := database.QueryRow("SELECT available_stock,locked_stock,sold_stock FROM product_skus WHERE id=?", skuID).Scan(&gotAvailable, &gotLocked, &gotSold); err != nil {
		t.Fatalf("query promotion SKU stock: %v", err)
	}
	if gotAvailable != available || gotLocked != locked || gotSold != sold {
		t.Fatalf("promotion SKU stock = %d/%d/%d, want %d/%d/%d", gotAvailable, gotLocked, gotSold, available, locked, sold)
	}
}
