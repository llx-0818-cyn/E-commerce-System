package content_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostPublishPersistsContentAndOutboxWithoutRabbitMQ(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Post Outbox 集成测试")
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

	// 故意不创建 RabbitMQ 连接或 Poller；发布只依赖本地 MySQL 事务。
	imageID := seedUnattachedImage(t, database, 1, "uploads/owner-1/outbox.jpg")
	post, err := service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "MQ 不可用也能发布 #Outbox",
		Body:     "内容和待发事件必须一起持久化。",
		ImageIDs: []uint64{imageID},
	})
	if err != nil {
		t.Fatalf("Publish() without RabbitMQ error = %v", err)
	}
	if countRows(t, database, "posts") != 1 || countRows(t, database, "outbox_events") != 1 {
		t.Fatalf("posts/outbox counts = %d/%d, want 1/1", countRows(t, database, "posts"), countRows(t, database, "outbox_events"))
	}

	var eventID, aggregateType, aggregateID, eventType, payload, status string
	if err := database.QueryRowContext(ctx, `
SELECT event_id, aggregate_type, aggregate_id, event_type, payload_json, status
FROM outbox_events
WHERE aggregate_type = 'post' AND aggregate_id = ?`, post.ID).Scan(
		&eventID,
		&aggregateType,
		&aggregateID,
		&eventType,
		&payload,
		&status,
	); err != nil {
		t.Fatalf("query content.published outbox: %v", err)
	}
	if eventID == "" || aggregateType != "post" || eventType != "content.published" || status != "pending" {
		t.Fatalf("outbox envelope = %q/%q/%q/%q/%q", eventID, aggregateType, aggregateID, eventType, status)
	}
	var decoded struct {
		PostID   uint64 `json:"post_id"`
		AuthorID uint64 `json:"author_id"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if decoded.PostID != post.ID || decoded.AuthorID != 1 {
		t.Fatalf("outbox payload = %#v", decoded)
	}

	rollbackImage := seedUnattachedImage(t, database, 1, "uploads/owner-1/outbox-rollback.jpg")
	postCountBefore := countRows(t, database, "posts")
	outboxCountBefore := countRows(t, database, "outbox_events")
	if _, err := database.Exec(`
CREATE TRIGGER force_content_outbox_failure
BEFORE INSERT ON outbox_events
FOR EACH ROW
BEGIN
    IF NEW.event_type = 'content.published' THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced content outbox failure';
    END IF;
END`); err != nil {
		t.Fatalf("create outbox failure trigger: %v", err)
	}
	_, err = service.Publish(ctx, content.PublishInput{
		AuthorID: 1,
		Title:    "Outbox 失败应回滚 #Rollback",
		Body:     "这条内容不能残留。",
		ImageIDs: []uint64{rollbackImage},
	})
	if err == nil {
		t.Fatal("Publish() with forced outbox failure error = nil")
	}
	if got := countRows(t, database, "posts"); got != postCountBefore {
		t.Fatalf("posts after outbox rollback = %d, want %d", got, postCountBefore)
	}
	if got := countRows(t, database, "outbox_events"); got != outboxCountBefore {
		t.Fatalf("outbox after rollback = %d, want %d", got, outboxCountBefore)
	}
	var rollbackPostID sql.NullInt64
	if err := database.QueryRow("SELECT post_id FROM post_images WHERE id = ?", rollbackImage).Scan(&rollbackPostID); err != nil {
		t.Fatalf("query rollback image: %v", err)
	}
	if rollbackPostID.Valid {
		t.Fatalf("rollback image remained attached to post %d", rollbackPostID.Int64)
	}
	var rollbackTagCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM tags WHERE name = 'rollback'").Scan(&rollbackTagCount); err != nil {
		t.Fatalf("query rollback tag: %v", err)
	}
	if rollbackTagCount != 0 {
		t.Fatalf("rollback tag count = %d, want 0", rollbackTagCount)
	}
}
