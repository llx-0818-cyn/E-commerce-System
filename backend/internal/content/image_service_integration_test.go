package content_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestImageServiceUploadCleanupInUseAndAtomicReorder(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Image Service 集成测试")
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
	storageRoot := t.TempDir()
	storage, err := content.NewLocalStorage(storageRoot, "/static", 1024*1024)
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	images, err := content.NewImageService(repository, storage)
	if err != nil {
		t.Fatalf("NewImageService() error = %v", err)
	}
	posts, err := content.NewService(repository, config.ContentConfig{MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	unused, err := images.Upload(ctx, 1, "unused.png", bytes.NewReader(integrationPNG(t, 5, 4)))
	if err != nil {
		t.Fatalf("Upload(unused) error = %v", err)
	}
	if unused.Width != 5 || unused.Height != 4 || unused.MIMEType != "image/png" || unused.PostID != nil {
		t.Fatalf("unused image metadata = %#v", unused)
	}
	unusedPath := filepath.Join(storageRoot, filepath.FromSlash(unused.StorageKey))
	if _, err := os.Stat(unusedPath); err != nil {
		t.Fatalf("unused image file stat: %v", err)
	}
	if err := images.DeleteUnused(ctx, 1, unused.ID); err != nil {
		t.Fatalf("DeleteUnused() error = %v", err)
	}
	if _, err := os.Stat(unusedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted unused image stat = %v", err)
	}

	first, err := images.Upload(ctx, 1, "first.png", bytes.NewReader(integrationPNG(t, 2, 2)))
	if err != nil {
		t.Fatalf("Upload(first) error = %v", err)
	}
	second, err := images.Upload(ctx, 1, "second.png", bytes.NewReader(integrationPNG(t, 3, 2)))
	if err != nil {
		t.Fatalf("Upload(second) error = %v", err)
	}
	post, err := posts.Publish(ctx, content.PublishInput{AuthorID: 1, Title: "图片顺序", Body: "初始顺序", ImageIDs: []uint64{first.ID, second.ID}})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := images.DeleteUnused(ctx, 1, first.ID); !errors.Is(err, content.ErrImageInUse) {
		t.Fatalf("DeleteUnused(attached) error = %v", err)
	}
	reordered, err := posts.ReorderImages(ctx, 1, post.ID, []uint64{second.ID, first.ID})
	if err != nil {
		t.Fatalf("ReorderImages() error = %v", err)
	}
	assertImageOrder(t, reordered, []uint64{second.ID, first.ID})

	oldUnused, err := images.Upload(ctx, 1, "old.png", bytes.NewReader(integrationPNG(t, 2, 3)))
	if err != nil {
		t.Fatalf("Upload(old) error = %v", err)
	}
	if _, err := database.Exec("UPDATE post_images SET created_at = ? WHERE id = ?", time.Now().UTC().Add(-48*time.Hour), oldUnused.ID); err != nil {
		t.Fatalf("age unused image: %v", err)
	}
	deleted, err := images.CleanupUnused(ctx, time.Now().UTC().Add(-24*time.Hour), 10)
	if err != nil || deleted != 1 {
		t.Fatalf("CleanupUnused() = %d, %v; want 1, nil", deleted, err)
	}
	if got := countRows(t, database, "post_images"); got != 2 {
		t.Fatalf("post_images after cleanup = %d, want 2 attached", got)
	}
}

func integrationPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, color.RGBA{R: 50, G: uint8(x + 1), B: uint8(y + 1), A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatalf("encode integration PNG: %v", err)
	}
	return buffer.Bytes()
}
