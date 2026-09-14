package feed_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/feed"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestContentFollowingCurrentRelationsCursorCacheAndInvalidation(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行关注 Post Feed 集成测试")
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

	base := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	authorOneOlder := seedFollowingPost(t, database, 1, "作者一-旧", "published", base)
	authorTwoOlder := seedFollowingPost(t, database, 2, "作者二-旧", "published", base.Add(5*time.Second))
	authorOneSameLow := seedFollowingPost(t, database, 1, "作者一-同刻低 ID", "published", base.Add(10*time.Second))
	authorOneSameHigh := seedFollowingPost(t, database, 1, "作者一-同刻高 ID", "published", base.Add(10*time.Second))
	authorTwoNewest := seedFollowingPost(t, database, 2, "作者二-新", "published", base.Add(20*time.Second))
	_ = seedFollowingPost(t, database, 3, "未关注作者", "published", base.Add(30*time.Second))
	_ = seedFollowingPost(t, database, 1, "已隐藏内容", "hidden", base.Add(40*time.Second))

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
	service, err := feed.NewContentFollowingService(repository, cache)
	if err != nil {
		t.Fatalf("NewContentFollowingService() error = %v", err)
	}

	const viewerID = uint64(4)
	firstPage, err := service.ListFollowing(ctx, viewerID, 2, nil)
	if err != nil {
		t.Fatalf("ListFollowing(first page) error = %v", err)
	}
	if got, want := postIDs(firstPage.Posts), []uint64{authorOneSameHigh, authorOneSameLow}; !reflect.DeepEqual(got, want) || !firstPage.HasMore || firstPage.NextCursor == nil {
		t.Fatalf("first following page = %v hasMore=%v cursor=%#v, want %v", got, firstPage.HasMore, firstPage.NextCursor, want)
	}
	secondPage, err := service.ListFollowing(ctx, viewerID, 2, firstPage.NextCursor)
	if err != nil {
		t.Fatalf("ListFollowing(second page) error = %v", err)
	}
	if got, want := postIDs(secondPage.Posts), []uint64{authorOneOlder}; !reflect.DeepEqual(got, want) || secondPage.HasMore {
		t.Fatalf("second following page = %v hasMore=%v, want %v", got, secondPage.HasMore, want)
	}
	if got := countFollowingCacheKeys(mini, cache, viewerID); got != 2 {
		t.Fatalf("following cache page count = %d, want 2", got)
	}

	socialRepository := social.NewSocialRepository(gormDB)
	socialService := social.NewSocialService(socialRepository, account.NewAccountRepository(gormDB), nil, cache)
	if err := socialService.Follow(ctx, &social.Social{FollowerID: uint(viewerID), VloggerID: 2}); err != nil {
		t.Fatalf("Follow(author 2) error = %v", err)
	}
	if got := countFollowingCacheKeys(mini, cache, viewerID); got != 0 {
		t.Fatalf("cache keys after follow = %d, want 0", got)
	}
	afterFollow, err := service.ListFollowing(ctx, viewerID, 10, nil)
	if err != nil {
		t.Fatalf("ListFollowing(after follow) error = %v", err)
	}
	if got, want := postIDs(afterFollow.Posts), []uint64{authorTwoNewest, authorOneSameHigh, authorOneSameLow, authorTwoOlder, authorOneOlder}; !reflect.DeepEqual(got, want) {
		t.Fatalf("following ids after follow = %v, want %v", got, want)
	}

	if err := socialService.Unfollow(ctx, &social.Social{FollowerID: uint(viewerID), VloggerID: 1}); err != nil {
		t.Fatalf("Unfollow(author 1) error = %v", err)
	}
	if got := countFollowingCacheKeys(mini, cache, viewerID); got != 0 {
		t.Fatalf("cache keys after unfollow = %d, want 0", got)
	}
	afterUnfollow, err := service.ListFollowing(ctx, viewerID, 10, nil)
	if err != nil {
		t.Fatalf("ListFollowing(after unfollow) error = %v", err)
	}
	if got, want := postIDs(afterUnfollow.Posts), []uint64{authorTwoNewest, authorTwoOlder}; !reflect.DeepEqual(got, want) {
		t.Fatalf("following ids after unfollow = %v, want %v", got, want)
	}
}

func createFollowingFixtures(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`
CREATE TABLE accounts (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL DEFAULT '',
    token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    avatar_url VARCHAR(512) NOT NULL DEFAULT '',
    bio VARCHAR(255) NOT NULL DEFAULT ''
) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create accounts fixture: %v", err)
	}
	if _, err := database.Exec(`
CREATE TABLE socials (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    follower_id BIGINT UNSIGNED NOT NULL,
    vlogger_id BIGINT UNSIGNED NOT NULL,
    UNIQUE KEY uq_social_fixture (follower_id, vlogger_id)
) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create socials fixture: %v", err)
	}
	for id, username := range map[uint64]string{1: "author-one", 2: "author-two", 3: "outsider", 4: "viewer"} {
		if _, err := database.Exec("INSERT INTO accounts (id, username, token, refresh_token) VALUES (?, ?, '', '')", id, username); err != nil {
			t.Fatalf("seed account %d: %v", id, err)
		}
	}
	if _, err := database.Exec("INSERT INTO socials (follower_id, vlogger_id) VALUES (4, 1)"); err != nil {
		t.Fatalf("seed initial follow relation: %v", err)
	}
}

func seedFollowingPost(t *testing.T, database *sql.DB, authorID uint64, title, status string, publishedAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO posts (author_id, title, body, status, published_at)
VALUES (?, ?, ?, ?, ?)`, authorID, title, fmt.Sprintf("%s 正文", title), status, publishedAt)
	if err != nil {
		t.Fatalf("seed following post %q: %v", title, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("following post LastInsertId: %v", err)
	}
	return uint64(id)
}

func countFollowingCacheKeys(mini *miniredis.Miniredis, cache *rediscache.Client, viewerID uint64) int {
	prefix := strings.TrimSuffix(feed.ContentFollowingCachePattern(cache, viewerID), "*")
	count := 0
	for _, key := range mini.Keys() {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}
