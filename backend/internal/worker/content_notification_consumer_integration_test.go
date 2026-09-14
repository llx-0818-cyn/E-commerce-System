package worker

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type recordingNotificationHub struct {
	mu     sync.Mutex
	byUser map[uint64][]Notification
}

type combinedNotificationHub struct {
	hubs []NotificationHub
}

func (h *combinedNotificationHub) Push(userID uint64, notification *Notification) {
	for _, hub := range h.hubs {
		hub.Push(userID, notification)
	}
}

func newRecordingNotificationHub() *recordingNotificationHub {
	return &recordingNotificationHub{byUser: make(map[uint64][]Notification)}
}

func (h *recordingNotificationHub) Push(userID uint64, notification *Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byUser[userID] = append(h.byUser[userID], *notification)
}

func (h *recordingNotificationHub) count(userID uint64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byUser[userID])
}

func TestContentNotificationsOnlineOfflineIdempotentAndNoSelfNotify(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容通知集成测试")
	}
	database, cleanup := openReliableConsumerTestDatabase(t, adminDSN)
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
	if _, err := database.Exec(`
CREATE TABLE accounts (
    id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`); err != nil {
		t.Fatalf("create accounts fixture: %v", err)
	}
	for id, username := range map[uint64]string{1: "author", 2: "actor", 3: "alice", 4: "offline"} {
		if _, err := database.Exec("INSERT INTO accounts (id, username) VALUES (?, ?)", id, username); err != nil {
			t.Fatalf("seed account %d: %v", id, err)
		}
	}
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	recordingHub := newRecordingNotificationHub()
	sseHub := NewSSEHub(gormDB)
	authorStream := sseHub.Subscribe(1)
	aliceStream := sseHub.Subscribe(3)
	actorStream := sseHub.Subscribe(2)
	defer sseHub.Unsubscribe(1, authorStream)
	defer sseHub.Unsubscribe(3, aliceStream)
	defer sseHub.Unsubscribe(2, actorStream)
	hub := &combinedNotificationHub{hubs: []NotificationHub{recordingHub, sseHub}}
	consumer, err := NewContentNotificationConsumer(gormDB, hub)
	if err != nil {
		t.Fatalf("NewContentNotificationConsumer() error = %v", err)
	}

	likedPayload := mustJSON(t, map[string]any{"post_id": 10, "post_author_id": 1, "actor_id": 2})
	applied, err := consumer.Handle(ctx, "evt-like-1", "content.liked", likedPayload)
	if err != nil || !applied || recordingHub.count(1) != 1 {
		t.Fatalf("first like notification = %v, %v, pushes=%d", applied, err, recordingHub.count(1))
	}
	assertStreamNotification(t, authorStream, "like")
	applied, err = consumer.Handle(ctx, "evt-like-1", "content.liked", likedPayload)
	if err != nil || applied || recordingHub.count(1) != 1 {
		t.Fatalf("duplicate like notification = %v, %v, pushes=%d", applied, err, recordingHub.count(1))
	}

	selfLike := mustJSON(t, map[string]any{"post_id": 10, "post_author_id": 1, "actor_id": 1})
	applied, err = consumer.Handle(ctx, "evt-like-self", "content.liked", selfLike)
	if err != nil || !applied || recordingHub.count(1) != 1 {
		t.Fatalf("self like notification = %v, %v, pushes=%d", applied, err, recordingHub.count(1))
	}

	commentPayload := mustJSON(t, map[string]any{
		"post_id": 10, "post_author_id": 1, "comment_id": 99, "actor_id": 2,
		"mentioned_usernames": []string{"alice", "actor", "author", "ALICE"},
	})
	applied, err = consumer.Handle(ctx, "evt-comment-1", "content.comment.created", commentPayload)
	if err != nil || !applied {
		t.Fatalf("comment notification = %v, %v", applied, err)
	}
	if recordingHub.count(1) != 2 || recordingHub.count(3) != 1 || recordingHub.count(2) != 0 {
		t.Fatalf("comment pushes author/alice/actor = %d/%d/%d", recordingHub.count(1), recordingHub.count(3), recordingHub.count(2))
	}
	assertStreamNotification(t, authorStream, "comment")
	assertStreamNotification(t, aliceStream, "mention")
	assertNoStreamNotification(t, actorStream)

	offlineConsumer, err := NewContentNotificationConsumer(gormDB, nil)
	if err != nil {
		t.Fatalf("NewContentNotificationConsumer(offline) error = %v", err)
	}
	followPayload := mustJSON(t, map[string]any{"follower_id": 2, "vlogger_id": 4})
	applied, err = offlineConsumer.Handle(ctx, "evt-follow-1", "social.followed", followPayload)
	if err != nil || !applied {
		t.Fatalf("offline follow notification = %v, %v", applied, err)
	}
	notifications, err := ListNotifications(ctx, gormDB, 4, 20)
	if err != nil || len(notifications) != 1 || notifications[0].Type != "follow" {
		t.Fatalf("offline ListNotifications() = %#v, %v", notifications, err)
	}

	selfFollow := mustJSON(t, map[string]any{"follower_id": 4, "vlogger_id": 4})
	if applied, err := offlineConsumer.Handle(ctx, "evt-follow-self", "social.followed", selfFollow); err != nil || !applied {
		t.Fatalf("self follow event = %v, %v", applied, err)
	}
	notifications, err = ListNotifications(ctx, gormDB, 4, 20)
	if err != nil || len(notifications) != 1 {
		t.Fatalf("self follow ListNotifications() = %#v, %v", notifications, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test JSON: %v", err)
	}
	return data
}

func assertStreamNotification(t *testing.T, stream <-chan *Notification, wantType string) {
	t.Helper()
	select {
	case notification := <-stream:
		if notification == nil || notification.Type != wantType {
			t.Fatalf("stream notification = %#v, want type %s", notification, wantType)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s SSE notification", wantType)
	}
}

func assertNoStreamNotification(t *testing.T, stream <-chan *Notification) {
	t.Helper()
	select {
	case notification := <-stream:
		t.Fatalf("unexpected self-interaction SSE notification: %#v", notification)
	default:
	}
}
