package promotion

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func TestCompensationLuaRestoresReservationExactlyOnceAndCapsTotalStock(t *testing.T) {
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	raw := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer raw.Close()
	cache := rediscache.NewClient(raw, "test:")
	store, _ := NewLuaReservationStore(cache)
	ctx := context.Background()
	keys := reservationKeys(cache, 200)
	metadata, _ := json.Marshal(map[string]any{"total_stock": 1, "status": "enabled"})
	if err := cache.SetBytes(ctx, keys.Metadata, metadata, time.Hour); err != nil {
		t.Fatalf("seed compensation metadata: %v", err)
	}
	if err := cache.SetBytes(ctx, keys.Stock, []byte("0"), time.Hour); err != nil {
		t.Fatalf("seed compensation stock: %v", err)
	}
	if err := raw.HSet(ctx, keys.Buyers, "7", 1).Err(); err != nil {
		t.Fatalf("seed buyer reservation: %v", err)
	}
	request := ReservationResult{
		Code: ReservationAccepted, RequestID: "c200-v1-request", ReservationToken: "token-1",
		BuyerID: 7, Quantity: 1, RemainingStock: 0, ReservedByBuyer: 1,
	}
	requestJSON, _ := json.Marshal(request)
	if err := raw.HSet(ctx, keys.Requests, "7:idem-1", requestJSON).Err(); err != nil {
		t.Fatalf("seed accepted request: %v", err)
	}
	command := CompensationCommand{
		BuyerID: 7, IdempotencyKey: "idem-1", RequestID: request.RequestID,
		ReservationToken: request.ReservationToken, Quantity: 1,
		Reason: "order creation failed", TTL: time.Hour,
	}
	first, err := store.Compensate(ctx, keys, command)
	if err != nil || first.Code != CompensationApplied || first.RemainingStock != 1 || first.ReservedByBuyer != 0 {
		t.Fatalf("Compensate(first) = %#v, %v", first, err)
	}
	repeated, err := store.Compensate(ctx, keys, command)
	if err != nil || repeated.Code != CompensationDuplicate || repeated.RemainingStock != 1 {
		t.Fatalf("Compensate(repeat) = %#v, %v", repeated, err)
	}
	if stock, _ := raw.Get(ctx, keys.Stock).Int64(); stock != 1 {
		t.Fatalf("compensated stock = %d, want capped total 1", stock)
	}
	if reserved, _ := raw.HGet(ctx, keys.Buyers, "7").Int64(); reserved != 0 {
		t.Fatalf("compensated buyer reservation = %d, want 0", reserved)
	}
}
