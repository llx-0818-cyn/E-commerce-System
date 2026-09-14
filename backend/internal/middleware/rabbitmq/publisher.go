package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const defaultPublisherConfirmTimeout = 5 * time.Second

var (
	ErrPublishNack             = errors.New("rabbitmq publisher confirm NACK")
	ErrPublishReturned         = errors.New("rabbitmq message returned as unroutable")
	ErrPublisherClosed         = errors.New("rabbitmq publisher channel closed before confirm")
	ErrPublisherConfirmTimeout = errors.New("rabbitmq publisher confirm timeout")
)

type deferredConfirmation interface {
	Done() <-chan struct{}
	Acked() bool
}

type confirmChannel interface {
	EnableConfirm() error
	NotifyReturn(chan amqp.Return) <-chan amqp.Return
	NotifyClose(chan *amqp.Error) <-chan *amqp.Error
	PublishDeferred(context.Context, string, string, bool, bool, amqp.Publishing) (deferredConfirmation, error)
}

type amqpConfirmChannel struct {
	channel *amqp.Channel
}

func (a amqpConfirmChannel) EnableConfirm() error {
	return a.channel.Confirm(false)
}

func (a amqpConfirmChannel) NotifyReturn(receiver chan amqp.Return) <-chan amqp.Return {
	return a.channel.NotifyReturn(receiver)
}

func (a amqpConfirmChannel) NotifyClose(receiver chan *amqp.Error) <-chan *amqp.Error {
	return a.channel.NotifyClose(receiver)
}

func (a amqpConfirmChannel) PublishDeferred(
	ctx context.Context,
	exchange string,
	routingKey string,
	mandatory bool,
	immediate bool,
	message amqp.Publishing,
) (deferredConfirmation, error) {
	return a.channel.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, mandatory, immediate, message)
}

// ConfirmPublisher 串行发布并等待每条消息的 Broker confirm。
// 串行化使 Return/Close/Confirm 与当前消息一一对应，也避免多个请求争用同一 Channel。
type ConfirmPublisher struct {
	channel        confirmChannel
	returns        <-chan amqp.Return
	closes         <-chan *amqp.Error
	confirmTimeout time.Duration
	mu             sync.Mutex
}

func NewConfirmPublisher(channel *amqp.Channel, timeout time.Duration) (*ConfirmPublisher, error) {
	if channel == nil {
		return nil, errors.New("rabbitmq publisher channel is nil")
	}
	return newConfirmPublisher(amqpConfirmChannel{channel: channel}, timeout)
}

func newConfirmPublisher(channel confirmChannel, timeout time.Duration) (*ConfirmPublisher, error) {
	if channel == nil {
		return nil, errors.New("rabbitmq confirm channel is nil")
	}
	if timeout <= 0 {
		return nil, errors.New("rabbitmq publisher confirm timeout must be positive")
	}
	if err := channel.EnableConfirm(); err != nil {
		return nil, fmt.Errorf("enable rabbitmq publisher confirm: %w", err)
	}
	return &ConfirmPublisher{
		channel:        channel,
		returns:        channel.NotifyReturn(make(chan amqp.Return, 1)),
		closes:         channel.NotifyClose(make(chan *amqp.Error, 1)),
		confirmTimeout: timeout,
	}, nil
}

func (p *ConfirmPublisher) PublishJSON(ctx context.Context, exchange, routingKey string, payload any) error {
	if p == nil || p.channel == nil {
		return errors.New("rabbitmq confirm publisher is not initialized")
	}
	if exchange == "" || routingKey == "" {
		return errors.New("exchange and routingKey are required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	messageID, err := newEventID(16)
	if err != nil {
		return fmt.Errorf("generate rabbitmq message id: %w", err)
	}
	message := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    messageID,
		Timestamp:    time.Now().UTC(),
		Type:         routingKey,
		Body:         body,
	}
	return p.publish(ctx, exchange, routingKey, message)
}

func (p *ConfirmPublisher) publish(ctx context.Context, exchange, routingKey string, message amqp.Publishing) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	waitContext := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		waitContext, cancel = context.WithTimeout(ctx, p.confirmTimeout)
	}
	defer cancel()

	confirmation, err := p.channel.PublishDeferred(waitContext, exchange, routingKey, true, false, message)
	if err != nil {
		return fmt.Errorf("publish rabbitmq message %s: %w", message.MessageId, err)
	}
	if confirmation == nil {
		return errors.New("rabbitmq confirm mode returned nil confirmation")
	}

	for {
		select {
		case returned, ok := <-p.returns:
			if !ok {
				return ErrPublisherClosed
			}
			return fmt.Errorf(
				"%w: message_id=%s code=%d reason=%s exchange=%s routing_key=%s",
				ErrPublishReturned,
				returned.MessageId,
				returned.ReplyCode,
				returned.ReplyText,
				returned.Exchange,
				returned.RoutingKey,
			)
		case closeErr, ok := <-p.closes:
			if !ok || closeErr == nil {
				return ErrPublisherClosed
			}
			return fmt.Errorf("%w: %s", ErrPublisherClosed, closeErr.Reason)
		case <-confirmation.Done():
			if !confirmation.Acked() {
				return fmt.Errorf("%w: message_id=%s", ErrPublishNack, message.MessageId)
			}
			// RabbitMQ 会先发送 basic.return 再发送 confirm；非阻塞复查避免两个
			// channel 同时就绪时误把不可路由消息视作成功。
			select {
			case returned, ok := <-p.returns:
				if !ok {
					return ErrPublisherClosed
				}
				return fmt.Errorf("%w: message_id=%s code=%d reason=%s", ErrPublishReturned, returned.MessageId, returned.ReplyCode, returned.ReplyText)
			default:
			}
			return nil
		case <-waitContext.Done():
			return fmt.Errorf("%w: message_id=%s: %v", ErrPublisherConfirmTimeout, message.MessageId, waitContext.Err())
		}
	}
}

func PublishJSON(ctx context.Context, publisher *ConfirmPublisher, exchange string, routingKey string, payload any) error {
	if publisher == nil {
		return errors.New("rabbitmq confirm publisher is nil")
	}
	return publisher.PublishJSON(ctx, exchange, routingKey, payload)
}
