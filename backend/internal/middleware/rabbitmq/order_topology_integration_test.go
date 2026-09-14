package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestOrderExpirationRequestedMessageReachesDueQueueAfterTTL(t *testing.T) {
	url := os.Getenv("RABBITMQ_TEST_URL")
	if url == "" {
		t.Skip("设置 RABBITMQ_TEST_URL 后运行订单延迟关单 RabbitMQ 集成测试")
	}
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("dial RabbitMQ: %v", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open order topology channel: %v", err)
	}
	defer channel.Close()
	suffix := time.Now().UnixNano()
	topology := OrderTopology{
		Exchange:        fmt.Sprintf("codex.order.test.%d", suffix),
		LifecycleQueue:  fmt.Sprintf("codex.order.lifecycle.%d", suffix),
		DelayQueue:      fmt.Sprintf("codex.order.delay.%d", suffix),
		ExpirationQueue: fmt.Sprintf("codex.order.expiration.%d", suffix),
		DelayMillis:     (150 * time.Millisecond).Milliseconds(),
	}
	cleanupOrderTopology := func() {
		for _, queue := range []string{
			topology.DelayQueue,
			topology.ExpirationQueue, topology.ExpirationQueue + ".retry", topology.ExpirationQueue + ".dlq",
			topology.LifecycleQueue, topology.LifecycleQueue + ".retry", topology.LifecycleQueue + ".dlq",
		} {
			_, _ = channel.QueueDelete(queue, false, false, false)
		}
		_ = channel.ExchangeDelete(topology.ExpirationQueue+".retry.exchange", false, false)
		_ = channel.ExchangeDelete(topology.LifecycleQueue+".retry.exchange", false, false)
		_ = channel.ExchangeDelete(topology.Exchange, false, false)
	}
	cleanupOrderTopology()
	defer cleanupOrderTopology()
	if err := declareOrderTopologies(channel, topology, 50*time.Millisecond); err != nil {
		t.Fatalf("declareOrderTopologies() error = %v", err)
	}

	publishChannel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open order publisher channel: %v", err)
	}
	defer publishChannel.Close()
	publisher, err := NewConfirmPublisher(publishChannel, 2*time.Second)
	if err != nil {
		t.Fatalf("NewConfirmPublisher() error = %v", err)
	}
	envelope := DomainEventEnvelope{
		EventID: "order-expiration-test-1", AggregateType: "order", AggregateID: "42",
		EventType: OrderExpireRequestedKey, Payload: json.RawMessage(`{"order_id":42}`), OccurredAt: time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := publisher.PublishJSON(ctx, topology.Exchange, OrderExpireRequestedKey, envelope); err != nil {
		t.Fatalf("publish order expiration request: %v", err)
	}
	delivery := waitForDelivery(t, ctx, channel, topology.ExpirationQueue)
	if delivery.RoutingKey != OrderExpireDueKey {
		t.Fatalf("expiration routing key = %q, want %q", delivery.RoutingKey, OrderExpireDueKey)
	}
	var received DomainEventEnvelope
	if err := json.Unmarshal(delivery.Body, &received); err != nil || received.EventID != envelope.EventID || received.EventType != OrderExpireRequestedKey {
		t.Fatalf("expiration envelope = %#v, %v", received, err)
	}
	if err := delivery.Ack(false); err != nil {
		t.Fatalf("ack expiration delivery: %v", err)
	}
}
