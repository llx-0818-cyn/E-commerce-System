package content_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostServiceTextMultiImageOwnershipAndDeleteVisibility(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Post Service 集成测试")
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
	service, err := content.NewService(repository, config.ContentConfig{
		MaxImages:         9,
		MaxImageSizeBytes: 10 * 1024 * 1024,
		MaxBodyLength:     10_000,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	pureText, err := service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "纯文字内容",
		Body:     "这篇内容没有图片，但仍然合法。",
	})
	if err != nil {
		t.Fatalf("Publish(pure text) error = %v", err)
	}
	if pureText.Status != content.PostStatusPublished || len(pureText.Images) != 0 || pureText.PublishedAt == nil {
		t.Fatalf("pure text post = %#v", pureText)
	}

	image1 := seedUnattachedImage(t, database, 1, "uploads/owner-1/image-1.jpg")
	image2 := seedUnattachedImage(t, database, 1, "uploads/owner-1/image-2.jpg")
	multiImage, err := service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "多图内容",
		Body:     "图片顺序应与请求保持一致。",
		ImageIDs: []uint64{image2, image1},
	})
	if err != nil {
		t.Fatalf("Publish(multi image) error = %v", err)
	}
	assertImageOrder(t, multiImage, []uint64{image2, image1})

	_, err = service.Update(ctx, content.UpdateInput{
		AuthorID: 99,
		PostID:   multiImage.ID,
		Title:    "越权编辑",
		Body:     "不应成功",
		ImageIDs: []uint64{image2, image1},
	})
	if !errors.Is(err, content.ErrPostForbidden) {
		t.Fatalf("unauthorized Update() error = %v", err)
	}
	if err := service.Delete(ctx, 99, multiImage.ID); !errors.Is(err, content.ErrPostForbidden) {
		t.Fatalf("unauthorized Delete() error = %v", err)
	}

	updated, err := service.Update(ctx, content.UpdateInput{
		AuthorID: 1,
		PostID:   multiImage.ID,
		Title:    "多图内容（已编辑）",
		Body:     "编辑后反转图片顺序。",
		ImageIDs: []uint64{image1, image2},
	})
	if err != nil {
		t.Fatalf("authorized Update() error = %v", err)
	}
	if updated.Title != "多图内容（已编辑）" {
		t.Fatalf("updated title = %q", updated.Title)
	}
	assertImageOrder(t, updated, []uint64{image1, image2})

	beforeDelete, err := service.ListByAuthor(ctx, content.AuthorListQuery{AuthorID: 1, Limit: 20})
	if err != nil || len(beforeDelete) != 2 {
		t.Fatalf("ListByAuthor() before delete = %d, %v; want 2, nil", len(beforeDelete), err)
	}
	if err := service.Delete(ctx, 1, multiImage.ID); err != nil {
		t.Fatalf("authorized Delete() error = %v", err)
	}
	if _, err := service.Detail(ctx, multiImage.ID); !errors.Is(err, content.ErrPostNotFound) {
		t.Fatalf("Detail(deleted) error = %v", err)
	}
	afterDelete, err := service.ListByAuthor(ctx, content.AuthorListQuery{AuthorID: 1, Limit: 20})
	if err != nil || len(afterDelete) != 1 || afterDelete[0].ID != pureText.ID {
		t.Fatalf("ListByAuthor() after delete = %#v, %v", afterDelete, err)
	}

	foreignImage := seedUnattachedImage(t, database, 2, "uploads/owner-2/foreign.jpg")
	postCountBefore := countRows(t, database, "posts")
	_, err = service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "非法图片内容",
		Body:     "尝试绑定其他用户图片。",
		ImageIDs: []uint64{foreignImage},
	})
	if !errors.Is(err, content.ErrImageUnavailable) {
		t.Fatalf("Publish(foreign image) error = %v", err)
	}
	if got := countRows(t, database, "posts"); got != postCountBefore {
		t.Fatalf("post count after rolled-back publish = %d, want %d", got, postCountBefore)
	}
	var attachedPostID sql.NullInt64
	if err := database.QueryRow("SELECT post_id FROM post_images WHERE id = ?", foreignImage).Scan(&attachedPostID); err != nil {
		t.Fatalf("query foreign image: %v", err)
	}
	if attachedPostID.Valid {
		t.Fatalf("foreign image unexpectedly attached to post %d", attachedPostID.Int64)
	}
}

func seedUnattachedImage(t *testing.T, database *sql.DB, ownerID uint64, storageKey string) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO post_images
    (owner_id, post_id, storage_key, image_url, mime_type, byte_size, width, height, sort_order, attached_at)
VALUES
    (?, NULL, ?, ?, 'image/jpeg', 1024, 800, 600, NULL, NULL)`,
		ownerID,
		storageKey,
		"/static/"+storageKey,
	)
	if err != nil {
		t.Fatalf("seed image %s: %v", storageKey, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("image LastInsertId: %v", err)
	}
	return uint64(id)
}

func assertImageOrder(t *testing.T, post *content.Post, want []uint64) {
	t.Helper()
	if len(post.Images) != len(want) {
		t.Fatalf("post %d image count = %d, want %d", post.ID, len(post.Images), len(want))
	}
	for index, imageID := range want {
		if post.Images[index].ID != imageID || post.Images[index].SortOrder == nil || int(*post.Images[index].SortOrder) != index {
			t.Fatalf("post %d images = %#v, want order %v", post.ID, post.Images, want)
		}
	}
}

func countRows(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM `" + table + "`").Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func openContentTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_content_service_test_%d", time.Now().UnixNano())
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
