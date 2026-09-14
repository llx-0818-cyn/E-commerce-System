package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"feedsystem_video_go/internal/middleware/rabbitmq"
	orderdomain "feedsystem_video_go/internal/order"
)

func TestOrderLifecycleAndExpirationProcessors(t *testing.T) {
	recorder := &recordingOrderCreated{}
	lifecycle, err := NewOrderLifecycleProcessor(recorder)
	if err != nil {
		t.Fatalf("NewOrderLifecycleProcessor() error = %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"order_id": 11, "buyer_id": 7, "shop_id": 3, "total_cent": 9900})
	if err := lifecycle.Process(context.Background(), rabbitmq.DomainEventEnvelope{EventType: orderdomain.OrderCreatedEvent, Payload: payload}); err != nil {
		t.Fatalf("Process(order.created) error = %v", err)
	}
	if recorder.orderID != 11 || recorder.buyerID != 7 || recorder.shopID != 3 || recorder.total != 9900 {
		t.Fatalf("recorded order = %#v", recorder)
	}

	expirationService := &recordingOrderExpiration{}
	expiration, err := NewOrderExpirationProcessor(expirationService)
	if err != nil {
		t.Fatalf("NewOrderExpirationProcessor() error = %v", err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	expiration.now = func() time.Time { return now }
	expirePayload, _ := json.Marshal(map[string]any{"order_id": 11})
	if err := expiration.Process(context.Background(), rabbitmq.DomainEventEnvelope{EventType: orderdomain.OrderExpireRequestedEvent, Payload: expirePayload}); err != nil {
		t.Fatalf("Process(order.expire.requested) error = %v", err)
	}
	if expirationService.orderID != 11 || !expirationService.at.Equal(now) {
		t.Fatalf("expiration call = %#v", expirationService)
	}
	if err := expiration.Process(context.Background(), rabbitmq.DomainEventEnvelope{EventType: "order.unknown", Payload: expirePayload}); err == nil {
		t.Fatal("Process(order.unknown) error = nil")
	}
}

type recordingOrderCreated struct {
	buyerID uint64
	orderID uint64
	shopID  uint64
	total   int64
}

func (r *recordingOrderCreated) RecordOrderCreated(_ context.Context, buyerID, orderID, shopID uint64, total int64) error {
	r.buyerID, r.orderID, r.shopID, r.total = buyerID, orderID, shopID, total
	return nil
}

type recordingOrderExpiration struct {
	orderID uint64
	at      time.Time
}

func (r *recordingOrderExpiration) ExpireOrder(_ context.Context, orderID uint64, at time.Time) (*orderdomain.Order, error) {
	r.orderID, r.at = orderID, at
	return &orderdomain.Order{ID: orderID, Status: orderdomain.StatusCanceled}, nil
}
