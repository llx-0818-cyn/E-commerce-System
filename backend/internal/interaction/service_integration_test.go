package interaction_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestLikeFavoriteTargetStateIdempotencyAndFavoriteList(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行互动 Service 集成测试")
	}
	database, cleanup := openInteractionTestDatabase(t, adminDSN)
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, err := interaction.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	service, err := interaction.NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	post1 := seedInteractionPost(t, database, 1, "互动一", time.Now().UTC().Add(-3*time.Hour))
	post2 := seedInteractionPost(t, database, 1, "互动二", time.Now().UTC().Add(-2*time.Hour))
	post3 := seedInteractionPost(t, database, 2, "互动三", time.Now().UTC().Add(-time.Hour))

	for attempt := 0; attempt < 2; attempt++ {
		state, err := service.SetLike(ctx, 7, post1, true)
		if err != nil || !state.Active || state.Count != 1 {
			t.Fatalf("SetLike(true) attempt %d = %#v, %v", attempt, state, err)
		}
	}
	assertInteractionCount(t, database, "post_likes", post1, 1)
	assertPostCounter(t, database, post1, "likes_count", 1)
	assertInteractionOutboxCount(t, database, "content.liked", 1)
	for attempt := 0; attempt < 2; attempt++ {
		state, err := service.SetLike(ctx, 7, post1, false)
		if err != nil || state.Active || state.Count != 0 {
			t.Fatalf("SetLike(false) attempt %d = %#v, %v", attempt, state, err)
		}
	}
	assertInteractionCount(t, database, "post_likes", post1, 0)
	assertPostCounter(t, database, post1, "likes_count", 0)
	assertInteractionOutboxCount(t, database, "content.liked", 1)

	for attempt := 0; attempt < 2; attempt++ {
		state, err := service.SetFavorite(ctx, 7, post1, true)
		if err != nil || !state.Active || state.Count != 1 {
			t.Fatalf("SetFavorite(true) attempt %d = %#v, %v", attempt, state, err)
		}
	}
	assertInteractionCount(t, database, "post_favorites", post1, 1)
	assertPostCounter(t, database, post1, "favorites_count", 1)
	assertInteractionOutboxCount(t, database, "content.favorited", 1)
	for attempt := 0; attempt < 2; attempt++ {
		state, err := service.SetFavorite(ctx, 7, post1, false)
		if err != nil || state.Active || state.Count != 0 {
			t.Fatalf("SetFavorite(false) attempt %d = %#v, %v", attempt, state, err)
		}
	}
	assertInteractionCount(t, database, "post_favorites", post1, 0)
	assertPostCounter(t, database, post1, "favorites_count", 0)
	assertInteractionOutboxCount(t, database, "content.favorited", 1)

	for _, postID := range []uint64{post1, post2, post3} {
		if _, err := service.SetFavorite(ctx, 7, postID, true); err != nil {
			t.Fatalf("favorite post %d: %v", postID, err)
		}
	}
	base := time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC)
	for index, postID := range []uint64{post1, post2, post3} {
		if _, err := database.Exec("UPDATE post_favorites SET created_at = ? WHERE account_id = 7 AND post_id = ?", base.Add(time.Duration(index)*time.Second), postID); err != nil {
			t.Fatalf("set favorite cursor time: %v", err)
		}
	}
	if _, err := database.Exec("UPDATE posts SET status = 'deleted', deleted_at = CURRENT_TIMESTAMP(6) WHERE id = ?", post2); err != nil {
		t.Fatalf("soft delete favorite post: %v", err)
	}

	firstPage, err := service.ListFavorites(ctx, interaction.FavoriteListQuery{AccountID: 7, Limit: 1})
	if err != nil || len(firstPage.Posts) != 1 || firstPage.Posts[0].ID != post3 || !firstPage.HasMore || firstPage.NextBeforeTime == nil {
		t.Fatalf("first favorite page = %#v, %v", firstPage, err)
	}
	secondPage, err := service.ListFavorites(ctx, interaction.FavoriteListQuery{
		AccountID:  7,
		BeforeTime: *firstPage.NextBeforeTime,
		BeforeID:   firstPage.NextBeforeID,
		Limit:      1,
	})
	if err != nil || len(secondPage.Posts) != 1 || secondPage.Posts[0].ID != post1 || secondPage.HasMore {
		t.Fatalf("second favorite page = %#v, %v", secondPage, err)
	}
	if _, err := service.SetLike(ctx, 7, post2, true); !errors.Is(err, content.ErrPostNotFound) {
		t.Fatalf("SetLike(deleted post) error = %v", err)
	}
}

func seedInteractionPost(t *testing.T, database *sql.DB, authorID uint64, title string, publishedAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO posts (author_id, title, body, status, published_at)
VALUES (?, ?, '互动测试正文', 'published', ?)`, authorID, title, publishedAt)
	if err != nil {
		t.Fatalf("seed post %q: %v", title, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("post LastInsertId: %v", err)
	}
	return uint64(id)
}

func assertInteractionCount(t *testing.T, database *sql.DB, table string, postID uint64, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE post_id = ?", postID).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}

func assertPostCounter(t *testing.T, database *sql.DB, postID uint64, column string, want uint64) {
	t.Helper()
	var count uint64
	if err := database.QueryRow("SELECT "+column+" FROM posts WHERE id = ?", postID).Scan(&count); err != nil {
		t.Fatalf("query %s: %v", column, err)
	}
	if count != want {
		t.Fatalf("%s = %d, want %d", column, count, want)
	}
}

func assertInteractionOutboxCount(t *testing.T, database *sql.DB, eventType string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE event_type = ?", eventType).Scan(&count); err != nil {
		t.Fatalf("count outbox %s: %v", eventType, err)
	}
	if count != want {
		t.Fatalf("outbox %s count = %d, want %d", eventType, count, want)
	}
}

func openInteractionTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_interaction_service_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create temporary database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open temporary database: %v", err)
	}
	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop temporary database: %v", err)
		}
		adminDB.Close()
	}
	return database, cleanup
}
