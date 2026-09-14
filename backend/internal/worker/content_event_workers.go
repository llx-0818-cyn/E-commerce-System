package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	amqp "github.com/rabbitmq/amqp091-go"
	oredis "github.com/redis/go-redis/v9"
)

type ContentTimelineProcessor struct {
	cache *rediscache.Client
}

func NewContentTimelineProcessor(cache *rediscache.Client) (*ContentTimelineProcessor, error) {
	if cache == nil {
		return nil, errors.New("content timeline cache is nil")
	}
	return &ContentTimelineProcessor{cache: cache}, nil
}

func (p *ContentTimelineProcessor) Process(ctx context.Context, envelope rabbitmq.DomainEventEnvelope) error {
	if envelope.EventType != rabbitmq.ContentPublishedKey {
		return fmt.Errorf("unsupported content timeline event %q", envelope.EventType)
	}
	var payload struct {
		PostID      uint64 `json:"post_id"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.PostID == 0 || payload.PublishedAt == "" {
		return errors.New("content.published payload is invalid")
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, payload.PublishedAt)
	if err != nil {
		return fmt.Errorf("parse content published_at: %w", err)
	}
	key := feed.ContentTimelineKey(p.cache)
	if err := p.cache.ZAdd(ctx, key, oredis.Z{Score: float64(publishedAt.UnixMilli()), Member: strconv.FormatUint(payload.PostID, 10)}); err != nil {
		return fmt.Errorf("write content timeline: %w", err)
	}
	if err := p.cache.ZRemRangeByRank(ctx, key, 0, -1001); err != nil {
		return fmt.Errorf("trim content timeline: %w", err)
	}
	return nil
}

type ContentTimelineWorker struct {
	channel   *amqp.Channel
	queue     string
	processor *ContentTimelineProcessor
	reliable  *ReliableDeliveryHandler
}

func NewContentTimelineWorker(channel *amqp.Channel, queue string, processor *ContentTimelineProcessor, reliable *ReliableDeliveryHandler) *ContentTimelineWorker {
	return &ContentTimelineWorker{channel: channel, queue: queue, processor: processor, reliable: reliable}
}

func (w *ContentTimelineWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.queue == "" || w.processor == nil || w.reliable == nil {
		return errors.New("content timeline worker is not initialized")
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
				return errors.New("content timeline deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.processor.Process(ctx, envelope)
			})
		}
	}
}

type ContentNotificationWorker struct {
	channel  *amqp.Channel
	queue    string
	consumer *ContentNotificationConsumer
	reliable *ReliableDeliveryHandler
}

func NewContentNotificationWorker(channel *amqp.Channel, queue string, consumer *ContentNotificationConsumer, reliable *ReliableDeliveryHandler) *ContentNotificationWorker {
	return &ContentNotificationWorker{channel: channel, queue: queue, consumer: consumer, reliable: reliable}
}

func (w *ContentNotificationWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.queue == "" || w.consumer == nil || w.reliable == nil {
		return errors.New("content notification worker is not initialized")
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
				return errors.New("content notification deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				_, err := w.consumer.Handle(ctx, envelope.EventID, envelope.EventType, envelope.Payload)
				return err
			})
		}
	}
}
