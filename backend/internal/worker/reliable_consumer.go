package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/middleware/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

type FailureRecorder interface {
	Record(ctx context.Context, failure event.FailureRecord) error
}

type DeadLetterSink interface {
	Publish(ctx context.Context, payload any) error
}

type ReliableDeliveryHandler struct {
	consumerName string
	queueName    string
	maxRetries   int
	failures     FailureRecorder
	deadLetters  DeadLetterSink
	now          func() time.Time
}

type DeadLetterEnvelope struct {
	ConsumerName string          `json:"consumer_name"`
	QueueName    string          `json:"queue_name"`
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	RoutingKey   string          `json:"routing_key"`
	FailureStage string          `json:"failure_stage"`
	Attempts     int             `json:"attempts"`
	LastError    string          `json:"last_error"`
	FailedAt     time.Time       `json:"failed_at"`
	Payload      json.RawMessage `json:"payload"`
}

// NewRabbitReliableDeliveryHandler 为消费者创建独立 Confirm Channel；返回的
// cleanup 必须在该消费者重连/退出时调用。
func NewRabbitReliableDeliveryHandler(
	database *gorm.DB,
	rmq *rabbitmq.RabbitMQ,
	consumerName string,
	queueName string,
) (*ReliableDeliveryHandler, func(), error) {
	failures, err := event.NewFailureRepository(database)
	if err != nil {
		return nil, nil, err
	}
	if rmq == nil {
		return nil, nil, errors.New("rabbitmq connection is nil")
	}
	dlqChannel, err := rmq.NewChannel()
	if err != nil {
		return nil, nil, fmt.Errorf("open dead-letter publisher channel: %w", err)
	}
	cleanup := func() { _ = dlqChannel.Close() }
	deadLetters, err := rabbitmq.NewDeadLetterPublisher(dlqChannel, queueName, 5*time.Second)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	handler, err := NewReliableDeliveryHandler(consumerName, queueName, rabbitmq.MaxRetryCount, failures, deadLetters)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return handler, cleanup, nil
}

func NewReliableDeliveryHandler(
	consumerName string,
	queueName string,
	maxRetries int,
	failures FailureRecorder,
	deadLetters DeadLetterSink,
) (*ReliableDeliveryHandler, error) {
	consumerName = strings.TrimSpace(consumerName)
	queueName = strings.TrimSpace(queueName)
	if consumerName == "" || queueName == "" {
		return nil, errors.New("reliable consumer name and queue are required")
	}
	if maxRetries < 0 || maxRetries > 100 {
		return nil, errors.New("reliable consumer max retries must be in 0..100")
	}
	if failures == nil || deadLetters == nil {
		return nil, errors.New("reliable consumer failure repository and DLQ are required")
	}
	return &ReliableDeliveryHandler{
		consumerName: consumerName,
		queueName:    queueName,
		maxRetries:   maxRetries,
		failures:     failures,
		deadLetters:  deadLetters,
		now:          func() time.Time { return time.Now().UTC() },
	}, nil
}

// Handle 运行一次消费者逻辑：成功 ACK；可重试失败 NACK 到 TTL 重试队列；
// 耗尽后先持久化原因、再 Confirm 发布最终 DLQ，二者都成功才 ACK 原消息。
func (h *ReliableDeliveryHandler) Handle(
	ctx context.Context,
	delivery amqp.Delivery,
	failureStage string,
	process func(context.Context, amqp.Delivery) error,
) error {
	if h == nil || h.failures == nil || h.deadLetters == nil {
		return errors.New("reliable delivery handler is not initialized")
	}
	if process == nil {
		return errors.New("reliable consumer process callback is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := process(ctx, delivery); err == nil {
		return delivery.Ack(false)
	} else {
		return h.handleFailure(ctx, delivery, failureStage, err)
	}
}

func (h *ReliableDeliveryHandler) handleFailure(ctx context.Context, delivery amqp.Delivery, failureStage string, processErr error) error {
	retryCount := rabbitmq.GetRetryCountForQueue(delivery, h.queueName)
	if retryCount < h.maxRetries {
		if err := delivery.Nack(false, false); err != nil {
			return fmt.Errorf("route delivery to retry queue: %w", err)
		}
		return nil
	}

	metadata := deliveryMetadata(delivery)
	failedAt := h.now().UTC()
	attempts := retryCount + 1
	failure := event.FailureRecord{
		ConsumerName: h.consumerName,
		EventID:      metadata.EventID,
		EventType:    metadata.EventType,
		Payload:      normalizedJSONPayload(delivery.Body),
		FailureStage: failureStage,
		Attempts:     uint(attempts),
		LastError:    processErr,
		FailedAt:     failedAt,
	}
	if err := h.failures.Record(ctx, failure); err != nil {
		_ = delivery.Nack(false, true)
		return fmt.Errorf("persist exhausted delivery failure: %w", err)
	}

	envelope := DeadLetterEnvelope{
		ConsumerName: h.consumerName,
		QueueName:    h.queueName,
		EventID:      metadata.EventID,
		EventType:    metadata.EventType,
		RoutingKey:   delivery.RoutingKey,
		FailureStage: failureStage,
		Attempts:     attempts,
		LastError:    processErr.Error(),
		FailedAt:     failedAt,
		Payload:      normalizedJSONPayload(delivery.Body),
	}
	if err := h.deadLetters.Publish(ctx, envelope); err != nil {
		_ = delivery.Nack(false, true)
		return fmt.Errorf("publish exhausted delivery to DLQ: %w", err)
	}
	if err := delivery.Ack(false); err != nil {
		return fmt.Errorf("ack delivery after DLQ confirm: %w", err)
	}
	return nil
}

type messageMetadata struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
}

func deliveryMetadata(delivery amqp.Delivery) messageMetadata {
	metadata := messageMetadata{}
	_ = json.Unmarshal(delivery.Body, &metadata)
	metadata.EventID = strings.TrimSpace(metadata.EventID)
	metadata.EventType = strings.TrimSpace(metadata.EventType)
	if metadata.EventID == "" {
		metadata.EventID = strings.TrimSpace(delivery.MessageId)
	}
	if metadata.EventID == "" {
		digest := sha256.Sum256(delivery.Body)
		metadata.EventID = hex.EncodeToString(digest[:16])
	}
	if metadata.EventType == "" {
		metadata.EventType = strings.TrimSpace(delivery.Type)
	}
	if metadata.EventType == "" {
		metadata.EventType = strings.TrimSpace(delivery.RoutingKey)
	}
	if metadata.EventType == "" {
		metadata.EventType = "unknown.event"
	}
	return metadata
}

func normalizedJSONPayload(body []byte) json.RawMessage {
	if json.Valid(body) {
		return append(json.RawMessage(nil), body...)
	}
	encoded, _ := json.Marshal(map[string]string{"raw_body_hex": hex.EncodeToString(body)})
	return encoded
}
