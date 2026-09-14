package promotion

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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

func TestOrderFailureEventCompensationIsIdempotentAndNeverExceedsCampaignStock(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀失败补偿集成测试")
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
	_, skuID := seedPromotionCatalog(t, database, 1801, 2000, 1)
	seedPromotionAddress(t, database, 7)
	campaign, err := service.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 1801, SKUID: uint64(skuID), Title: "失败补偿活动",
		PriceCent: 1000, TotalStock: 1, PerUserLimit: 1,
		StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create compensation campaign: %v", err)
	}
	_, _ = service.SetCampaignEnabled(ctx, 1801, campaign.ID, true)
	preheated, err := service.PreheatCampaign(ctx, 1801, campaign.ID)
	if err != nil {
		t.Fatalf("preheat compensation campaign: %v", err)
	}
	accepted, err := service.ReserveCampaign(ctx, campaign.ID, 7, "compensate-request", 1)
	if err != nil || accepted.Code != ReservationAccepted {
		t.Fatalf("reserve compensation request = %#v, %v", accepted, err)
	}
	if stock, _ := rawRedis.Get(ctx, preheated.StockKey).Int64(); stock != 0 {
		t.Fatalf("reserved Redis stock = %d, want 0", stock)
	}
	if _, err := database.Exec("UPDATE product_skus SET available_stock=0 WHERE id=?", skuID); err != nil {
		t.Fatalf("force MySQL stock failure: %v", err)
	}
	var requestedEventID, requestedPayload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", accepted.RequestID, SeckillOrderRequestedEvent).Scan(&requestedEventID, &requestedPayload); err != nil {
		t.Fatalf("load requested event: %v", err)
	}
	if err := service.ProcessOrderRequest(ctx, requestedEventID, []byte(requestedPayload)); err != nil {
		t.Fatalf("ProcessOrderRequest(terminal failure) error = %v", err)
	}
	var requestStatus string
	var orderCount int
	if err := database.QueryRow("SELECT status FROM flash_sale_requests WHERE request_id=?", accepted.RequestID).Scan(&requestStatus); err != nil {
		t.Fatalf("load failed request status: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM orders WHERE submission_id IN (SELECT id FROM order_submissions WHERE idempotency_key=?)", "seckill:"+accepted.RequestID).Scan(&orderCount); err != nil {
		t.Fatalf("count failed seckill orders: %v", err)
	}
	if requestStatus != "failed" || orderCount != 0 {
		t.Fatalf("failed request/order count = %s/%d", requestStatus, orderCount)
	}
	var failedEventID, failedPayload string
	if err := database.QueryRow("SELECT event_id,payload_json FROM outbox_events WHERE aggregate_id=? AND event_type=?", accepted.RequestID, SeckillOrderFailedEvent).Scan(&failedEventID, &failedPayload); err != nil {
		t.Fatalf("load failure event: %v", err)
	}
	if err := service.ProcessCompensation(ctx, failedEventID, []byte(failedPayload)); err != nil {
		t.Fatalf("ProcessCompensation(first) error = %v", err)
	}
	if err := service.ProcessCompensation(ctx, failedEventID, []byte(failedPayload)); err != nil {
		t.Fatalf("ProcessCompensation(same event) error = %v", err)
	}
	secondEventID := strings.Repeat("f", 64)
	if secondEventID == failedEventID {
		secondEventID = strings.Repeat("e", 64)
	}
	if err := service.ProcessCompensation(ctx, secondEventID, []byte(failedPayload)); err != nil {
		t.Fatalf("ProcessCompensation(different event) error = %v", err)
	}
	if stock, _ := rawRedis.Get(ctx, preheated.StockKey).Int64(); stock != 1 {
		t.Fatalf("compensated Redis stock = %d, want capped 1", stock)
	}
	if reserved, _ := rawRedis.HGet(ctx, preheated.BuyersKey, "7").Int64(); reserved != 0 {
		t.Fatalf("compensated buyer reservation = %d, want 0", reserved)
	}
	if err := database.QueryRow("SELECT status FROM flash_sale_requests WHERE request_id=?", accepted.RequestID).Scan(&requestStatus); err != nil || requestStatus != "compensated" {
		t.Fatalf("compensated request status = %s, %v", requestStatus, err)
	}
	resultBytes, err := rawRedis.Get(ctx, CampaignRequestResultKey(cache, campaign.ID, campaign.CampaignVersion, accepted.RequestID)).Bytes()
	if err != nil {
		t.Fatalf("load compensated result: %v", err)
	}
	var finalResult ReservationResult
	if err := json.Unmarshal(resultBytes, &finalResult); err != nil || finalResult.Code != ReservationFailed {
		t.Fatalf("compensated query result = %#v, %v", finalResult, err)
	}
	var failureEvents int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=? AND event_type=?", accepted.RequestID, SeckillOrderFailedEvent).Scan(&failureEvents); err != nil || failureEvents != 1 {
		t.Fatalf("failure event count = %d, %v; want 1", failureEvents, err)
	}
}
