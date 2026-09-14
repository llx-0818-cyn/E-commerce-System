package promotion

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	paymentdomain "feedsystem_video_go/internal/payment"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPaidTimeoutAndFailureCompensationPreserveCampaignAndDatabaseInventory(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀支付/超时/补偿组合测试")
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
	promotionService, _ := NewService(promotionRepository, cache, orderService)
	provider, _ := paymentdomain.NewMockPaymentProvider("/api/mock-payments/cashier", "0123456789abcdef0123456789abcdef")
	paymentRepository, _ := paymentdomain.NewRepository(gormDB)
	paymentService, _ := paymentdomain.NewService(paymentRepository, provider, orderService)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	promotionService.now = func() time.Time { return now }
	_, skuID := seedPromotionCatalog(t, database, 1901, 2000, 3)
	for _, buyerID := range []uint64{7, 8, 9} {
		seedPromotionAddress(t, database, buyerID)
	}
	campaign, err := promotionService.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 1901, SKUID: uint64(skuID), Title: "支付超时补偿组合",
		PriceCent: 1000, TotalStock: 3, PerUserLimit: 1,
		StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create combination campaign: %v", err)
	}
	_, _ = promotionService.SetCampaignEnabled(ctx, 1901, campaign.ID, true)
	preheated, err := promotionService.PreheatCampaign(ctx, 1901, campaign.ID)
	if err != nil {
		t.Fatalf("preheat combination campaign: %v", err)
	}

	paidRequest, paidOrderID := acceptAndCreateSeckillOrder(t, ctx, database, promotionService, campaign.ID, 7, "paid-path")
	paymentSession, err := paymentService.CreatePayment(ctx, paymentdomain.CreatePaymentInput{BuyerID: 7, OrderID: paidOrderID, AmountCent: 1000})
	if err != nil {
		t.Fatalf("create seckill payment: %v", err)
	}
	callback, _ := provider.Simulate(ctx, paymentSession.Payment.PaymentNo, paymentdomain.CallbackSucceeded)
	if outcome, err := paymentService.ProcessCallback(ctx, callback); err != nil || outcome == nil || !outcome.Applied {
		t.Fatalf("pay seckill order = %#v, %v", outcome, err)
	}
	processSeckillLifecycleEvent(t, ctx, database, promotionService, paidOrderID, orderdomain.OrderPaidEvent)
	processSeckillLifecycleEvent(t, ctx, database, promotionService, paidOrderID, orderdomain.OrderPaidEvent)

	canceledRequest, canceledOrderID := acceptAndCreateSeckillOrder(t, ctx, database, promotionService, campaign.ID, 8, "timeout-path")
	if _, err := database.Exec("UPDATE orders SET created_at=CURRENT_TIMESTAMP(6)-INTERVAL 2 SECOND,expire_at=CURRENT_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", canceledOrderID); err != nil {
		t.Fatalf("expire seckill order: %v", err)
	}
	if _, err := orderService.ExpireOrder(ctx, canceledOrderID, time.Now().UTC()); err != nil {
		t.Fatalf("close expired seckill order: %v", err)
	}
	processSeckillLifecycleEvent(t, ctx, database, promotionService, canceledOrderID, orderdomain.OrderCanceledEvent)
	processSeckillLifecycleEvent(t, ctx, database, promotionService, canceledOrderID, orderdomain.OrderCanceledEvent)

	failedRequest, err := promotionService.ReserveCampaign(ctx, campaign.ID, 9, "failure-path", 1)
	if err != nil || failedRequest.Code != ReservationAccepted {
		t.Fatalf("reserve failure path = %#v, %v", failedRequest, err)
	}
	var productID uint64
	if err := database.QueryRow("SELECT product_id FROM product_skus WHERE id=?", skuID).Scan(&productID); err != nil {
		t.Fatalf("load campaign product: %v", err)
	}
	if _, err := database.Exec("UPDATE products SET status='off_sale' WHERE id=?", productID); err != nil {
		t.Fatalf("off-sale product for compensation: %v", err)
	}
	var requestedEventID, requestedPayload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", failedRequest.RequestID, SeckillOrderRequestedEvent).Scan(&requestedEventID, &requestedPayload); err != nil {
		t.Fatalf("load failure path request event: %v", err)
	}
	if err := promotionService.ProcessOrderRequest(ctx, requestedEventID, []byte(requestedPayload)); err != nil {
		t.Fatalf("process failure path order: %v", err)
	}
	var failedEventID, failedPayload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", failedRequest.RequestID, SeckillOrderFailedEvent).Scan(&failedEventID, &failedPayload); err != nil {
		t.Fatalf("load failure path compensation event: %v", err)
	}
	if err := promotionService.ProcessCompensation(ctx, failedEventID, []byte(failedPayload)); err != nil {
		t.Fatalf("process failure path compensation: %v", err)
	}

	assertFlashSaleRequestStatus(t, database, paidRequest.RequestID, "paid")
	assertFlashSaleRequestStatus(t, database, canceledRequest.RequestID, "canceled")
	assertFlashSaleRequestStatus(t, database, failedRequest.RequestID, "compensated")
	assertReservationResultCode(t, ctx, rawRedis, cache, campaign, paidRequest.RequestID, ReservationPaid)
	assertReservationResultCode(t, ctx, rawRedis, cache, campaign, canceledRequest.RequestID, ReservationCanceled)
	assertReservationResultCode(t, ctx, rawRedis, cache, campaign, failedRequest.RequestID, ReservationFailed)
	if stock, _ := rawRedis.Get(ctx, preheated.StockKey).Int64(); stock != 2 {
		t.Fatalf("combined Redis stock = %d, want 2", stock)
	}
	if paidReserved, _ := rawRedis.HGet(ctx, preheated.BuyersKey, "7").Int64(); paidReserved != 1 {
		t.Fatalf("paid buyer reservation = %d, want 1", paidReserved)
	}
	for _, buyer := range []string{"8", "9"} {
		if reserved, _ := rawRedis.HGet(ctx, preheated.BuyersKey, buyer).Int64(); reserved != 0 {
			t.Fatalf("released buyer %s reservation = %d", buyer, reserved)
		}
	}
	assertPromotionSKUStock(t, database, skuID, 2, 0, 1)
	if redisStock, _ := rawRedis.Get(ctx, preheated.StockKey).Int64(); redisStock+1 != campaign.TotalStock {
		t.Fatalf("campaign conservation remaining=%d paid=1 total=%d", redisStock, campaign.TotalStock)
	}
}

