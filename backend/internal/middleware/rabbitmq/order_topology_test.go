package rabbitmq

import (
	"testing"
	"time"
)

func TestBuildOrderTopologyUsesConfiguredPaymentDelayAndIsolatedQueues(t *testing.T) {
	topology, err := BuildOrderTopology(30 * time.Minute)
	if err != nil {
		t.Fatalf("BuildOrderTopology() error = %v", err)
	}
	if topology.Exchange != OrderExchange || topology.LifecycleQueue != OrderLifecycleQueue ||
		topology.DelayQueue != OrderExpirationDelayQueue || topology.ExpirationQueue != OrderExpirationQueue ||
		topology.DelayMillis != (30*time.Minute).Milliseconds() {
		t.Fatalf("order topology = %#v", topology)
	}
	if topology.DelayQueue == topology.ExpirationQueue || topology.LifecycleQueue == topology.ExpirationQueue {
		t.Fatalf("order queues are not isolated: %#v", topology)
	}
	if _, err := BuildOrderTopology(0); err == nil {
		t.Fatal("BuildOrderTopology(0) error = nil")
	}
}
