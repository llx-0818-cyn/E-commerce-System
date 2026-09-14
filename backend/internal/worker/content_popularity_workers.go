package worker

import (
	"context"
	"encoding/json"
	"errors"

	"feedsystem_video_go/internal/analytics"
	"feedsystem_video_go/internal/middleware/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type ContentPopularityWorker struct {
	channel    *amqp.Channel
	queue      string
	aggregator *analytics.PopularityAggregator
	reliable   *ReliableDeliveryHandler
}

func NewContentPopularityWorker(channel *amqp.Channel, queue string, aggregator *analytics.PopularityAggregator, reliable *ReliableDeliveryHandler) *ContentPopularityWorker {
	return &ContentPopularityWorker{channel: channel, queue: queue, aggregator: aggregator, reliable: reliable}
}

func (w *ContentPopularityWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.queue == "" || w.aggregator == nil || w.reliable == nil {
		return errors.New("content popularity worker is not initialized")
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
				return errors.New("content popularity deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.aggregator.HandleContentEvent(ctx, envelope.EventType, envelope.Payload, envelope.OccurredAt)
			})
		}
	}
}

type BehaviorPopularityWorker struct {
	channel  *amqp.Channel
	queue    string
	consumer *analytics.Consumer
	reliable *ReliableDeliveryHandler
}

func NewBehaviorPopularityWorker(channel *amqp.Channel, queue string, consumer *analytics.Consumer, reliable *ReliableDeliveryHandler) *BehaviorPopularityWorker {
	return &BehaviorPopularityWorker{channel: channel, queue: queue, consumer: consumer, reliable: reliable}
}

func (w *BehaviorPopularityWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.queue == "" || w.consumer == nil || w.reliable == nil {
		return errors.New("behavior popularity worker is not initialized")
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
				return errors.New("behavior popularity deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.consumer.Handle(ctx, envelope.EventType, envelope.Payload)
			})
		}
	}
}
