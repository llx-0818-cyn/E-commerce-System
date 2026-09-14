package promotion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	mysql "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCampaignCreateEnableQueryVersionedPreheatRepeatAndExpiry(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀活动与预热集成测试")
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
	shopID, skuID := seedPromotionCatalog(t, database, 1501, 2000, 100)
	_ = shopID

	campaign, err := service.CreateCampaign(ctx, CreateCampaignInput{
		OwnerID: 1501, SKUID: uint64(skuID), Title: "版本化预热活动",
		PriceCent: 1000, TotalStock: 20, PerUserLimit: 2,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour),
	})
	if err != nil || campaign.Status != CampaignStatusDraft || campaign.CampaignVersion != 1 {
		t.Fatalf("CreateCampaign() = %#v, %v", campaign, err)
	}
	if _, err := service.SetCampaignEnabled(ctx, 999, campaign.ID, true); !errors.Is(err, ErrCampaignForbidden) {
		t.Fatalf("SetCampaignEnabled(other owner) error = %v", err)
	}
	enabled, err := service.SetCampaignEnabled(ctx, 1501, campaign.ID, true)
	if err != nil || enabled.Status != CampaignStatusEnabled || enabled.CampaignVersion != 1 {
		t.Fatalf("SetCampaignEnabled() = %#v, %v", enabled, err)
	}
	loaded, err := service.GetCampaign(ctx, campaign.ID)
	if err != nil || loaded.Status != CampaignStatusEnabled {
		t.Fatalf("GetCampaign() = %#v, %v", loaded, err)
	}
	first, err := service.PreheatCampaign(ctx, 1501, campaign.ID)
	if err != nil || !first.StockInitialized || first.CampaignVersion != 1 {
		t.Fatalf("PreheatCampaign(first) = %#v, %v", first, err)
	}
	if stock, err := rawRedis.Get(ctx, first.StockKey).Int64(); err != nil || stock != 20 {
		t.Fatalf("preheated stock = %d, %v", stock, err)
	}
	if _, err := rawRedis.DecrBy(ctx, first.StockKey, 1).Result(); err != nil {
		t.Fatalf("decrement preheated stock: %v", err)
	}
	repeated, err := service.PreheatCampaign(ctx, 1501, campaign.ID)
	if err != nil || repeated.StockInitialized || repeated.StockKey != first.StockKey {
		t.Fatalf("PreheatCampaign(repeat) = %#v, %v", repeated, err)
	}
	if stock, _ := rawRedis.Get(ctx, repeated.StockKey).Int64(); stock != 19 {
		t.Fatalf("repeat preheat reset stock to %d, want 19", stock)
	}
	if metadata, err := rawRedis.Get(ctx, first.MetadataKey).Result(); err != nil || metadata == "" {
		t.Fatalf("preheated metadata = %q, %v", metadata, err)
	}

	disabled, err := service.SetCampaignEnabled(ctx, 1501, campaign.ID, false)
	if err != nil || disabled.Status != CampaignStatusDisabled {
		t.Fatalf("disable campaign = %#v, %v", disabled, err)
	}
	if _, err := service.PreheatCampaign(ctx, 1501, campaign.ID); !errors.Is(err, ErrCampaignUnavailable) {
		t.Fatalf("preheat disabled error = %v", err)
	}
	reenabled, err := service.SetCampaignEnabled(ctx, 1501, campaign.ID, true)
	if err != nil || reenabled.CampaignVersion != 2 {
		t.Fatalf("reenable campaign = %#v, %v", reenabled, err)
	}
	versionTwo, err := service.PreheatCampaign(ctx, 1501, campaign.ID)
	if err != nil || !versionTwo.StockInitialized || versionTwo.StockKey == first.StockKey {
		t.Fatalf("preheat version two = %#v, %v", versionTwo, err)
	}
	if oldStock, _ := rawRedis.Get(ctx, first.StockKey).Int64(); oldStock != 19 {
		t.Fatalf("version two changed old stock to %d", oldStock)
	}
	service.now = func() time.Time { return now.Add(90 * time.Minute) }
	reservation, err := service.ReserveCampaign(ctx, campaign.ID, 7, "status-query", 1)
	if err != nil || reservation.Code != ReservationAccepted || reservation.RequestID == "" || reservation.BuyerID != 7 {
		t.Fatalf("ReserveCampaign() = %#v, %v", reservation, err)
	}
	status, err := service.GetReservationStatus(ctx, 7, reservation.RequestID)
	if err != nil || status.RequestID != reservation.RequestID || status.ReservationToken != reservation.ReservationToken {
		t.Fatalf("GetReservationStatus() = %#v, %v", status, err)
	}
	if _, err := service.GetReservationStatus(ctx, 8, reservation.RequestID); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("GetReservationStatus(other buyer) error = %v", err)
	}

	expiredResult, err := database.Exec(`
INSERT INTO flash_sale_campaigns
    (campaign_no,sku_id,title,status,price_cent,total_stock,per_user_limit,starts_at,ends_at)
VALUES ('EXPIRED-PREHEAT',?,'已过期活动','enabled',1000,10,1,?,?)`, skuID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("insert expired campaign: %v", err)
	}
	expiredID, _ := expiredResult.LastInsertId()
	if _, err := service.PreheatCampaign(ctx, 1501, uint64(expiredID)); !errors.Is(err, ErrCampaignExpired) {
		t.Fatalf("PreheatCampaign(expired) error = %v", err)
	}
}

func openPromotionTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
	t.Helper()
	adminConfig, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatalf("parse MIGRATION_TEST_DSN: %v", err)
	}
	adminConfig.DBName = ""
	adminConfig.MultiStatements = true
	adminConfig.ParseTime = true
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	databaseName := fmt.Sprintf("feedsystem_promotion_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create promotion test database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open promotion test database: %v", err)
	}
	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop promotion test database: %v", err)
		}
		adminDB.Close()
	}
	return database, cleanup
}

func seedPromotionCatalog(t *testing.T, database *sql.DB, ownerID uint64, price, stock int64) (int64, int64) {
	t.Helper()
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?,'秒杀 Service 店','open')", ownerID)
	if err != nil {
		t.Fatalf("seed promotion shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'秒杀 Service 商品','详情','on_sale')", shopID)
	if err != nil {
		t.Fatalf("seed promotion product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'PROMOTION-SKU',JSON_OBJECT(),?,?,'active')", productID, price, stock)
	if err != nil {
		t.Fatalf("seed promotion SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	return shopID, skuID
}
