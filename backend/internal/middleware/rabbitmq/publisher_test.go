package rabbitmq

import (
	"context"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type fakeDeferredConfirmation struct {
	done chan struct{}
	ack  bool
}

func completedConfirmation(ack bool) *fakeDeferredConfirmation {
	done := make(chan struct{})
	close(done)
	return &fakeDeferredConfirmation{done: done, ack: ack}
}

func pendingConfirmation() *fakeDeferredConfirmation {
	return &fakeDeferredConfirmation{done: make(chan struct{})}
}

func (f *fakeDeferredConfirmation) Done() <-chan struct{} { return f.done }
func (f *fakeDeferredConfirmation) Acked() bool           { return f.ack }

type fakeConfirmChannel struct {
	confirmErr    error
	publishErr    error
	confirmation  deferredConfirmation
	returnOnSend  *amqp.Return
	closeOnSend   *amqp.Error
	returns       chan amqp.Return
	closes        chan *amqp.Error
	confirmCalled bool
	exchange      string
	routingKey    string
	mandatory     bool
	immediate     bool
	message       amqp.Publishing
}

func (f *fakeConfirmChannel) EnableConfirm() error {
	f.confirmCalled = true
	return f.confirmErr
}

func (f *fakeConfirmChannel) NotifyReturn(receiver chan amqp.Return) <-chan amqp.Return {
	f.returns = receiver
	return receiver
}

func (f *fakeConfirmChannel) NotifyClose(receiver chan *amqp.Error) <-chan *amqp.Error {
	f.closes = receiver
	return receiver
}

func (f *fakeConfirmChannel) PublishDeferred(
	_ context.Context,
	exchange string,
	routingKey string,
	mandatory bool,
	immediate bool,
	message amqp.Publishing,
) (deferredConfirmation, error) {
	f.exchange = exchange
	f.routingKey = routingKey
	f.mandatory = mandatory
	f.immediate = immediate
	f.message = message
	if f.returnOnSend != nil {
		returned := *f.returnOnSend
		returned.MessageId = message.MessageId
		returned.Exchange = exchange
		returned.RoutingKey = routingKey
		f.returns <- returned
	}
	if f.closeOnSend != nil {
		f.closes <- f.closeOnSend
	}
	return f.confirmation, f.publishErr
}

func TestConfirmPublisherACKMarksPublishSuccessful(t *testing.T) {
	channel := &fakeConfirmChannel{confirmation: completedConfirmation(true)}
	publisher, err := newConfirmPublisher(channel, time.Second)
	if err != nil {
		t.Fatalf("newConfirmPublisher() error = %v", err)
	}

	completed := false
	if err := publisher.PublishJSON(context.Background(), "content.events", "content.published", map[string]any{"post_id": 42}); err != nil {
		t.Fatalf("PublishJSON() error = %v", err)
	} else {
		completed = true
	}
	if !completed {
		t.Fatal("ACKed publish was not marked successful")
	}
	if !channel.confirmCalled || !channel.mandatory || channel.immediate {
		t.Fatalf("confirm/mandatory/immediate = %v/%v/%v", channel.confirmCalled, channel.mandatory, channel.immediate)
	}
	if channel.message.DeliveryMode != amqp.Persistent {
		t.Fatalf("DeliveryMode = %d, want Persistent", channel.message.DeliveryMode)
	}
	if channel.message.ContentType != "application/json" || channel.message.MessageId == "" || channel.message.Type != "content.published" {
		t.Fatalf("publishing properties = %#v", channel.message)
	}
}

func TestConfirmPublisherNACKDoesNotMarkPublishSuccessful(t *testing.T) {
	channel := &fakeConfirmChannel{confirmation: completedConfirmation(false)}
	publisher, err := newConfirmPublisher(channel, time.Second)
	if err != nil {
		t.Fatalf("newConfirmPublisher() error = %v", err)
	}

	publishErr := publisher.PublishJSON(context.Background(), "content.events", "content.published", map[string]any{"post_id": 42})
	completed := publishErr == nil
	if completed || !errors.Is(publishErr, ErrPublishNack) {
		t.Fatalf("NACK publish completed=%v error=%v, want incomplete ErrPublishNack", completed, publishErr)
	}
}

func TestConfirmPublisherConnectionCloseDoesNotMarkPublishSuccessful(t *testing.T) {
	channel := &fakeConfirmChannel{
		confirmation: pendingConfirmation(),
		closeOnSend:  &amqp.Error{Code: 320, Reason: "CONNECTION_FORCED"},
	}
	publisher, err := newConfirmPublisher(channel, time.Second)
	if err != nil {
		t.Fatalf("newConfirmPublisher() error = %v", err)
	}

	publishErr := publisher.PublishJSON(context.Background(), "content.events", "content.published", map[string]any{"post_id": 42})
	completed := publishErr == nil
	if completed || !errors.Is(publishErr, ErrPublisherClosed) {
		t.Fatalf("closed publish completed=%v error=%v, want incomplete ErrPublisherClosed", completed, publishErr)
	}
}

func TestConfirmPublisherRejectsReturnedMessageEvenWhenACKed(t *testing.T) {
	channel := &fakeConfirmChannel{
		confirmation: completedConfirmation(true),
		returnOnSend: &amqp.Return{ReplyCode: 312, ReplyText: "NO_ROUTE"},
	}
	publisher, err := newConfirmPublisher(channel, time.Second)
	if err != nil {
		t.Fatalf("newConfirmPublisher() error = %v", err)
	}

	publishErr := publisher.PublishJSON(context.Background(), "content.events", "content.unknown", map[string]any{"post_id": 42})
	if !errors.Is(publishErr, ErrPublishReturned) {
		t.Fatalf("returned publish error = %v, want ErrPublishReturned", publishErr)
	}
}

func TestConfirmPublisherTimesOutWithoutConfirmation(t *testing.T) {
	channel := &fakeConfirmChannel{confirmation: pendingConfirmation()}
	publisher, err := newConfirmPublisher(channel, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("newConfirmPublisher() error = %v", err)
	}

	publishErr := publisher.PublishJSON(context.Background(), "content.events", "content.published", map[string]any{"post_id": 42})
	if !errors.Is(publishErr, ErrPublisherConfirmTimeout) {
		t.Fatalf("timeout publish error = %v, want ErrPublisherConfirmTimeout", publishErr)
	}
}

func TestConfirmPublisherRequiresConfirmMode(t *testing.T) {
	want := errors.New("confirm.select failed")
	_, err := newConfirmPublisher(&fakeConfirmChannel{confirmErr: want}, time.Second)
	if !errors.Is(err, want) {
		t.Fatalf("newConfirmPublisher() error = %v, want wrapped %v", err, want)
	}
}
