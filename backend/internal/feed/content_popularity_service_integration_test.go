package feed_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"

	"feedsystem_video_go/internal/feed"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestContentPopularitySnapshotStableWhileLeaderboardChangesAndDBCursorFallback(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行热门 Post Feed 集成测试")
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
	persistent := []struct {
		title      string
		popularity float64
		published  time.Time
	}{
		{title: "持久榜一", popularity: 50, published: base.Add(5 * time.Second)},
		{title: "持久榜二", popularity: 40, published: base.Add(4 * time.Second)},
		{title: "持久榜三", popularity: 30, published: base.Add(3 * time.Second)},
		{title: "持久榜四", popularity: 20, published: base.Add(2 * time.Second)},
		{title: "持久榜五", popularity: 10, published: base.Add(1 * time.Second)},
	}
	postIDsByRank := make([]uint64, 0, len(persistent))
	for _, item := range persistent {
		postIDsByRank = append(postIDsByRank, seedPopularityPost(t, database, item.title, item.popularity, item.published))
	}

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
	recorder, err := feed.NewContentPopularityRecorder(cache)
	if err != nil {
		t.Fatalf("NewContentPopularityRecorder() error = %v", err)
	}
	snapshotAt := base.Truncate(time.Minute)
	for index, postID := range postIDsByRank[:4] {
		if err := recorder.RecordSignal(ctx, postID, float64(40-index*10), snapshotAt); err != nil {
			t.Fatalf("RecordSignal(initial rank %d) error = %v", index, err)
		}
	}
	service, err := feed.NewContentPopularityService(repository, cache)
	if err != nil {
		t.Fatalf("NewContentPopularityService() error = %v", err)
	}

	firstPage, err := service.ListPopular(ctx, 2, snapshotAt, 0, nil)
	if err != nil {
		t.Fatalf("ListPopular(first snapshot page) error = %v", err)
	}
	if got, want := postIDs(firstPage.Posts), postIDsByRank[:2]; !reflect.DeepEqual(got, want) || !firstPage.HasMore || firstPage.NextOffset != 2 || firstPage.SnapshotAt == nil || firstPage.Degraded {
		t.Fatalf("first popular page = %v hasMore=%v offset=%d snapshot=%v degraded=%v, want %v", got, firstPage.HasMore, firstPage.NextOffset, firstPage.SnapshotAt, firstPage.Degraded, want)
	}

	// 第一页后实时榜发生剧烈变化；旧 snapshot ZSET 必须保持不变。
	if err := recorder.RecordSignal(ctx, postIDsByRank[4], 1000, snapshotAt); err != nil {
		t.Fatalf("RecordSignal(new leader) error = %v", err)
	}
	if err := recorder.RecordSignal(ctx, postIDsByRank[2], 1000, snapshotAt); err != nil {
		t.Fatalf("RecordSignal(changed old rank) error = %v", err)
	}
	secondPage, err := service.ListPopular(ctx, 2, *firstPage.SnapshotAt, firstPage.NextOffset, firstPage.NextFallbackCursor)
	if err != nil {
		t.Fatalf("ListPopular(second snapshot page) error = %v", err)
	}
	if got, want := postIDs(secondPage.Posts), postIDsByRank[2:4]; !reflect.DeepEqual(got, want) || secondPage.HasMore || secondPage.NextOffset != 4 || secondPage.Degraded {
		t.Fatalf("second popular page after leaderboard change = %v hasMore=%v offset=%d degraded=%v, want %v", got, secondPage.HasMore, secondPage.NextOffset, secondPage.Degraded, want)
	}

	mini.Close()
	fallbackFirst, err := service.ListPopular(ctx, 2, time.Time{}, 0, nil)
	if err != nil {
		t.Fatalf("ListPopular(Redis down first page) error = %v", err)
	}
	if got, want := postIDs(fallbackFirst.Posts), postIDsByRank[:2]; !reflect.DeepEqual(got, want) || !fallbackFirst.Degraded || !fallbackFirst.HasMore || fallbackFirst.NextFallbackCursor == nil {
		t.Fatalf("DB fallback first page = %v degraded=%v hasMore=%v cursor=%#v, want %v", got, fallbackFirst.Degraded, fallbackFirst.HasMore, fallbackFirst.NextFallbackCursor, want)
	}
	fallbackSecond, err := service.ListPopular(ctx, 2, time.Time{}, 0, fallbackFirst.NextFallbackCursor)
	if err != nil {
		t.Fatalf("ListPopular(Redis down second page) error = %v", err)
	}
	if got, want := postIDs(fallbackSecond.Posts), postIDsByRank[2:4]; !reflect.DeepEqual(got, want) || !fallbackSecond.Degraded || !fallbackSecond.HasMore {
		t.Fatalf("DB fallback second page = %v degraded=%v hasMore=%v, want %v", got, fallbackSecond.Degraded, fallbackSecond.HasMore, want)
	}
	fallbackThird, err := service.ListPopular(ctx, 2, time.Time{}, 0, fallbackSecond.NextFallbackCursor)
	if err != nil {
		t.Fatalf("ListPopular(Redis down third page) error = %v", err)
	}
	if got, want := postIDs(fallbackThird.Posts), postIDsByRank[4:]; !reflect.DeepEqual(got, want) || fallbackThird.HasMore {
		t.Fatalf("DB fallback third page = %v hasMore=%v, want %v", got, fallbackThird.HasMore, want)
	}
}

func seedPopularityPost(t *testing.T, database *sql.DB, title string, popularity float64, publishedAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO posts (author_id, title, body, status, popularity, published_at)
VALUES (1, ?, '热门流正文', 'published', ?, ?)`, title, popularity, publishedAt)
	if err != nil {
		t.Fatalf("seed popularity post %q: %v", title, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("popularity post LastInsertId: %v", err)
	}
	return uint64(id)
}
