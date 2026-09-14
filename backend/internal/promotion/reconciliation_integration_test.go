package promotion

import (
	"context"
	"database/sql"
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

func TestReconciliationAuditsActiveDifferenceSafelyRepairsEndedStockAndFlagsStateMismatch(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀对账集成测试")
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
	_, skuID := seedPromotionCatalog(t, database, 2001, 2000, 2)
	seedPromotionAddress(t, database, 7)
	campaign, err := promotionService.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 2001, SKUID: uint64(skuID), Title: "对账活动",
		PriceCent: 1000, TotalStock: 2, PerUserLimit: 1,
		StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create reconciliation campaign: %v", err)
	}
	_, _ = promotionService.SetCampaignEnabled(ctx, 2001, campaign.ID, true)
	preheated, _ := promotionService.PreheatCampaign(ctx, 2001, campaign.ID)
	request, orderID := acceptAndCreateSeckillOrder(t, ctx, database, promotionService, campaign.ID, 7, "reconcile-paid")
	session, _ := paymentService.CreatePayment(ctx, paymentdomain.CreatePaymentInput{BuyerID: 7, OrderID: orderID, AmountCent: 1000})
	callback, _ := provider.Simulate(ctx, session.Payment.PaymentNo, paymentdomain.CallbackSucceeded)
	if _, err := paymentService.ProcessCallback(ctx, callback); err != nil {
		t.Fatalf("pay reconciliation order: %v", err)
	}
	processSeckillLifecycleEvent(t, ctx, database, promotionService, orderID, orderdomain.OrderPaidEvent)

	if err := rawRedis.Set(ctx, preheated.StockKey, 0, time.Hour).Err(); err != nil {
		t.Fatalf("tamper active Redis stock: %v", err)
	}
	activeResult, err := promotionService.ReconcileCampaign(ctx, campaign.ID)
	if err != nil || activeResult.Repaired || activeResult.ExpectedRemaining != 1 || activeResult.ActualRemaining != 0 {
		t.Fatalf("active reconciliation = %#v, %v", activeResult, err)
	}
	assertReconciliationAnomaly(t, database, campaign.ID, "stock:v1", "open")
	if stock, _ := rawRedis.Get(ctx, preheated.StockKey).Int64(); stock != 0 {
		t.Fatalf("active reconciliation unsafely repaired stock to %d", stock)
	}

	promotionService.now = func() time.Time { return now.Add(2 * time.Hour) }
	endedResult, err := promotionService.ReconcileCampaign(ctx, campaign.ID)
	if err != nil || !endedResult.Repaired || endedResult.ActualRemaining != 1 {
		t.Fatalf("ended reconciliation = %#v, %v", endedResult, err)
	}
	if stock, _ := rawRedis.Get(ctx, preheated.StockKey).Int64(); stock != 1 {
		t.Fatalf("ended reconciliation stock = %d, want 1", stock)
	}
	assertReconciliationAnomaly(t, database, campaign.ID, "stock:v1", "resolved")

	if err := rawRedis.HSet(ctx, preheated.BuyersKey, "7", 2).Err(); err != nil {
		t.Fatalf("tamper buyer reservations: %v", err)
	}
	reservationMismatch, err := promotionService.ReconcileCampaign(ctx, campaign.ID)
	if err != nil || reservationMismatch.ActualReservations != 2 || reservationMismatch.ExpectedReservations != 1 {
		t.Fatalf("reservation reconciliation = %#v, %v", reservationMismatch, err)
	}
	assertReconciliationAnomaly(t, database, campaign.ID, "reservation:v1", "open")
	if err := rawRedis.HSet(ctx, preheated.BuyersKey, "7", 1).Err(); err != nil {
		t.Fatalf("restore buyer reservations: %v", err)
	}
	if _, err := database.Exec("UPDATE flash_sale_requests SET status='order_created' WHERE request_id=?", request.RequestID); err != nil {
		t.Fatalf("tamper request status: %v", err)
	}
	stateMismatch, err := promotionService.ReconcileCampaign(ctx, campaign.ID)
	if err != nil || stateMismatch.Anomalies == 0 {
		t.Fatalf("state reconciliation = %#v, %v", stateMismatch, err)
	}
	assertReconciliationAnomaly(t, database, campaign.ID, "reservation:v1", "resolved")
	assertReconciliationAnomaly(t, database, campaign.ID, "request:"+request.RequestID, "open")
	if cycles, err := promotionService.ReconcileCampaignCycle(ctx, 100); err != nil || cycles == 0 {
		t.Fatalf("ReconcileCampaignCycle() = %d, %v", cycles, err)
	}
}

func assertReconciliationAnomaly(t *testing.T, database *sql.DB, campaignID uint64, key, wantStatus string) {
	t.Helper()
	var status string
	var detail string
	if err := database.QueryRow("SELECT status,detail FROM flash_sale_reconciliation_anomalies WHERE campaign_id=? AND anomaly_key=?", campaignID, key).Scan(&status, &detail); err != nil {
		t.Fatalf("load reconciliation anomaly %s: %v", key, err)
	}
	if status != wantStatus || detail == "" {
		t.Fatalf("reconciliation anomaly %s = %s/%q, want %s/nonempty", key, status, detail, wantStatus)
	}
}
