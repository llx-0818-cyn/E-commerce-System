package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	DLXExchange       = "dlx.events"
	MaxRetryCount     = 3
	DefaultRetryDelay = 5 * time.Second
)

type RetryTopology struct {
	SourceExchange  string
	SourceQueue     string
	BindingKey      string
	RetryExchange   string
	RetryQueue      string
	DeadLetterQueue string
	DeadLetterKey   string
}

func BuildRetryTopology(exchange, queue, bindingKey string) (RetryTopology, error) {
	if exchange == "" || queue == "" || bindingKey == "" {
		return RetryTopology{}, errors.New("exchange/queue/bindingKey is required")
	}
	return RetryTopology{
		SourceExchange:  exchange,
		SourceQueue:     queue,
		BindingKey:      bindingKey,
		RetryExchange:   queue + ".retry.exchange",
		RetryQueue:      queue + ".retry",
		DeadLetterQueue: queue + ".dlq",
		DeadLetterKey:   queue + ".failed",
	}, nil
}

// DeclareReliableTopic 声明主队列、隔离的 TTL 重试队列和最终可查询 DLQ。
// 主队列 Reject/Nack(requeue=false) 后进入专属重试 Exchange；TTL 到期按原
// routing key 返回源 Exchange，避免多个消费者的重试队列互相串消息。
func DeclareReliableTopic(ch *amqp.Channel, exchange, queue, bindingKey string, retryDelay time.Duration) error {
	if ch == nil {
		return errors.New("channel is not initialized")
	}
	if retryDelay <= 0 {
		return errors.New("retry delay must be positive")
	}
	topology, err := BuildRetryTopology(exchange, queue, bindingKey)
	if err != nil {
		return err
	}

	for _, exchangeName := range []string{topology.SourceExchange, topology.RetryExchange, DLXExchange} {
		if err := ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %s: %w", exchangeName, err)
		}
	}

	mainQueue, err := ch.QueueDeclare(
		topology.SourceQueue,
		true,
		false,
		false,
		false,
		amqp.Table{"x-dead-letter-exchange": topology.RetryExchange},
	)
	if err != nil {
		return fmt.Errorf("declare source queue %s: %w", topology.SourceQueue, err)
	}
	if err := ch.QueueBind(mainQueue.Name, topology.BindingKey, topology.SourceExchange, false, nil); err != nil {
		return fmt.Errorf("bind source queue %s: %w", topology.SourceQueue, err)
	}

	retryQueue, err := ch.QueueDeclare(
		topology.RetryQueue,
		true,
		false,
		false,
		false,
		amqp.Table{
			"x-message-ttl":          retryDelay.Milliseconds(),
			"x-dead-letter-exchange": topology.SourceExchange,
		},
	)
	if err != nil {
		return fmt.Errorf("declare retry queue %s: %w", topology.RetryQueue, err)
	}
	if err := ch.QueueBind(retryQueue.Name, "#", topology.RetryExchange, false, nil); err != nil {
		return fmt.Errorf("bind retry queue %s: %w", topology.RetryQueue, err)
	}

	deadLetterQueue, err := ch.QueueDeclare(topology.DeadLetterQueue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declare DLQ %s: %w", topology.DeadLetterQueue, err)
	}
	if err := ch.QueueBind(deadLetterQueue.Name, topology.DeadLetterKey, DLXExchange, false, nil); err != nil {
		return fmt.Errorf("bind DLQ %s: %w", topology.DeadLetterQueue, err)
	}
	return nil
}

// GetRetryCountForQueue 从 x-death 中读取该源队列被 reject 的次数。
func GetRetryCountForQueue(delivery amqp.Delivery, queueName string) int {
	deaths, ok := delivery.Headers["x-death"].([]interface{})
	if !ok {
		return 0
	}
	for _, raw := range deaths {
		death, ok := raw.(amqp.Table)
		if !ok || fmt.Sprint(death["queue"]) != queueName || fmt.Sprint(death["reason"]) != "rejected" {
			continue
		}
		switch count := death["count"].(type) {
		case int64:
			return int(count)
		case int32:
			return int(count)
		case int:
			return count
		}
	}
	return 0
}

func GetRetryCount(delivery amqp.Delivery) int {
	deaths, ok := delivery.Headers["x-death"].([]interface{})
	if !ok {
		return 0
	}
	for _, raw := range deaths {
		death, ok := raw.(amqp.Table)
		if !ok || fmt.Sprint(death["reason"]) != "rejected" {
			continue
		}
		switch count := death["count"].(type) {
		case int64:
			return int(count)
		case int:
			return count
		}
	}
	return 0
}

type DeadLetterPublisher struct {
	publisher  *ConfirmPublisher
	routingKey string
}

func NewDeadLetterPublisher(channel *amqp.Channel, sourceQueue string, timeout time.Duration) (*DeadLetterPublisher, error) {
	if sourceQueue == "" {
		return nil, errors.New("dead letter source queue is required")
	}
	publisher, err := NewConfirmPublisher(channel, timeout)
	if err != nil {
		return nil, err
	}
	return &DeadLetterPublisher{publisher: publisher, routingKey: sourceQueue + ".failed"}, nil
}

func (p *DeadLetterPublisher) Publish(ctx context.Context, payload any) error {
	if p == nil || p.publisher == nil {
		return errors.New("dead letter publisher is not initialized")
	}
	return p.publisher.PublishJSON(ctx, DLXExchange, p.routingKey, payload)
}
