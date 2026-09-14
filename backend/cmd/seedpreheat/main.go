package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/promotion"

	"github.com/joho/godotenv"
)

const seedCampaignNo = "FS-SEED-20260824-01"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	_ = godotenv.Load()
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	cfg, _, err := config.LoadLocalDev(configPath)
	if err != nil {
		return err
	}
	database, err := db.NewDB(cfg.Database)
	if err != nil {
		return err
	}
	defer db.CloseDB(database)
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		return err
	}
	defer cache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cache.Ping(ctx); err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	var campaign struct {
		ID      uint64 `gorm:"column:id"`
		OwnerID uint64 `gorm:"column:owner_id"`
	}
	if err := database.WithContext(ctx).Table("flash_sale_campaigns AS campaign").
		Select("campaign.id,shop.owner_id").
		Joins("JOIN product_skus AS sku ON sku.id=campaign.sku_id").
		Joins("JOIN products AS product ON product.id=sku.product_id").
		Joins("JOIN shops AS shop ON shop.id=product.shop_id").
		Where("campaign.campaign_no=?", seedCampaignNo).Take(&campaign).Error; err != nil {
		return fmt.Errorf("查询种子秒杀活动: %w", err)
	}
	repository, err := promotion.NewRepository(database)
	if err != nil {
		return err
	}
	service, err := promotion.NewService(repository, cache)
	if err != nil {
		return err
	}
	result, err := service.PreheatCampaign(ctx, campaign.OwnerID, campaign.ID)
	if err != nil {
		return err
	}
	fmt.Printf("FLASH_SALE_ID=%d\nFLASH_SALE_VERSION=%d\n", result.CampaignID, result.CampaignVersion)
	return nil
}