func acceptAndCreateSeckillOrder(t *testing.T, ctx context.Context, database *sql.DB, service *Service, campaignID, buyerID uint64, key string) (*ReservationResult, uint64) {
	t.Helper()
	accepted, err := service.ReserveCampaign(ctx, campaignID, buyerID, key, 1)
	if err != nil || accepted.Code != ReservationAccepted {
		t.Fatalf("reserve %s = %#v, %v", key, accepted, err)
	}
	var eventID, payload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", accepted.RequestID, SeckillOrderRequestedEvent).Scan(&eventID, &payload); err != nil {
		t.Fatalf("load %s request event: %v", key, err)
	}
	if err := service.ProcessOrderRequest(ctx, eventID, []byte(payload)); err != nil {
		t.Fatalf("process %s request: %v", key, err)
	}
	status, err := service.GetReservationStatus(ctx, buyerID, accepted.RequestID)
	if err != nil || status.Code != ReservationOrderCreated || status.OrderID == 0 {
		t.Fatalf("load %s order result = %#v, %v", key, status, err)
	}
	return accepted, status.OrderID
}

func processSeckillLifecycleEvent(t *testing.T, ctx context.Context, database *sql.DB, service *Service, orderID uint64, eventType string) {
	t.Helper()
	var eventID, payload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", orderID, eventType).Scan(&eventID, &payload); err != nil {
		t.Fatalf("load lifecycle event %s: %v", eventType, err)
	}
	if err := service.ProcessOrderLifecycle(ctx, eventID, eventType, []byte(payload)); err != nil {
		t.Fatalf("process lifecycle event %s: %v", eventType, err)
	}
}

func assertFlashSaleRequestStatus(t *testing.T, database *sql.DB, requestID, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow("SELECT status FROM flash_sale_requests WHERE request_id=?", requestID).Scan(&got); err != nil || got != want {
		t.Fatalf("request %s status = %s, %v; want %s", requestID, got, err, want)
	}
}

func assertReservationResultCode(t *testing.T, ctx context.Context, raw *redis.Client, cache *rediscache.Client, campaign *FlashSaleCampaign, requestID string, want ReservationCode) {
	t.Helper()
	data, err := raw.Get(ctx, CampaignRequestResultKey(cache, campaign.ID, campaign.CampaignVersion, requestID)).Bytes()
	if err != nil {
		t.Fatalf("load request %s result: %v", requestID, err)
	}
	var result ReservationResult
	if err := json.Unmarshal(data, &result); err != nil || result.Code != want {
		t.Fatalf("request %s result = %#v, %v; want %s", requestID, result, err, want)
	}
}
