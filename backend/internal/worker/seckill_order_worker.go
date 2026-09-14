package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"feedsystem_video_go/internal/middleware/rabbitmq"
	promotiondomain "feedsystem_video_go/internal/promotion"

	amqp "github.com/rabbitmq/amqp091-go"
)

type SeckillOrderProcessor interface {
	ProcessOrderRequest(context.Context, string, []byte) error
}

type SeckillCompensationProcessor interface {
	ProcessCompensation(context.Context, string, []byte) error
}

type SeckillOrderLifecycleProcessor interface {
	ProcessOrderLifecycle(context.Context, string, string, []byte) error
}

type SeckillOrderWorker struct {
	channel   *amqp.Channel
	processor SeckillOrderProcessor
	reliable  *ReliableDeliveryHandler
}

type SeckillCompensationWorker struct {
	channel   *amqp.Channel
	processor SeckillCompensationProcessor
	reliable  *ReliableDeliveryHandler
}

func NewSeckillCompensationWorker(channel *amqp.Channel, processor SeckillCompensationProcessor, reliable *ReliableDeliveryHandler) *SeckillCompensationWorker {
	return &SeckillCompensationWorker{channel: channel, processor: processor, reliable: reliable}
}

func (w *SeckillCompensationWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.processor == nil || w.reliable == nil {
		return errors.New("seckill compensation worker is not initialized")
	}
	deliveries, err := w.channel.Consume(rabbitmq.SeckillCompensationQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("seckill compensation deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "compensate", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				if envelope.EventType != promotiondomain.SeckillOrderFailedEvent {
					return fmt.Errorf("unsupported seckill compensation event %q", envelope.EventType)
				}
				return w.processor.ProcessCompensation(ctx, envelope.EventID, envelope.Payload)
			})
		}
	}
}

type SeckillOrderLifecycleWorker struct {
	channel   *amqp.Channel
	processor SeckillOrderLifecycleProcessor
	reliable  *ReliableDeliveryHandler
}

func NewSeckillOrderLifecycleWorker(channel *amqp.Channel, processor SeckillOrderLifecycleProcessor, reliable *ReliableDeliveryHandler) *SeckillOrderLifecycleWorker {
	return &SeckillOrderLifecycleWorker{channel: channel, processor: processor, reliable: reliable}
}

func (w *SeckillOrderLifecycleWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.processor == nil || w.reliable == nil {
		return errors.New("seckill order lifecycle worker is not initialized")
	}
	deliveries, err := w.channel.Consume(rabbitmq.SeckillLifecycleQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("seckill order lifecycle deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "lifecycle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				return w.processor.ProcessOrderLifecycle(ctx, envelope.EventID, envelope.EventType, envelope.Payload)
			})
		}
	}
}

func NewSeckillOrderWorker(channel *amqp.Channel, processor SeckillOrderProcessor, reliable *ReliableDeliveryHandler) *SeckillOrderWorker {
	return &SeckillOrderWorker{channel: channel, processor: processor, reliable: reliable}
}

func (w *SeckillOrderWorker) Run(ctx context.Context) error {
	if w == nil || w.channel == nil || w.processor == nil || w.reliable == nil {
		return errors.New("seckill order worker is not initialized")
	}
	deliveries, err := w.channel.Consume(rabbitmq.SeckillOrderQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("seckill order deliveries closed")
			}
			_ = w.reliable.Handle(ctx, delivery, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
				var envelope rabbitmq.DomainEventEnvelope
				if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
					return err
				}
				if envelope.EventType != promotiondomain.SeckillOrderRequestedEvent {
					return fmt.Errorf("unsupported seckill order event %q", envelope.EventType)
				}
				return w.processor.ProcessOrderRequest(ctx, envelope.EventID, envelope.Payload)
			})
		}
	}
}
