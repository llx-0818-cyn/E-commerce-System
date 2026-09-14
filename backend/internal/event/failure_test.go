package event

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateFailureRecordNormalizesPayload(t *testing.T) {
	record := FailureRecord{
		ConsumerName: " timeline-consumer ",
		EventID:      "evt-1",
		EventType:    "content.published",
		Payload:      []byte(`{"post_id":42}`),
		FailureStage: "handle",
		Attempts:     4,
		LastError:    errors.New("forced failure"),
	}
	if err := validateFailureRecord(&record); err != nil {
		t.Fatalf("validateFailureRecord() error = %v", err)
	}
	if record.ConsumerName != "timeline-consumer" || record.FailedAt.IsZero() {
		t.Fatalf("normalized failure = %#v", record)
	}
}

func TestValidateFailureRecordRejectsInvalidPayload(t *testing.T) {
	record := FailureRecord{
		ConsumerName: "timeline-consumer",
		EventID:      "evt-1",
		EventType:    "content.published",
		Payload:      []byte("not-json"),
		FailureStage: "handle",
		Attempts:     4,
		LastError:    errors.New("forced failure"),
	}
	if err := validateFailureRecord(&record); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("validateFailureRecord() error = %v", err)
	}
}
