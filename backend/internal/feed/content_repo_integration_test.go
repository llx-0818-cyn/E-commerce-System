package feed_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPublishedPostCompositeCursorSameTimestampHasNoDuplicateOrGap(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Post Feed Repository 集成测试")
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
	sameTime := time.Date(2026, time.August, 24, 8, 0, 0, 123000000, time.UTC)
	ids := make([]uint64, 0, 7)
	for index := 0; index < 7; index++ {
		result, err := database.Exec(`
INSERT INTO posts (author_id, title, body, status, published_at)
VALUES (1, ?, '相同时间戳分页正文', 'published', ?)`, fmt.Sprintf("同时间内容-%d", index), sameTime)
		if err != nil {
			t.Fatalf("seed same-time post %d: %v", index, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("post LastInsertId: %v", err)
		}
		ids = append(ids, uint64(id))
	}
	if _, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at) VALUES (1,'隐藏','正文','hidden',NULL)"); err != nil {
		t.Fatalf("seed hidden post: %v", err)
	}
	deletedAt := time.Now().UTC()
	if _, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at,deleted_at) VALUES (1,'删除','正文','deleted',NULL,?)", deletedAt); err != nil {
		t.Fatalf("seed deleted post: %v", err)
	}

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository := feed.NewFeedRepository(gormDB)
	var got []uint64
	var cursor *feed.PostCursor
	for {
		page, err := repository.ListPublishedPosts(ctx, 3, cursor)
		if err != nil {
			t.Fatalf("ListPublishedPosts() error = %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, post := range page {
			got = append(got, post.ID)
		}
		last := page[len(page)-1]
		cursor = &feed.PostCursor{PublishedAt: *last.PublishedAt, ID: last.ID}
	}
	want := make([]uint64, len(ids))
	for index := range ids {
		want[index] = ids[len(ids)-1-index]
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged post ids = %v, want %v", got, want)
	}
	seen := make(map[uint64]struct{}, len(got))
	for _, id := range got {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate post id across pages: %d", id)
		}
		seen[id] = struct{}{}
	}
}

func openFeedTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_post_feed_test_%d", time.Now().UnixNano())
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
