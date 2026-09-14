package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"
)

func TestContentInteractionMigrationUniquenessAndNonNegativeCounts(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行互动 Schema Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "content_interaction")
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
		t.Fatalf("Up() = %d, %v; want %d, nil", count, err, len(migrationItems))
	}

	assertIndex(t, database, schema, "post_likes", "uq_post_likes_post_account", true, []string{"post_id", "account_id"})
	assertIndex(t, database, schema, "post_likes", "idx_post_likes_account_page", false, []string{"account_id", "created_at", "post_id"})
	assertIndex(t, database, schema, "post_favorites", "uq_post_favorites_post_account", true, []string{"post_id", "account_id"})
	assertIndex(t, database, schema, "post_favorites", "idx_post_favorites_account_page", false, []string{"account_id", "created_at", "post_id"})
	assertIndex(t, database, schema, "post_comments", "idx_post_comments_post_page", false, []string{"post_id", "status", "created_at", "id"})

	result, err := database.ExecContext(ctx, `
INSERT INTO posts (author_id, title, body, status, published_at)
VALUES (1, '互动测试', '正文', 'published', CURRENT_TIMESTAMP(6))`)
	if err != nil {
		t.Fatalf("insert post: %v", err)
	}
	postID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("post LastInsertId: %v", err)
	}

	if _, err := database.ExecContext(ctx, "INSERT INTO post_likes (post_id, account_id) VALUES (?, 7)", postID); err != nil {
		t.Fatalf("insert post like: %v", err)
	}
	_, err = database.ExecContext(ctx, "INSERT INTO post_likes (post_id, account_id) VALUES (?, 7)", postID)
	assertDuplicateKey(t, err, "post like")

	if _, err := database.ExecContext(ctx, "INSERT INTO post_favorites (post_id, account_id) VALUES (?, 7)", postID); err != nil {
		t.Fatalf("insert post favorite: %v", err)
	}
	_, err = database.ExecContext(ctx, "INSERT INTO post_favorites (post_id, account_id) VALUES (?, 7)", postID)
	assertDuplicateKey(t, err, "post favorite")

	if _, err := database.ExecContext(ctx, "UPDATE posts SET likes_count = -1 WHERE id = ?", postID); err == nil {
		t.Fatal("negative likes_count update succeeded")
	}
	if _, err := database.ExecContext(ctx, "UPDATE posts SET favorites_count = favorites_count - 1 WHERE id = ?", postID); err == nil {
		t.Fatal("favorites_count underflow update succeeded")
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO post_comments (post_id, author_id, content) VALUES (?, 7, '   ')", postID); err == nil {
		t.Fatal("blank post comment insert succeeded")
	}
}
