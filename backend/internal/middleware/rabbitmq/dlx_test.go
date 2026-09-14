package rabbitmq

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestBuildRetryTopologyUsesIsolatedRetryAndFinalDLQ(t *testing.T) {
	topology, err := BuildRetryTopology("content.events", "content.timeline", "content.*")
	if err != nil {
		t.Fatalf("BuildRetryTopology() error = %v", err)
	}
	if topology.RetryExchange != "content.timeline.retry.exchange" || topology.RetryQueue != "content.timeline.retry" {
		t.Fatalf("retry topology = %#v", topology)
	}
	if topology.DeadLetterQueue != "content.timeline.dlq" || topology.DeadLetterKey != "content.timeline.failed" {
		t.Fatalf("dead-letter topology = %#v", topology)
	}
}

func TestGetRetryCountForQueueSelectsRejectedSourceDeath(t *testing.T) {
	delivery := amqp.Delivery{Headers: amqp.Table{
		"x-death": []interface{}{
			amqp.Table{"queue": "content.timeline.retry", "reason": "expired", "count": int64(3)},
			amqp.Table{"queue": "content.timeline", "reason": "rejected", "count": int64(2)},
			amqp.Table{"queue": "notification.content", "reason": "rejected", "count": int64(9)},
		},
	}}
	if got := GetRetryCountForQueue(delivery, "content.timeline"); got != 2 {
		t.Fatalf("GetRetryCountForQueue() = %d, want 2", got)
	}
}
