package content_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"feedsystem_video_go/internal/cacheinvalidation"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestFailedPostCacheDeletePersistsEventAndEventuallyRefreshes(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容缓存失效恢复测试")
	}
	database, cleanup := openContentTestDatabase(t, adminDSN)
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	hook := &failDeleteHook{}
	rawRedis := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	rawRedis.AddHook(hook)
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	repository, _ := content.NewRepository(gormDB)
	reader, err := content.NewPublicEntityReader(repository, cache)
	if err != nil {
		t.Fatalf("NewPublicEntityReader() error = %v", err)
	}
	service, err := content.NewService(repository, config.ContentConfig{
		MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000,
	}, reader)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	post, err := service.Publish(ctx, content.PublishInput{AuthorID: 1, Title: "旧标题", Body: "旧正文"})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	primed, err := service.Detail(ctx, post.ID)
	if err != nil || primed.Title != "旧标题" {
		t.Fatalf("prime Detail() = %#v, %v", primed, err)
	}

	hook.fail.Store(true)
	updated, err := service.Update(ctx, content.UpdateInput{AuthorID: 1, PostID: post.ID, Title: "新标题", Body: "新正文"})
	if err != nil || updated.Title != "新标题" {
		t.Fatalf("Update() = %#v, %v", updated, err)
	}
	stale, err := service.Detail(ctx, post.ID)
	if err != nil || stale.Title != "旧标题" {
		t.Fatalf("Detail() before fallback = %#v, %v; want stale cache proof", stale, err)
	}

	var eventType, payloadJSON string
	if err := database.QueryRow(`
SELECT event_type,payload_json FROM outbox_events
WHERE event_type=? ORDER BY id DESC LIMIT 1`, cacheinvalidation.EventType).Scan(&eventType, &payloadJSON); err != nil {
		t.Fatalf("load cache invalidation event: %v", err)
	}
	hook.fail.Store(false)
	processor, err := cacheinvalidation.NewProcessor(reader)
	if err != nil {
		t.Fatalf("NewProcessor() error = %v", err)
	}
	if err := processor.Process(ctx, eventType, []byte(payloadJSON)); err != nil {
		t.Fatalf("Process(cache invalidation) error = %v", err)
	}
	fresh, err := service.Detail(ctx, post.ID)
	if err != nil || fresh.Title != "新标题" || fresh.Body != "新正文" {
		t.Fatalf("Detail() after fallback = %#v, %v", fresh, err)
	}
}

func TestPublicContentFallsBackWhileRedisIsDownAndRefillsAfterRecovery(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容 Redis 故障恢复测试")
	}
	database, cleanup := openContentTestDatabase(t, adminDSN)
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
	result, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at) VALUES (1,'故障内容','数据库正文','published',UTC_TIMESTAMP(6))")
	if err != nil {
		t.Fatalf("seed public content: %v", err)
	}
	postID, _ := result.LastInsertId()
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	address := mini.Addr()
	rawRedis := goredis.NewClient(&goredis.Options{Addr: address, MaxRetries: 0})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	repository, _ := content.NewRepository(gormDB)
	reader, _ := content.NewPublicEntityReader(repository, cache)
	service, _ := content.NewService(repository, config.ContentConfig{
		MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000,
	}, reader)

	mini.Close()
	post, err := service.Detail(ctx, uint64(postID))
	if err != nil || post.Title != "故障内容" {
		t.Fatalf("Detail() while Redis is down = %#v, %v", post, err)
	}
	recovered := miniredis.NewMiniRedis()
	if err := recovered.StartAddr(address); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
	defer recovered.Close()
	post, err = service.Detail(ctx, uint64(postID))
	if err != nil || post.Body != "数据库正文" {
		t.Fatalf("Detail() after Redis recovery = %#v, %v", post, err)
	}
	if !recovered.Exists(cache.Key("content:entity:%d", postID)) {
		t.Fatal("recovered Redis did not receive content backfill")
	}
}

type failDeleteHook struct {
	fail atomic.Bool
}

func (h *failDeleteHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return next
}

func (h *failDeleteHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, command goredis.Cmder) error {
		if h.fail.Load() && strings.EqualFold(command.Name(), "del") {
			return errors.New("forced cache delete failure")
		}
		return next(ctx, command)
	}
}

func (h *failDeleteHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}
