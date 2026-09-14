package worker

import (
	"context"
	"encoding/json"
	"errors"

	"feedsystem_video_go/internal/middleware/rabbitmq"
	"feedsystem_video_go/internal/search"

	amqp "github.com/rabbitmq/amqp091-go"
)

type SearchIndexWorker struct {
	channel  *amqp.Channel
	consumer *search.IndexConsumer
	reliable *ReliableDeliveryHandler
}

func NewSearchIndexWorker(channel *amqp.Channel, consumer *search.IndexConsumer, reliable *ReliableDeliveryHandler) *SearchIndexWorker {
	return &SearchIndexWorker{channel: channel, consumer: consumer, reliable: reliable}
}

func (w *SearchIndexWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.consumer == nil || w.reliable == nil {
		return errors.New("search index worker is not initialized")
	}
	deliveries, err := w.channel.Consume(rabbitmq.SearchIndexQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("search index deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.consumer.Process(ctx, envelope.EventType, envelope.Payload)
			})
		}
	}
}
