package promotion

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestFlashSaleRejectsNewReservationWhenRedisFailsWithoutTouchingDatabaseStock(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀 Redis 故障测试")
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
	_, skuID := seedPromotionCatalog(t, database, 2201, 2000, 10)
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rawRedis := goredis.NewClient(&goredis.Options{Addr: mini.Addr(), MaxRetries: 0})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "test:")
	repository, _ := NewRepository(gormDB)
	service, _ := NewService(repository, cache)
	now := time.Now().UTC()
	campaign, err := service.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 2201, SKUID: uint64(skuID), Title: "Redis 故障活动", PriceCent: 1000,
		TotalStock: 10, PerUserLimit: 1, StartsAt: now.Add(-time.Minute), EndsAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateCampaign() error = %v", err)
	}
	if _, err := service.SetCampaignEnabled(ctx, 2201, campaign.ID, true); err != nil {
		t.Fatalf("SetCampaignEnabled() error = %v", err)
	}
	if _, err := service.PreheatCampaign(ctx, 2201, campaign.ID); err != nil {
		t.Fatalf("PreheatCampaign() error = %v", err)
	}
	mini.Close()

	result, err := service.ReserveCampaign(ctx, campaign.ID, 7, "redis-down-request", 1)
	if result != nil || !errors.Is(err, ErrPreheatUnavailable) {
		t.Fatalf("ReserveCampaign() with Redis down = %#v, %v", result, err)
	}
	for table, want := range map[string]int{"flash_sale_requests": 0, "orders": 0, "inventory_ledger": 0} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count = %d, %v; want %d", table, count, err, want)
		}
	}
	var available, locked, sold int64
	if err := database.QueryRow("SELECT available_stock,locked_stock,sold_stock FROM product_skus WHERE id=?", skuID).
		Scan(&available, &locked, &sold); err != nil || available != 10 || locked != 0 || sold != 0 {
		t.Fatalf("database stock after Redis failure = %d/%d/%d, %v", available, locked, sold, err)
	}
}
