package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"feedsystem_video_go/internal/event"

	amqp "github.com/rabbitmq/amqp091-go"
)

type DomainEventEnvelope struct {
	EventID       string          `json:"event_id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

type OutboxPublisher struct {
	base      *RabbitMQ
	channel   *amqp.Channel
	publisher *ConfirmPublisher
	exchange  string
	routes    []EventExchangeRoute
	mu        sync.Mutex
}

type EventExchangeRoute struct {
	EventTypePrefix string
	Exchange        string
}

func NewOutboxPublisher(base *RabbitMQ, exchange string) (*OutboxPublisher, error) {
	return NewRoutedOutboxPublisher(base, exchange, nil)
}

func NewRoutedOutboxPublisher(base *RabbitMQ, exchange string, routes []EventExchangeRoute) (*OutboxPublisher, error) {
	if base == nil || exchange == "" {
		return nil, errors.New("outbox publisher connection and exchange are required")
	}
	normalizedRoutes := make([]EventExchangeRoute, 0, len(routes))
	for _, route := range routes {
		route.EventTypePrefix = strings.TrimSpace(route.EventTypePrefix)
		route.Exchange = strings.TrimSpace(route.Exchange)
		if route.EventTypePrefix == "" || route.Exchange == "" {
			return nil, errors.New("outbox event exchange route is incomplete")
		}
		normalizedRoutes = append(normalizedRoutes, route)
	}
	publisher := &OutboxPublisher{base: base, exchange: exchange, routes: normalizedRoutes}
	if err := publisher.resetLocked(); err != nil {
		return nil, err
	}
	return publisher, nil
}

func (p *OutboxPublisher) Publish(ctx context.Context, item event.OutboxEvent) error {
	if p == nil || p.base == nil {
		return errors.New("outbox publisher is not initialized")
	}
	payload := json.RawMessage(item.PayloadJSON)
	if !json.Valid(payload) {
		return fmt.Errorf("outbox event %s payload is invalid JSON", item.EventID)
	}
	envelope := DomainEventEnvelope{
		EventID:       item.EventID,
		AggregateType: item.AggregateType,
		AggregateID:   item.AggregateID,
		EventType:     item.EventType,
		Payload:       payload,
		OccurredAt:    item.CreatedAt.UTC(),
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.publisher == nil {
		if err := p.resetLocked(); err != nil {
			return err
		}
	}
	exchange := p.exchangeForEvent(item.EventType)
	if err := p.publisher.PublishJSON(ctx, exchange, item.EventType, envelope); err == nil {
		return nil
	} else if resetErr := p.resetLocked(); resetErr != nil {
		return fmt.Errorf("publish outbox event %s and reconnect: %w; reconnect error: %v", item.EventID, err, resetErr)
	}
	return p.publisher.PublishJSON(ctx, exchange, item.EventType, envelope)
}

func (p *OutboxPublisher) exchangeForEvent(eventType string) string {
	exchange, matchLength := p.exchange, 0
	for _, route := range p.routes {
		if strings.HasPrefix(eventType, route.EventTypePrefix) && len(route.EventTypePrefix) > matchLength {
			exchange, matchLength = route.Exchange, len(route.EventTypePrefix)
		}
	}
	return exchange
}

func (p *OutboxPublisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channel == nil {
		return nil
	}
	err := p.channel.Close()
	p.channel = nil
	p.publisher = nil
	return err
}

func (p *OutboxPublisher) resetLocked() error {
	if p.channel != nil {
		_ = p.channel.Close()
	}
	p.channel = nil
	p.publisher = nil
	channel, err := p.base.NewChannel()
	if err != nil {
		return fmt.Errorf("open outbox publisher channel: %w", err)
	}
	exchanges := map[string]struct{}{p.exchange: {}}
	for _, route := range p.routes {
		exchanges[route.Exchange] = struct{}{}
	}
	for exchange := range exchanges {
		if err := channel.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
			channel.Close()
			return fmt.Errorf("declare outbox exchange %s: %w", exchange, err)
		}
	}
	confirmPublisher, err := NewConfirmPublisher(channel, defaultPublisherConfirmTimeout)
	if err != nil {
		channel.Close()
		return err
	}
	p.channel = channel
	p.publisher = confirmPublisher
	return nil
}

func DeclareContentTopologies(channel *amqp.Channel, retryDelay time.Duration) error {
	if err := DeclareReliableTopic(channel, ContentExchange, ContentTimelineQueue, ContentPublishedKey, retryDelay); err != nil {
		return err
	}
	if err := DeclareReliableTopic(channel, ContentExchange, ContentNotificationQueue, ContentLikedKey, retryDelay); err != nil {
		return err
	}
	if err := channel.QueueBind(ContentNotificationQueue, ContentCommentCreatedKey, ContentExchange, false, nil); err != nil {
		return fmt.Errorf("bind content comment notification: %w", err)
	}
	if err := DeclareReliableTopic(channel, ContentExchange, ContentPopularityQueue, ContentLikedKey, retryDelay); err != nil {
		return err
	}
	for _, key := range []string{ContentFavoritedKey, ContentCommentCreatedKey} {
		if err := channel.QueueBind(ContentPopularityQueue, key, ContentExchange, false, nil); err != nil {
			return fmt.Errorf("bind content popularity %s: %w", key, err)
		}
	}
	return nil
}

func DeclareBehaviorTopology(channel *amqp.Channel, retryDelay time.Duration) error {
	return DeclareReliableTopic(channel, BehaviorExchange, BehaviorQueue, BehaviorBindingKey, retryDelay)
}

func DeclareCacheInvalidationTopology(channel *amqp.Channel, retryDelay time.Duration) error {
	return DeclareReliableTopic(channel, CacheExchange, CacheInvalidationQueue, CacheInvalidationKey, retryDelay)
}

func DeclareSearchIndexTopology(channel *amqp.Channel, retryDelay time.Duration) error {
	return DeclareReliableTopic(channel, SearchExchange, SearchIndexQueue, SearchIndexRequestedKey, retryDelay)
}

func DeclareSeckillTopology(channel *amqp.Channel, retryDelay time.Duration) error {
	if err := DeclareReliableTopic(channel, SeckillExchange, SeckillOrderQueue, SeckillOrderRequestedKey, retryDelay); err != nil {
		return err
	}
	if err := DeclareReliableTopic(channel, SeckillExchange, SeckillCompensationQueue, SeckillOrderFailedKey, retryDelay); err != nil {
		return err
	}
	if err := DeclareReliableTopic(channel, OrderExchange, SeckillLifecycleQueue, OrderPaidKey, retryDelay); err != nil {
		return err
	}
	return channel.QueueBind(SeckillLifecycleQueue, OrderCanceledKey, OrderExchange, false, nil)
}

type OrderTopology struct {
	Exchange        string
	LifecycleQueue  string
	DelayQueue      string
	ExpirationQueue string
	DelayMillis     int64
}

func BuildOrderTopology(paymentDelay time.Duration) (OrderTopology, error) {
	if paymentDelay <= 0 {
		return OrderTopology{}, errors.New("order payment delay must be positive")
	}
	return OrderTopology{
		Exchange: OrderExchange, LifecycleQueue: OrderLifecycleQueue,
		DelayQueue: OrderExpirationDelayQueue, ExpirationQueue: OrderExpirationQueue,
		DelayMillis: paymentDelay.Milliseconds(),
	}, nil
}

func DeclareOrderTopologies(channel *amqp.Channel, paymentDelay, retryDelay time.Duration) error {
	if channel == nil {
		return errors.New("channel is not initialized")
	}
	topology, err := BuildOrderTopology(paymentDelay)
	if err != nil {
		return err
	}
	return declareOrderTopologies(channel, topology, retryDelay)
}

func declareOrderTopologies(channel *amqp.Channel, topology OrderTopology, retryDelay time.Duration) error {
	if channel == nil || topology.Exchange == "" || topology.LifecycleQueue == "" || topology.DelayQueue == "" ||
		topology.ExpirationQueue == "" || topology.DelayMillis <= 0 {
		return errors.New("order topology is incomplete")
	}
	if err := DeclareReliableTopic(channel, topology.Exchange, topology.LifecycleQueue, OrderCreatedKey, retryDelay); err != nil {
		return err
	}
	for _, key := range []string{OrderPaidKey, OrderCanceledKey, OrderShippedKey, OrderCompletedKey} {
		if err := channel.QueueBind(topology.LifecycleQueue, key, topology.Exchange, false, nil); err != nil {
			return fmt.Errorf("bind order lifecycle %s: %w", key, err)
		}
	}
	if err := DeclareReliableTopic(channel, topology.Exchange, topology.ExpirationQueue, OrderExpireDueKey, retryDelay); err != nil {
		return err
	}
	delayQueue, err := channel.QueueDeclare(
		topology.DelayQueue,
		true,
		false,
		false,
		false,
		amqp.Table{
			"x-message-ttl":             topology.DelayMillis,
			"x-dead-letter-exchange":    topology.Exchange,
			"x-dead-letter-routing-key": OrderExpireDueKey,
		},
	)
	if err != nil {
		return fmt.Errorf("declare order expiration delay queue: %w", err)
	}
	if err := channel.QueueBind(delayQueue.Name, OrderExpireRequestedKey, topology.Exchange, false, nil); err != nil {
		return fmt.Errorf("bind order expiration delay queue: %w", err)
	}
	return nil
}
