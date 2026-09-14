package content_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostTagExtractionDedupAndTransactionRollback(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Post Tag 集成测试")
	}
	database, cleanup := openContentTestDatabase(t, adminDSN)
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
	repository, err := content.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	service, err := content.NewService(repository, config.ContentConfig{MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	post, err := service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "#Go 内容电商 #电商",
		Body:     "正文重复 #go，并新增 #Redis 与 #电商。",
		Tags:     []string{"Go", "#缓存", "redis"},
	})
	if err != nil {
		t.Fatalf("Publish(tags) error = %v", err)
	}
	gotNames := make([]string, len(post.Tags))
	for index := range post.Tags {
		gotNames[index] = post.Tags[index].Name
	}
	sort.Strings(gotNames)
	wantNames := []string{"go", "redis", "电商", "缓存"}
	sort.Strings(wantNames)
	if len(gotNames) != len(wantNames) {
		t.Fatalf("tag names = %v, want %v", gotNames, wantNames)
	}
	for index := range wantNames {
		if gotNames[index] != wantNames[index] {
			t.Fatalf("tag names = %v, want %v", gotNames, wantNames)
		}
	}
	if got := countRows(t, database, "tags"); got != 4 {
		t.Fatalf("tags count = %d, want 4", got)
	}
	if got := countRows(t, database, "post_tags"); got != 4 {
		t.Fatalf("post_tags count = %d, want 4", got)
	}

	imageID := seedUnattachedImage(t, database, 1, "uploads/owner-1/rollback-tag.jpg")
	postCountBefore := countRows(t, database, "posts")
	tagCountBefore := countRows(t, database, "tags")
	if _, err := database.Exec(`
CREATE TRIGGER force_post_tag_failure
BEFORE INSERT ON post_tags
FOR EACH ROW
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced post_tags failure'`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	_, err = service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "事务回滚 #rollbacktag",
		Body:     "关联标签时数据库故意失败。",
		ImageIDs: []uint64{imageID},
	})
	if err == nil {
		t.Fatal("Publish() error = nil, want forced post_tags failure")
	}
	if got := countRows(t, database, "posts"); got != postCountBefore {
		t.Fatalf("posts after tag rollback = %d, want %d", got, postCountBefore)
	}
	if got := countRows(t, database, "tags"); got != tagCountBefore {
		t.Fatalf("tags after rollback = %d, want %d", got, tagCountBefore)
	}
	var postID sql.NullInt64
	if err := database.QueryRow("SELECT post_id FROM post_images WHERE id = ?", imageID).Scan(&postID); err != nil {
		t.Fatalf("query rollback image: %v", err)
	}
	if postID.Valid {
		t.Fatalf("image remained attached after tag rollback: post_id=%d", postID.Int64)
	}
	var rollbackTags int
	if err := database.QueryRow("SELECT COUNT(*) FROM tags WHERE name = 'rollbacktag'").Scan(&rollbackTags); err != nil {
		t.Fatalf("query rollback tag: %v", err)
	}
	if rollbackTags != 0 {
		t.Fatalf("rollback tag count = %d, want 0", rollbackTags)
	}

	_, err = service.Publish(ctx, content.PublishInput{AuthorID: 1, Title: "非法标签", Body: "正文", Tags: []string{"bad tag"}})
	if !errors.Is(err, content.ErrTagValidation) {
		t.Fatalf("invalid explicit tag error = %v", err)
	}
}
