package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"
)

func TestContentMigrationConstraintsAndPaginationIndexes(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容 Schema Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "content_schema")
	defer cleanup()

	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(items) {
		t.Fatalf("Up() = %d, %v; want %d, nil", count, err, len(items))
	}

	assertIndex(t, database, schema, "posts", "idx_posts_public_page", false, []string{"status", "published_at", "id"})
	assertIndex(t, database, schema, "posts", "idx_posts_author_page", false, []string{"author_id", "status", "published_at", "id"})
	assertIndex(t, database, schema, "posts", "idx_posts_popularity_page", false, []string{"status", "popularity", "published_at", "id"})
	assertIndex(t, database, schema, "post_images", "uq_post_images_order", true, []string{"post_id", "sort_order"})
	assertIndex(t, database, schema, "post_images", "idx_post_images_owner_unattached", false, []string{"owner_id", "post_id", "created_at", "id"})
	assertIndex(t, database, schema, "post_tags", "PRIMARY", true, []string{"post_id", "tag_id"})
	assertIndex(t, database, schema, "post_tags", "idx_post_tags_tag_post", false, []string{"tag_id", "post_id"})

	result, err := database.ExecContext(ctx, `
INSERT INTO posts (author_id, title, body, status, published_at)
VALUES (1, '第一篇图文', '纯文字正文', 'published', CURRENT_TIMESTAMP(6))`)
	if err != nil {
		t.Fatalf("insert post: %v", err)
	}
	postID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("post LastInsertId: %v", err)
	}
	attachedAt := time.Now().UTC()
	for order, key := range []string{"posts/1/a.jpg", "posts/1/b.jpg"} {
		if _, err := database.ExecContext(ctx, `
INSERT INTO post_images
    (owner_id, post_id, storage_key, image_url, mime_type, byte_size, width, height, sort_order, attached_at)
VALUES (?, ?, ?, ?, 'image/jpeg', 1024, 800, 600, ?, ?)`,
			1, postID, key, "/static/"+key, order, attachedAt,
		); err != nil {
			t.Fatalf("insert post image %d: %v", order, err)
		}
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO post_images
    (owner_id, post_id, storage_key, image_url, mime_type, byte_size, width, height, sort_order, attached_at)
VALUES (?, ?, 'posts/1/duplicate.jpg', '/static/posts/1/duplicate.jpg', 'image/jpeg', 1024, 800, 600, 1, ?)`,
		1, postID, attachedAt,
	)
	assertDuplicateKey(t, err, "post image sort order")

	if _, err := database.ExecContext(ctx, "INSERT INTO post_tags (post_id, tag_id) VALUES (?, 7)", postID); err != nil {
		t.Fatalf("insert post tag: %v", err)
	}
	_, err = database.ExecContext(ctx, "INSERT INTO post_tags (post_id, tag_id) VALUES (?, 7)", postID)
	assertDuplicateKey(t, err, "post tag")

	assertQueryUsesIndex(t, database, "SELECT id FROM posts FORCE INDEX (idx_posts_public_page) WHERE status = 'published' AND (published_at < CURRENT_TIMESTAMP(6) OR (published_at = CURRENT_TIMESTAMP(6) AND id < 999999)) ORDER BY status, published_at DESC, id DESC LIMIT 20", "idx_posts_public_page")
	assertQueryUsesIndex(t, database, "SELECT id FROM posts FORCE INDEX (idx_posts_author_page) WHERE author_id = 1 AND status = 'published' ORDER BY author_id, status, published_at DESC, id DESC LIMIT 20", "idx_posts_author_page")
}
