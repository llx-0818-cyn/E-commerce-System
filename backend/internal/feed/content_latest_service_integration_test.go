package feed_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/feed"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestContentLatestEmptyRebuildHotColdBoundaryAndRedisFailure(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行最新 Post Feed 集成测试")
	}
	database, cleanup := openFeedTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	migrationItems, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(migrationItems) {
		t.Fatalf("migration Up() = %d, %v; want %d, nil", count, err, len(migrationItems))
	}
	base := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	ids := make([]uint64, 0, 8)
	for index := 0; index < 8; index++ {
		publishedAt := base.Add(time.Duration(index) * time.Second)
		result, err := database.Exec(`
INSERT INTO posts (author_id,title,body,status,published_at)
VALUES (1, ?, '冷热拼接正文', 'published', ?)`, fmt.Sprintf("内容-%d", index), publishedAt)
		if err != nil {
			t.Fatalf("seed post %d: %v", index, err)
		}
		id, _ := result.LastInsertId()
		ids = append(ids, uint64(id))
	}
	wantAll := reverseIDs(ids)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository := feed.NewFeedRepository(gormDB)
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	rawRedis := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	service, err := feed.NewContentLatestService(repository, cache)
	if err != nil {
		t.Fatalf("NewContentLatestService() error = %v", err)
	}

	rebuilt, err := service.ListLatest(ctx, 3, nil)
	if err != nil {
		t.Fatalf("ListLatest(empty cache) error = %v", err)
	}
	if got := postIDs(rebuilt.Posts); !reflect.DeepEqual(got, wantAll[:3]) {
		t.Fatalf("rebuilt page ids = %v, want %v", got, wantAll[:3])
	}
	if card, err := rawRedis.ZCard(ctx, feed.ContentTimelineKey(cache)).Result(); err != nil || card != 8 {
		t.Fatalf("rebuilt timeline cardinality = %d, %v; want 8", card, err)
	}

	if err := rawRedis.Del(ctx, feed.ContentTimelineKey(cache)).Err(); err != nil {
		t.Fatalf("clear content timeline: %v", err)
	}
	for index := len(ids) - 3; index < len(ids); index++ {
		if err := rawRedis.ZAdd(ctx, feed.ContentTimelineKey(cache), redis.Z{
			Score: float64(base.Add(time.Duration(index) * time.Second).UnixMilli()), Member: strconv.FormatUint(ids[index], 10),
		}).Err(); err != nil {
			t.Fatalf("seed hot timeline: %v", err)
		}
	}
	boundaryPage, err := service.ListLatest(ctx, 5, nil)
	if err != nil {
		t.Fatalf("ListLatest(hot/cold) error = %v", err)
	}
	if got := postIDs(boundaryPage.Posts); !reflect.DeepEqual(got, wantAll[:5]) || !boundaryPage.HasMore || boundaryPage.NextCursor == nil {
		t.Fatalf("boundary page = %v hasMore=%v cursor=%#v, want %v", got, boundaryPage.HasMore, boundaryPage.NextCursor, wantAll[:5])
	}
	nextPage, err := service.ListLatest(ctx, 5, boundaryPage.NextCursor)
	if err != nil {
		t.Fatalf("ListLatest(next boundary) error = %v", err)
	}
	if got := postIDs(nextPage.Posts); !reflect.DeepEqual(got, wantAll[5:]) || nextPage.HasMore {
		t.Fatalf("next boundary page = %v hasMore=%v, want %v", got, nextPage.HasMore, wantAll[5:])
	}

	mini.Close()
	fallbackPage, err := service.ListLatest(ctx, 4, nil)
	if err != nil {
		t.Fatalf("ListLatest(Redis down) error = %v", err)
	}
	if got := postIDs(fallbackPage.Posts); !reflect.DeepEqual(got, wantAll[:4]) {
		t.Fatalf("Redis fallback page = %v, want %v", got, wantAll[:4])
	}
}

func reverseIDs(ids []uint64) []uint64 {
	result := make([]uint64, len(ids))
	for index := range ids {
		result[index] = ids[len(ids)-1-index]
	}
	return result
}

func postIDs(posts []content.Post) []uint64 {
	ids := make([]uint64, len(posts))
	for index := range posts {
		ids[index] = posts[index].ID
	}
	return ids
}
