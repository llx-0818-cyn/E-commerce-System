package event

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPrepareOutboxEventGeneratesStableEnvelope(t *testing.T) {
	availableAt := time.Date(2026, time.August, 24, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60))
	record, err := prepareOutboxEvent(DomainEvent{
		AggregateType: "post",
		AggregateID:   "42",
		EventType:     "content.published",
		Payload:       map[string]any{"post_id": 42},
		AvailableAt:   availableAt,
	}, func() (string, error) { return "event-fixed-1", nil })
	if err != nil {
		t.Fatalf("prepareOutboxEvent() error = %v", err)
	}
	if record.EventID != "event-fixed-1" || record.Status != OutboxStatusPending || record.Attempts != 0 {
		t.Fatalf("record = %#v", record)
	}
	if record.PayloadJSON != `{"post_id":42}` {
		t.Fatalf("payload = %s", record.PayloadJSON)
	}
	if !record.NextAttemptAt.Equal(availableAt.UTC()) {
		t.Fatalf("next attempt = %s, want %s", record.NextAttemptAt, availableAt.UTC())
	}
}

func TestPrepareOutboxEventRejectsInvalidEnvelope(t *testing.T) {
	tests := []struct {
		name  string
		event DomainEvent
		want  string
	}{
		{"invalid aggregate type", DomainEvent{AggregateType: "Post Type", AggregateID: "1", EventType: "content.published", Payload: map[string]int{}}, "aggregate_type"},
		{"missing aggregate id", DomainEvent{AggregateType: "post", EventType: "content.published", Payload: map[string]int{}}, "aggregate_id"},
		{"invalid event type", DomainEvent{AggregateType: "post", AggregateID: "1", EventType: "Content Published", Payload: map[string]int{}}, "event_type"},
		{"missing payload", DomainEvent{AggregateType: "post", AggregateID: "1", EventType: "content.published"}, "payload"},
		{"unserializable payload", DomainEvent{AggregateType: "post", AggregateID: "1", EventType: "content.published", Payload: func() {}}, "序列化"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareOutboxEvent(test.event, func() (string, error) { return "event-1", nil })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("prepareOutboxEvent() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrepareOutboxEventPropagatesIDGenerationFailure(t *testing.T) {
	want := errors.New("entropy unavailable")
	_, err := prepareOutboxEvent(DomainEvent{
		AggregateType: "post",
		AggregateID:   "1",
		EventType:     "content.published",
		Payload:       map[string]int{},
	}, func() (string, error) { return "", want })
	if !errors.Is(err, want) {
		t.Fatalf("prepareOutboxEvent() error = %v, want wrapped %v", err, want)
	}
}
