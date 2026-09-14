package worker

import (
	"context"
	"encoding/json"
	"errors"

	"feedsystem_video_go/internal/cacheinvalidation"
	"feedsystem_video_go/internal/middleware/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type CacheInvalidationWorker struct {
	channel   *amqp.Channel
	processor *cacheinvalidation.Processor
	reliable  *ReliableDeliveryHandler
}

func NewCacheInvalidationWorker(channel *amqp.Channel, processor *cacheinvalidation.Processor, reliable *ReliableDeliveryHandler) *CacheInvalidationWorker {
	return &CacheInvalidationWorker{channel: channel, processor: processor, reliable: reliable}
}

func (w *CacheInvalidationWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.processor == nil || w.reliable == nil {
		return errors.New("cache invalidation worker is not initialized")
	}
	deliveries, err := w.channel.Consume(rabbitmq.CacheInvalidationQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("cache invalidation deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.processor.Process(ctx, envelope.EventType, envelope.Payload)
			})
		}
	}
}
