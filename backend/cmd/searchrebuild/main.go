package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	"feedsystem_video_go/internal/search"

	"github.com/joho/godotenv"
)

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
	if cfg.Search.Provider != "elasticsearch" {
		return fmt.Errorf("SEARCH_PROVIDER 必须为 elasticsearch 才能执行索引重建")
	}
	database, err := db.NewDB(cfg.Database)
	if err != nil {
		return err
	}
	defer db.CloseDB(database)
	repository, err := search.NewRepository(database)
	if err != nil {
		return err
	}
	adapter, err := search.NewElasticsearchAdapter(cfg.Search.Elasticsearch)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	index, err := adapter.Rebuild(ctx, repository)
	if err != nil {
		return err
	}
	log.Printf("Elasticsearch 全量重建完成并切换别名，当前物理索引=%s", index)
	return nil
}
