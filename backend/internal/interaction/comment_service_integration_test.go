package interaction_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostCommentPublishDeleteListMentionsAndVisibility(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Post 评论 Service 集成测试")
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
	postID := seedInteractionPost(t, database, 1, "评论目标", time.Now().UTC())

	if _, err := service.PublishComment(ctx, 7, postID, "   "); !errors.Is(err, interaction.ErrCommentValidation) {
		t.Fatalf("blank PublishComment() error = %v", err)
	}
	assertPostCounter(t, database, postID, "comments_count", 0)

	comment, err := service.PublishComment(ctx, 7, postID, "写得很好 @Alice @alice @张三")
	if err != nil {
		t.Fatalf("PublishComment() error = %v", err)
	}
	if len(comment.MentionedUsernames) != 2 || comment.MentionedUsernames[0] != "alice" || comment.MentionedUsernames[1] != "张三" {
		t.Fatalf("comment mentions = %#v", comment.MentionedUsernames)
	}
	assertPostCounter(t, database, postID, "comments_count", 1)
	assertInteractionOutboxCount(t, database, "content.comment.created", 1)

	list, err := service.ListComments(ctx, interaction.CommentListQuery{PostID: postID, Limit: 20})
	if err != nil || len(list.Comments) != 1 || list.Comments[0].ID != comment.ID {
		t.Fatalf("ListComments() = %#v, %v", list, err)
	}
	if err := service.DeleteComment(ctx, 8, comment.ID); !errors.Is(err, interaction.ErrCommentForbidden) {
		t.Fatalf("unauthorized DeleteComment() error = %v", err)
	}
	assertPostCounter(t, database, postID, "comments_count", 1)
	if err := service.DeleteComment(ctx, 7, comment.ID); err != nil {
		t.Fatalf("authorized DeleteComment() error = %v", err)
	}
	assertPostCounter(t, database, postID, "comments_count", 0)
	list, err = service.ListComments(ctx, interaction.CommentListQuery{PostID: postID, Limit: 20})
	if err != nil || len(list.Comments) != 0 {
		t.Fatalf("ListComments() after delete = %#v, %v", list, err)
	}

	deletedPostID := seedInteractionPost(t, database, 1, "已删除内容", time.Now().UTC())
	if _, err := database.Exec("UPDATE posts SET status='deleted', deleted_at=CURRENT_TIMESTAMP(6) WHERE id = ?", deletedPostID); err != nil {
		t.Fatalf("soft delete post fixture: %v", err)
	}
	if _, err := service.PublishComment(ctx, 7, deletedPostID, "不能评论"); !errors.Is(err, content.ErrPostNotFound) {
		t.Fatalf("PublishComment(deleted post) error = %v", err)
	}
	if _, err := service.ListComments(ctx, interaction.CommentListQuery{PostID: deletedPostID, Limit: 20}); !errors.Is(err, content.ErrPostNotFound) {
		t.Fatalf("ListComments(deleted post) error = %v", err)
	}
}
