package promotion

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAcceptedReservationPersistsOutboxAcrossPublisherDisconnectAndRecovery(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀受理 Outbox 恢复集成测试")
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
	repository, _ := NewRepository(gormDB)
	service, _ := NewService(repository, cache)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	_, skuID := seedPromotionCatalog(t, database, 1601, 2000, 20)
	campaign, err := service.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 1601, SKUID: uint64(skuID), Title: "可靠受理活动",
		PriceCent: 1000, TotalStock: 10, PerUserLimit: 1,
		StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create reliable campaign: %v", err)
	}
	if _, err := service.SetCampaignEnabled(ctx, 1601, campaign.ID, true); err != nil {
		t.Fatalf("enable reliable campaign: %v", err)
	}
	if _, err := service.PreheatCampaign(ctx, 1601, campaign.ID); err != nil {
		t.Fatalf("preheat reliable campaign: %v", err)
	}
	accepted, err := service.ReserveCampaign(ctx, campaign.ID, 7, "reliable-request", 1)
	if err != nil || accepted.Code != ReservationAccepted {
		t.Fatalf("ReserveCampaign() = %#v, %v", accepted, err)
	}
	replayed, err := service.ReserveCampaign(ctx, campaign.ID, 7, "reliable-request", 1)
	if err != nil || !replayed.Duplicate || replayed.RequestID != accepted.RequestID {
		t.Fatalf("ReserveCampaign(replay) = %#v, %v", replayed, err)
	}
	assertReservationOutboxCounts(t, database, accepted.RequestID, 1, 1, "pending")

	publisher := &recoveringSeckillPublisher{failure: errors.New("rabbitmq disconnected")}
	config := domainevent.DefaultPollerConfig("seckill-recovery-test")
	config.BaseBackoff = time.Millisecond
	config.MaxBackoff = time.Millisecond
	poller, err := domainevent.NewPoller(gormDB, publisher, config)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	failed, err := poller.PollOnce(ctx)
	if err != nil || failed.Retried != 1 || failed.Published != 0 {
		t.Fatalf("PollOnce(disconnected) = %#v, %v", failed, err)
	}
	assertReservationOutboxCounts(t, database, accepted.RequestID, 1, 1, "pending")
	if _, err := database.Exec("UPDATE outbox_events SET next_attempt_at=CURRENT_TIMESTAMP(6) WHERE event_type=?", SeckillOrderRequestedEvent); err != nil {
		t.Fatalf("make seckill outbox retry due: %v", err)
	}
	publisher.failure = nil
	recovered, err := poller.PollOnce(ctx)
	if err != nil || recovered.Published != 1 || len(publisher.events) != 1 || publisher.events[0].EventType != SeckillOrderRequestedEvent {
		t.Fatalf("PollOnce(recovered) = %#v events=%#v err=%v", recovered, publisher.events, err)
	}
	assertReservationOutboxCounts(t, database, accepted.RequestID, 1, 1, "published")
}

type recoveringSeckillPublisher struct {
	failure error
	events  []domainevent.OutboxEvent
}

func (p *recoveringSeckillPublisher) Publish(_ context.Context, value domainevent.OutboxEvent) error {
	if p.failure != nil {
		return p.failure
	}
	p.events = append(p.events, value)
	return nil
}

func assertReservationOutboxCounts(t *testing.T, database *sql.DB, requestID string, wantRequests, wantEvents int, wantStatus string) {
	t.Helper()
	var requestCount, eventCount int
	var status string
	if err := database.QueryRow("SELECT COUNT(*) FROM flash_sale_requests WHERE request_id=?", requestID).Scan(&requestCount); err != nil {
		t.Fatalf("count flash sale request: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*),COALESCE(MAX(status),'') FROM outbox_events WHERE aggregate_id=? AND event_type=?", requestID, SeckillOrderRequestedEvent).Scan(&eventCount, &status); err != nil {
		t.Fatalf("count flash sale outbox: %v", err)
	}
	if requestCount != wantRequests || eventCount != wantEvents || status != wantStatus {
		t.Fatalf("request/event/status = %d/%d/%s, want %d/%d/%s", requestCount, eventCount, status, wantRequests, wantEvents, wantStatus)
	}
}
