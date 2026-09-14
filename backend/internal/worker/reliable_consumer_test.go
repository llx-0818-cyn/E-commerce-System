package worker

import (
	"context"
	"errors"
	"testing"

	"feedsystem_video_go/internal/event"

	amqp "github.com/rabbitmq/amqp091-go"
)

type fakeAcknowledger struct {
	acks        int
	nacks       int
	lastRequeue bool
	err         error
}

func (f *fakeAcknowledger) Ack(_ uint64, _ bool) error {
	f.acks++
	return f.err
}

func (f *fakeAcknowledger) Nack(_ uint64, _ bool, requeue bool) error {
	f.nacks++
	f.lastRequeue = requeue
	return f.err
}

func (f *fakeAcknowledger) Reject(_ uint64, requeue bool) error {
	f.nacks++
	f.lastRequeue = requeue
	return f.err
}

type fakeFailureRecorder struct {
	records []event.FailureRecord
	err     error
}

func (f *fakeFailureRecorder) Record(_ context.Context, record event.FailureRecord) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, record)
	return nil
}

type fakeDeadLetterSink struct {
	payloads []any
	err      error
}

func (f *fakeDeadLetterSink) Publish(_ context.Context, payload any) error {
	if f.err != nil {
		return f.err
	}
	f.payloads = append(f.payloads, payload)
	return nil
}

func TestReliableDeliveryHandlerRoutesTransientFailureToRetryQueue(t *testing.T) {
	acknowledger := &fakeAcknowledger{}
	failures := &fakeFailureRecorder{}
	deadLetters := &fakeDeadLetterSink{}
	handler, err := NewReliableDeliveryHandler("content-consumer", "content.queue", 3, failures, deadLetters)
	if err != nil {
		t.Fatalf("NewReliableDeliveryHandler() error = %v", err)
	}
	delivery := failedDelivery(acknowledger, "content.queue", 2)

	err = handler.Handle(context.Background(), delivery, "handle", func(context.Context, amqp.Delivery) error {
		return errors.New("temporary database failure")
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if acknowledger.acks != 0 || acknowledger.nacks != 1 || acknowledger.lastRequeue {
		t.Fatalf("ack/nack/requeue = %d/%d/%v, want 0/1/false", acknowledger.acks, acknowledger.nacks, acknowledger.lastRequeue)
	}
	if len(failures.records) != 0 || len(deadLetters.payloads) != 0 {
		t.Fatalf("transient failure unexpectedly persisted/DLQed: %d/%d", len(failures.records), len(deadLetters.payloads))
	}
}

func TestReliableDeliveryHandlerExhaustionPersistsAndDLQsBeforeACK(t *testing.T) {
	acknowledger := &fakeAcknowledger{}
	failures := &fakeFailureRecorder{}
	deadLetters := &fakeDeadLetterSink{}
	handler, err := NewReliableDeliveryHandler("content-consumer", "content.queue", 3, failures, deadLetters)
	if err != nil {
		t.Fatalf("NewReliableDeliveryHandler() error = %v", err)
	}
	delivery := failedDelivery(acknowledger, "content.queue", 3)

	err = handler.Handle(context.Background(), delivery, "handle", func(context.Context, amqp.Delivery) error {
		return errors.New("permanent failure")
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if acknowledger.acks != 1 || acknowledger.nacks != 0 {
		t.Fatalf("ack/nack = %d/%d, want 1/0", acknowledger.acks, acknowledger.nacks)
	}
	if len(failures.records) != 1 || failures.records[0].Attempts != 4 || len(deadLetters.payloads) != 1 {
		t.Fatalf("persisted/DLQed = %#v / %#v", failures.records, deadLetters.payloads)
	}
}

func TestReliableDeliveryHandlerDoesNotSilentlyACKWhenFailureSinkFails(t *testing.T) {
	tests := []struct {
		name       string
		storeError error
		dlqError   error
	}{
		{"failure record unavailable", errors.New("mysql unavailable"), nil},
		{"DLQ confirm unavailable", nil, errors.New("broker NACK")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			acknowledger := &fakeAcknowledger{}
			failures := &fakeFailureRecorder{err: test.storeError}
			deadLetters := &fakeDeadLetterSink{err: test.dlqError}
			handler, err := NewReliableDeliveryHandler("content-consumer", "content.queue", 3, failures, deadLetters)
			if err != nil {
				t.Fatalf("NewReliableDeliveryHandler() error = %v", err)
			}
			delivery := failedDelivery(acknowledger, "content.queue", 3)
			if err := handler.Handle(context.Background(), delivery, "handle", func(context.Context, amqp.Delivery) error {
				return errors.New("permanent failure")
			}); err == nil {
				t.Fatal("Handle() error = nil")
			}
			if acknowledger.acks != 0 || acknowledger.nacks != 1 || !acknowledger.lastRequeue {
				t.Fatalf("ack/nack/requeue = %d/%d/%v, want 0/1/true", acknowledger.acks, acknowledger.nacks, acknowledger.lastRequeue)
			}
		})
	}
}

func failedDelivery(acknowledger amqp.Acknowledger, queue string, retries int64) amqp.Delivery {
	return amqp.Delivery{
		Acknowledger: acknowledger,
		DeliveryTag:  1,
		RoutingKey:   "content.published",
		MessageId:    "message-1",
		Body:         []byte(`{"event_id":"event-1","event_type":"content.published","post_id":42}`),
		Headers: amqp.Table{"x-death": []interface{}{
			amqp.Table{"queue": queue, "reason": "rejected", "count": retries},
		}},
	}
}
