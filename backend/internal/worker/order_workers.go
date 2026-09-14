package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"feedsystem_video_go/internal/middleware/rabbitmq"
	orderdomain "feedsystem_video_go/internal/order"

	amqp "github.com/rabbitmq/amqp091-go"
)

type OrderCreatedRecorder interface {
	RecordOrderCreated(context.Context, uint64, uint64, uint64, int64) error
}

type OrderLifecycleProcessor struct {
	recorder OrderCreatedRecorder
}

func NewOrderLifecycleProcessor(recorder OrderCreatedRecorder) (*OrderLifecycleProcessor, error) {
	if recorder == nil {
		return nil, errors.New("order lifecycle recorder is nil")
	}
	return &OrderLifecycleProcessor{recorder: recorder}, nil
}

func (p *OrderLifecycleProcessor) Process(ctx context.Context, envelope rabbitmq.DomainEventEnvelope) error {
	var payload struct {
		OrderID   uint64 `json:"order_id"`
		BuyerID   uint64 `json:"buyer_id"`
		ShopID    uint64 `json:"shop_id"`
		TotalCent int64  `json:"total_cent"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.OrderID == 0 {
		return errors.New("order lifecycle payload is invalid")
	}
	switch envelope.EventType {
	case orderdomain.OrderCreatedEvent:
		if payload.BuyerID == 0 || payload.ShopID == 0 || payload.TotalCent <= 0 {
			return errors.New("order.created payload is invalid")
		}
		return p.recorder.RecordOrderCreated(ctx, payload.BuyerID, payload.OrderID, payload.ShopID, payload.TotalCent)
	case orderdomain.OrderPaidEvent:
		return nil
	case orderdomain.OrderCanceledEvent, orderdomain.OrderShippedEvent, orderdomain.OrderCompletedEvent:
		return nil
	default:
		return fmt.Errorf("unsupported order lifecycle event %q", envelope.EventType)
	}
}

type OrderExpirationService interface {
	ExpireOrder(context.Context, uint64, time.Time) (*orderdomain.Order, error)
}

type OrderExpirationProcessor struct {
	service OrderExpirationService
	now     func() time.Time
}

func NewOrderExpirationProcessor(service OrderExpirationService) (*OrderExpirationProcessor, error) {
	if service == nil {
		return nil, errors.New("order expiration service is nil")
	}
	return &OrderExpirationProcessor{service: service, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (p *OrderExpirationProcessor) Process(ctx context.Context, envelope rabbitmq.DomainEventEnvelope) error {
	if envelope.EventType != orderdomain.OrderExpireRequestedEvent {
		return fmt.Errorf("unsupported order expiration event %q", envelope.EventType)
	}
	var payload struct {
		OrderID uint64 `json:"order_id"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.OrderID == 0 {
		return errors.New("order expiration payload is invalid")
	}
	_, err := p.service.ExpireOrder(ctx, payload.OrderID, p.now().UTC())
	return err
}

type OrderDomainWorker struct {
	channel   *amqp.Channel
	queue     string
	reliable  *ReliableDeliveryHandler
	processor func(context.Context, rabbitmq.DomainEventEnvelope) error
}

func NewOrderLifecycleWorker(channel *amqp.Channel, reliable *ReliableDeliveryHandler, processor *OrderLifecycleProcessor) *OrderDomainWorker {
	var process func(context.Context, rabbitmq.DomainEventEnvelope) error
	if processor != nil {
		process = processor.Process
	}
	return &OrderDomainWorker{channel: channel, queue: rabbitmq.OrderLifecycleQueue, reliable: reliable, processor: process}
}

func NewOrderExpirationWorker(channel *amqp.Channel, reliable *ReliableDeliveryHandler, processor *OrderExpirationProcessor) *OrderDomainWorker {
	var process func(context.Context, rabbitmq.DomainEventEnvelope) error
	if processor != nil {
		process = processor.Process
	}
	return &OrderDomainWorker{channel: channel, queue: rabbitmq.OrderExpirationQueue, reliable: reliable, processor: process}
}

func (w *OrderDomainWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.queue == "" || w.reliable == nil || w.processor == nil {
		return errors.New("order domain worker is not initialized")
	}
	deliveries, err := w.channel.Consume(w.queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("order domain deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.processor(ctx, envelope)
			})
		}
	}
}

type OrderExpirationScanner interface {
	ScanExpiredOrders(context.Context, time.Time, int) (orderdomain.ExpirationScanResult, error)
}

func RunOrderExpirationScanner(ctx context.Context, scanner OrderExpirationScanner, interval time.Duration, limit int, report func(error)) error {
	if scanner == nil || interval <= 0 || limit < 1 || limit > 1000 {
		return errors.New("order expiration scanner configuration is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if _, err := scanner.ScanExpiredOrders(ctx, now.UTC(), limit); err != nil && report != nil {
				report(err)
			}
		}
	}
}
