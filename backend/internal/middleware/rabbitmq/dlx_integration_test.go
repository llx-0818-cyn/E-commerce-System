package rabbitmq

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestReliableTopicRetriesThenPublishesToRealDLQ(t *testing.T) {
	url := os.Getenv("RABBITMQ_TEST_URL")
	if url == "" {
		t.Skip("设置 RABBITMQ_TEST_URL 后运行 RabbitMQ 重试/DLQ 集成测试")
	}
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial RabbitMQ: %v", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open topology channel: %v", err)
	}
	defer channel.Close()

	suffix := time.Now().UnixNano()
	exchange := fmt.Sprintf("codex.retry.test.%d", suffix)
	queue := fmt.Sprintf("codex.retry.queue.%d", suffix)
	topology, err := BuildRetryTopology(exchange, queue, "test.*")
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
	if err := DeclareReliableTopic(channel, exchange, queue, "test.*", 50*time.Millisecond); err != nil {
		t.Fatalf("DeclareReliableTopic() error = %v", err)
	}

	publishChannel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open publisher channel: %v", err)
	}
	defer publishChannel.Close()
	publisher, err := NewConfirmPublisher(publishChannel, 2*time.Second)
	if err != nil {
		t.Fatalf("NewConfirmPublisher() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := publisher.PublishJSON(ctx, exchange, "test.failed", map[string]any{"event_id": "rabbit-retry-1"}); err != nil {
		t.Fatalf("publish source message: %v", err)
	}

	delivery, ok, err := channel.Get(queue, false)
	if err != nil || !ok {
		t.Fatalf("get source delivery = %v, %v", ok, err)
	}
	if err := delivery.Nack(false, false); err != nil {
		t.Fatalf("Nack source delivery: %v", err)
	}

	redelivered := waitForDelivery(t, ctx, channel, queue)
	if got := GetRetryCountForQueue(redelivered, queue); got != 1 {
		t.Fatalf("retry count = %d, want 1; headers=%v", got, redelivered.Headers)
	}

	dlqChannel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open DLQ publisher channel: %v", err)
	}
	defer dlqChannel.Close()
	dlqPublisher, err := NewDeadLetterPublisher(dlqChannel, queue, 2*time.Second)
	if err != nil {
		t.Fatalf("NewDeadLetterPublisher() error = %v", err)
	}
	if err := dlqPublisher.Publish(ctx, map[string]any{"event_id": "rabbit-retry-1", "last_error": "forced"}); err != nil {
		t.Fatalf("publish final DLQ: %v", err)
	}
	if err := redelivered.Ack(false); err != nil {
		t.Fatalf("Ack source after final DLQ: %v", err)
	}

	deadLetter := waitForDelivery(t, ctx, channel, topology.DeadLetterQueue)
	if deadLetter.RoutingKey != topology.DeadLetterKey {
		t.Fatalf("DLQ routing key = %q, want %q", deadLetter.RoutingKey, topology.DeadLetterKey)
	}
	if err := deadLetter.Ack(false); err != nil {
		t.Fatalf("Ack final DLQ test message: %v", err)
	}
}

func waitForDelivery(t *testing.T, ctx context.Context, channel *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		delivery, ok, err := channel.Get(queue, false)
		if err != nil {
			t.Fatalf("get delivery from %s: %v", queue, err)
		}
		if ok {
			return delivery
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait delivery from %s: %v", queue, ctx.Err())
		case <-ticker.C:
		}
	}
}
