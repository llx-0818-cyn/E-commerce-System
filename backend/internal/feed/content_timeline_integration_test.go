package feed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/worker"
	"feedsystem_video_go/migrations"

	"github.com/alicebob/miniredis/v2"
	amqp "github.com/rabbitmq/amqp091-go"
	redis "github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostPublishEventuallyEntersContentRedisTimeline(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	rabbitURL := os.Getenv("RABBITMQ_TEST_URL")
	if adminDSN == "" || rabbitURL == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 和 RABBITMQ_TEST_URL 后运行内容时间线集成测试")
	}
	database, cleanupDatabase := openFeedTestDatabase(t, adminDSN)
	defer cleanupDatabase()
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

	connection, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatalf("dial RabbitMQ: %v", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open RabbitMQ channel: %v", err)
	}
	defer channel.Close()
	suffix := time.Now().UnixNano()
	exchange := fmt.Sprintf("codex.content.events.%d", suffix)
	queue := fmt.Sprintf("codex.content.timeline.%d", suffix)
	topology, err := rabbitmq.BuildRetryTopology(exchange, queue, rabbitmq.ContentPublishedKey)
	if err != nil {
		t.Fatalf("BuildRetryTopology() error = %v", err)
	}
	defer func() {
		_, _ = channel.QueueDelete(topology.SourceQueue, false, false, false)
		_, _ = channel.QueueDelete(topology.RetryQueue, false, false, false)
		_, _ = channel.QueueDelete(topology.DeadLetterQueue, false, false, false)
		_ = channel.ExchangeDelete(topology.SourceExchange, false, false)
		_ = channel.ExchangeDelete(topology.RetryExchange, false, false)
	}()
	if err := rabbitmq.DeclareReliableTopic(channel, exchange, queue, rabbitmq.ContentPublishedKey, 50*time.Millisecond); err != nil {
		t.Fatalf("DeclareReliableTopic() error = %v", err)
	}

	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	rawRedis := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer rawRedis.Close()
	cache := rediscache.NewClient(rawRedis, "v1:")
	processor, err := worker.NewContentTimelineProcessor(cache)
	if err != nil {
		t.Fatalf("NewContentTimelineProcessor() error = %v", err)
	}

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	contentRepository, err := content.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	contentService, err := content.NewService(contentRepository, config.ContentConfig{MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	post, err := contentService.Publish(ctx, content.PublishInput{AuthorID: 1, Title: "时间线内容", Body: "发布后进入新 Redis ZSET"})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	base := &rabbitmq.RabbitMQ{Conn: connection}
	publisher, err := rabbitmq.NewOutboxPublisher(base, exchange)
	if err != nil {
		t.Fatalf("NewOutboxPublisher() error = %v", err)
	}
	defer publisher.Close()
	pollerConfig := event.DefaultPollerConfig("content-timeline-test-poller")
	pollerConfig.BatchSize = 10
	poller, err := event.NewPoller(gormDB, publisher, pollerConfig)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	result, err := poller.PollOnce(ctx)
	if err != nil || result.Published != 1 {
		t.Fatalf("PollOnce() = %#v, %v; want one published", result, err)
	}

	delivery := waitFeedDelivery(t, ctx, channel, queue)
	var envelope rabbitmq.DomainEventEnvelope
	if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
		t.Fatalf("decode domain envelope: %v", err)
	}
	if envelope.EventType != rabbitmq.ContentPublishedKey {
		t.Fatalf("event type = %q", envelope.EventType)
	}
	if err := processor.Process(ctx, envelope); err != nil {
		t.Fatalf("Process(content.published) error = %v", err)
	}
	if err := delivery.Ack(false); err != nil {
		t.Fatalf("Ack timeline delivery: %v", err)
	}

	timelineKey := feed.ContentTimelineKey(cache)
	score, err := rawRedis.ZScore(ctx, timelineKey, strconv.FormatUint(post.ID, 10)).Result()
	if err != nil {
		t.Fatalf("ZScore content timeline: %v", err)
	}
	if score != float64(post.PublishedAt.UnixMilli()) {
		t.Fatalf("timeline score = %.0f, want %d", score, post.PublishedAt.UnixMilli())
	}
	if key := feed.ContentEntityKey(cache, post.ID); key != fmt.Sprintf("v1:content:entity:%d", post.ID) {
		t.Fatalf("content entity key = %q", key)
	}
}

func waitFeedDelivery(t *testing.T, ctx context.Context, channel *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		delivery, ok, err := channel.Get(queue, false)
		if err != nil {
			t.Fatalf("get RabbitMQ delivery: %v", err)
		}
		if ok {
			return delivery
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait RabbitMQ delivery: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
