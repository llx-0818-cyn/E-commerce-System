package event

import (
	"strings"
	"testing"
)

func TestProcessedRecordValidation(t *testing.T) {
	guard := &ConsumerGuard{consumerName: "timeline-consumer"}
	record, err := guard.processedRecord(ConsumedEvent{
		EventID:       "evt-1",
		EventType:     "content.published",
		AggregateType: "post",
		AggregateID:   "42",
	})
	if err != nil {
		t.Fatalf("processedRecord() error = %v", err)
	}
	if record.ConsumerName != "timeline-consumer" || record.EventID != "evt-1" || record.AggregateID == nil || *record.AggregateID != "42" {
		t.Fatalf("processed record = %#v", record)
	}
}

func TestProcessedRecordRejectsPartialAggregate(t *testing.T) {
	guard := &ConsumerGuard{consumerName: "timeline-consumer"}
	_, err := guard.processedRecord(ConsumedEvent{EventID: "evt-1", EventType: "content.published", AggregateType: "post"})
	if err == nil || !strings.Contains(err.Error(), "同时提供") {
		t.Fatalf("processedRecord() error = %v", err)
	}
}
