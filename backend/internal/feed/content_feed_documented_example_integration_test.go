package feed_test

import (
	"context"
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

func TestDocumentedContentFeedFixedSeedWalkthrough(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行文档固定种子 Feed 演算测试")
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
	createFollowingFixtures(t, database)
	if _, err := database.Exec("INSERT INTO socials (follower_id, vlogger_id) VALUES (4, 2)"); err != nil {
		t.Fatalf("seed second documented follow: %v", err)
	}

	base := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	seeds := []struct {
		id         uint64
		authorID   uint64
		title      string
		minute     int
		popularity float64
		tag        string
		score      float64
	}{
		{id: 106, authorID: 1, title: "A", minute: 6, popularity: 60, tag: "go", score: 90},
		{id: 105, authorID: 2, title: "B", minute: 5, popularity: 50, tag: "go", score: 60},
		{id: 104, authorID: 3, title: "C", minute: 5, popularity: 40, tag: "go", score: 80},
		{id: 103, authorID: 1, title: "D", minute: 4, popularity: 30, tag: "redis", score: 50},
		{id: 102, authorID: 3, title: "E", minute: 3, popularity: 20, tag: "go", score: 40},
		{id: 101, authorID: 2, title: "F", minute: 2, popularity: 10, tag: "go", score: 70},
	}
	if _, err := database.Exec("INSERT INTO tags (name) VALUES ('go'), ('redis')"); err != nil {
		t.Fatalf("seed documented tags: %v", err)
	}
	for _, seed := range seeds {
		publishedAt := base.Add(time.Duration(seed.minute) * time.Minute)
		if _, err := database.Exec(`
INSERT INTO posts (id,author_id,title,body,status,popularity,published_at)
VALUES (?, ?, ?, '固定种子正文', 'published', ?, ?)`, seed.id, seed.authorID, seed.title, seed.popularity, publishedAt); err != nil {
			t.Fatalf("seed documented post %s: %v", seed.title, err)
		}
		if _, err := database.Exec("INSERT INTO post_tags (post_id, tag_id) SELECT ?, id FROM tags WHERE name = ?", seed.id, seed.tag); err != nil {
			t.Fatalf("attach documented tag %s: %v", seed.title, err)
		}
	}

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository := feed.NewFeedRepository(gormDB)
	latest, _ := feed.NewContentLatestService(repository, nil)
	following, _ := feed.NewContentFollowingService(repository, nil)
	tags, _ := feed.NewContentTagService(repository)
	popularityDB, _ := feed.NewContentPopularityService(repository, nil)

	assertDocumentedPages(t, "latest", collectDocumentedPostPages(t, func(cursor *feed.PostCursor) (feed.LatestPostPage, error) {
		return latest.ListLatest(ctx, 2, cursor)
	}), [][]uint64{{106, 105}, {104, 103}, {102, 101}})
	assertDocumentedPages(t, "following", collectDocumentedPostPages(t, func(cursor *feed.PostCursor) (feed.LatestPostPage, error) {
		return following.ListFollowing(ctx, 4, 2, cursor)
	}), [][]uint64{{106, 105}, {103, 101}})
	assertDocumentedPages(t, "go tag", collectDocumentedPostPages(t, func(cursor *feed.PostCursor) (feed.LatestPostPage, error) {
		return tags.ListByTag(ctx, "GO", 2, cursor)
	}), [][]uint64{{106, 105}, {104, 102}, {101}})
	assertDocumentedPages(t, "MySQL popularity", collectDocumentedPopularityPages(t, ctx, popularityDB), [][]uint64{{106, 105}, {104, 103}, {102, 101}})

	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	rawRedis := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	recorder, _ := feed.NewContentPopularityRecorder(cache)
	snapshotAt := base.Add(10 * time.Minute)
	for _, seed := range seeds {
		if err := recorder.RecordSignal(ctx, seed.id, seed.score, snapshotAt); err != nil {
			t.Fatalf("record documented score %s: %v", seed.title, err)
		}
	}
	popularityRedis, _ := feed.NewContentPopularityService(repository, cache)
	first, err := popularityRedis.ListPopular(ctx, 2, snapshotAt, 0, nil)
	if err != nil {
		t.Fatalf("documented snapshot first page: %v", err)
	}
	if got, want := postIDs(first.Posts), []uint64{106, 104}; !reflect.DeepEqual(got, want) {
		t.Fatalf("documented snapshot first page = %v, want %v", got, want)
	}
	if err := recorder.RecordSignal(ctx, 105, 1000, snapshotAt); err != nil {
		t.Fatalf("mutate documented live leaderboard: %v", err)
	}
	second, err := popularityRedis.ListPopular(ctx, 2, *first.SnapshotAt, first.NextOffset, first.NextFallbackCursor)
	if err != nil {
		t.Fatalf("documented snapshot second page: %v", err)
	}
	if got, want := postIDs(second.Posts), []uint64{101, 105}; !reflect.DeepEqual(got, want) {
		t.Fatalf("documented snapshot second page after live change = %v, want %v", got, want)
	}
}

type documentedPostPageFunc func(*feed.PostCursor) (feed.LatestPostPage, error)

func collectDocumentedPostPages(t *testing.T, list documentedPostPageFunc) [][]uint64 {
	t.Helper()
	pages := make([][]uint64, 0)
	var cursor *feed.PostCursor
	for {
		page, err := list(cursor)
		if err != nil {
			t.Fatalf("collect documented Post page: %v", err)
		}
		pages = append(pages, postIDs(page.Posts))
		if !page.HasMore {
			return pages
		}
		if page.NextCursor == nil {
			t.Fatal("documented Post page has_more without next cursor")
		}
		cursor = page.NextCursor
	}
}

func collectDocumentedPopularityPages(t *testing.T, ctx context.Context, service *feed.ContentPopularityService) [][]uint64 {
	t.Helper()
	pages := make([][]uint64, 0)
	var cursor *feed.PopularityCursor
	for {
		page, err := service.ListPopular(ctx, 2, time.Time{}, 0, cursor)
		if err != nil {
			t.Fatalf("collect documented popularity page: %v", err)
		}
		pages = append(pages, postIDs(page.Posts))
		if !page.HasMore {
			return pages
		}
		if page.NextFallbackCursor == nil {
			t.Fatal("documented popularity page has_more without fallback cursor")
		}
		cursor = page.NextFallbackCursor
	}
}

func assertDocumentedPages(t *testing.T, name string, got, want [][]uint64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("documented %s pages = %v, want %v", name, got, want)
	}
	seen := make(map[uint64]struct{})
	for _, page := range got {
		for _, id := range page {
			if _, duplicate := seen[id]; duplicate {
				t.Fatalf("documented %s duplicate id %d", name, id)
			}
			seen[id] = struct{}{}
		}
	}
}
