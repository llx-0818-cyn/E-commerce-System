package feed_test

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/feed"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestContentPopularityConfiguredDecayPaidCapAndNoPaidOnlyMember(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行可配置热度快照集成测试")
	}
	database, cleanup := openFeedTestDatabase(t, adminDSN)
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
	snapshotAt := time.Date(2026, time.August, 24, 15, 0, 0, 0, time.UTC)
	current := seedPopularityPost(t, database, "当前分钟", 0, snapshotAt)
	previous := seedPopularityPost(t, database, "上一分钟", 0, snapshotAt.Add(-time.Minute))
	paidOnly := seedPopularityPost(t, database, "纯成交", 0, snapshotAt.Add(-2*time.Minute))
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	rawRedis := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	recorder, _ := feed.NewContentPopularityRecorder(cache)
	if err := recorder.RecordSignal(ctx, current, 10, snapshotAt); err != nil {
		t.Fatalf("record current organic score: %v", err)
	}
	if err := recorder.RecordSignal(ctx, previous, 10, snapshotAt.Add(-time.Minute)); err != nil {
		t.Fatalf("record previous organic score: %v", err)
	}
	if err := recorder.RecordPaidSignal(ctx, current, 100, snapshotAt); err != nil {
		t.Fatalf("record capped paid score: %v", err)
	}
	if err := recorder.RecordPaidSignal(ctx, paidOnly, 100, snapshotAt); err != nil {
		t.Fatalf("record paid-only score: %v", err)
	}
	service, err := feed.NewContentPopularityService(feed.NewFeedRepository(gormDB), cache, config.PopularityConfig{
		PaidOrderContributionCap: 5,
		DecayHalfLife:            config.Duration(time.Minute),
	})
	if err != nil {
		t.Fatalf("NewContentPopularityService(weighted) error = %v", err)
	}
	page, err := service.ListPopular(ctx, 10, snapshotAt, 0, nil)
	if err != nil {
		t.Fatalf("ListPopular(weighted) error = %v", err)
	}
	if got, want := postIDs(page.Posts), []uint64{current, previous}; !reflect.DeepEqual(got, want) || page.Degraded {
		t.Fatalf("weighted page ids = %v degraded=%v, want %v", got, page.Degraded, want)
	}
	snapshotKey := feed.ContentPopularitySnapshotKey(cache, snapshotAt)
	currentScore, err := rawRedis.ZScore(ctx, snapshotKey, strconv.FormatUint(current, 10)).Result()
	if err != nil || currentScore != 15 {
		t.Fatalf("current snapshot score = %v, %v; want 15", currentScore, err)
	}
	previousScore, err := rawRedis.ZScore(ctx, snapshotKey, strconv.FormatUint(previous, 10)).Result()
	if err != nil || previousScore != 5 {
		t.Fatalf("previous snapshot score = %v, %v; want 5", previousScore, err)
	}
	if _, err := rawRedis.ZScore(ctx, snapshotKey, strconv.FormatUint(paidOnly, 10)).Result(); err != redis.Nil {
		t.Fatalf("paid-only member snapshot error = %v, want redis.Nil", err)
	}
}
