package analytics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/analytics"
	domainevent "feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/middleware/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestBehaviorOutboxEventPublishesOnlyToDedicatedExchange(t *testing.T) {
	rabbitURL := os.Getenv("RABBITMQ_TEST_URL")
	if rabbitURL == "" {
		t.Skip("设置 RABBITMQ_TEST_URL 后运行行为事件 RabbitMQ 路由测试")
	}
	connection, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatalf("dial RabbitMQ: %v", err)
	}
	defer connection.Close()
	base := &rabbitmq.RabbitMQ{Conn: connection}
	suffix := time.Now().UnixNano()
	contentExchange := fmt.Sprintf("codex.behavior.default.%d", suffix)
	behaviorExchange := fmt.Sprintf("codex.behavior.events.%d", suffix)
	publisher, err := rabbitmq.NewRoutedOutboxPublisher(base, contentExchange, []rabbitmq.EventExchangeRoute{
		{EventTypePrefix: "behavior.", Exchange: behaviorExchange},
	})
	if err != nil {
		t.Fatalf("NewRoutedOutboxPublisher() error = %v", err)
	}
	defer publisher.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatalf("open assertion channel: %v", err)
	}
	defer channel.Close()
	behaviorQueue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("declare behavior assertion queue: %v", err)
	}
	if err := channel.QueueBind(behaviorQueue.Name, "behavior.#", behaviorExchange, false, nil); err != nil {
		t.Fatalf("bind behavior assertion queue: %v", err)
	}
	contentQueue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("declare content assertion queue: %v", err)
	}
	if err := channel.QueueBind(contentQueue.Name, "#", contentExchange, false, nil); err != nil {
		t.Fatalf("bind content assertion queue: %v", err)
	}
	defer channel.ExchangeDelete(contentExchange, false, false)
	defer channel.ExchangeDelete(behaviorExchange, false, false)

	payload, _ := json.Marshal(analytics.BehaviorEvent{
		ActorID: 7, ProductID: 42, ShopID: 9, OccurredAt: time.Now().UTC(),
	})
	if err := publisher.Publish(context.Background(), domainevent.OutboxEvent{
		EventID: "behavior-route-event", AggregateType: "product", AggregateID: "42",
		EventType: analytics.ProductClickedEvent, PayloadJSON: string(payload), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Publish(behavior event) error = %v", err)
	}
	delivery := waitBehaviorDelivery(t, channel, behaviorQueue.Name)
	if delivery.RoutingKey != analytics.ProductClickedEvent {
		t.Fatalf("behavior routing key = %q", delivery.RoutingKey)
	}
	var envelope rabbitmq.DomainEventEnvelope
	if err := json.Unmarshal(delivery.Body, &envelope); err != nil || envelope.EventType != analytics.ProductClickedEvent {
		t.Fatalf("behavior envelope = %#v, %v", envelope, err)
	}
	if _, ok, err := channel.Get(contentQueue.Name, true); err != nil || ok {
		t.Fatalf("default content exchange delivery ok=%v err=%v, want none", ok, err)
	}
}

func waitBehaviorDelivery(t *testing.T, channel *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		delivery, ok, err := channel.Get(queue, true)
		if err != nil {
			t.Fatalf("get behavior delivery: %v", err)
		}
		if ok {
			return delivery
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for behavior delivery")
	return amqp.Delivery{}
}
