package promotion

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func TestReservationLuaTimeIdempotencyLimitStockAndSuccessCodes(t *testing.T) {
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
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	notPreheated, err := store.Reserve(ctx, reservationKeys(cache, 1), reservationCommand(now, 1, "not-preheated"))
	if err != nil || notPreheated.Code != ReservationNotPreheated {
		t.Fatalf("not preheated = %#v, %v", notPreheated, err)
	}

	notStartedKeys := reservationKeys(cache, 2)
	seedReservation(t, ctx, cache, notStartedKeys, now.Add(time.Minute), now.Add(time.Hour), "enabled", 10, 2)
	notStarted, err := store.Reserve(ctx, notStartedKeys, reservationCommand(now, 1, "not-started"))
	if err != nil || notStarted.Code != ReservationNotStarted {
		t.Fatalf("not started = %#v, %v", notStarted, err)
	}
	notStartedReplay, err := store.Reserve(ctx, notStartedKeys, reservationCommand(now.Add(2*time.Minute), 1, "not-started"))
	if err != nil || notStartedReplay.Code != ReservationNotStarted || !notStartedReplay.Duplicate {
		t.Fatalf("not started replay = %#v, %v", notStartedReplay, err)
	}

	endedKeys := reservationKeys(cache, 3)
	seedReservation(t, ctx, cache, endedKeys, now.Add(-time.Hour), now.Add(-time.Minute), "enabled", 10, 2)
	ended, err := store.Reserve(ctx, endedKeys, reservationCommand(now, 1, "ended"))
	if err != nil || ended.Code != ReservationEnded {
		t.Fatalf("ended = %#v, %v", ended, err)
	}

	disabledKeys := reservationKeys(cache, 4)
	seedReservation(t, ctx, cache, disabledKeys, now.Add(-time.Minute), now.Add(time.Hour), "disabled", 10, 2)
	disabled, err := store.Reserve(ctx, disabledKeys, reservationCommand(now, 1, "disabled"))
	if err != nil || disabled.Code != ReservationDisabled {
		t.Fatalf("disabled = %#v, %v", disabled, err)
	}

	soldOutKeys := reservationKeys(cache, 5)
	seedReservation(t, ctx, cache, soldOutKeys, now.Add(-time.Minute), now.Add(time.Hour), "enabled", 0, 2)
	soldOut, err := store.Reserve(ctx, soldOutKeys, reservationCommand(now, 1, "sold-out"))
	if err != nil || soldOut.Code != ReservationSoldOut || soldOut.RemainingStock != 0 {
		t.Fatalf("sold out = %#v, %v", soldOut, err)
	}

	successKeys := reservationKeys(cache, 6)
	seedReservation(t, ctx, cache, successKeys, now.Add(-time.Minute), now.Add(time.Hour), "enabled", 2, 1)
	acceptedCommand := reservationCommand(now, 7, "accepted")
	accepted, err := store.Reserve(ctx, successKeys, acceptedCommand)
	if err != nil || accepted.Code != ReservationAccepted || accepted.RemainingStock != 1 || accepted.ReservedByBuyer != 1 {
		t.Fatalf("accepted = %#v, %v", accepted, err)
	}
	replayed, err := store.Reserve(ctx, successKeys, acceptedCommand)
	if err != nil || replayed.Code != ReservationAccepted || !replayed.Duplicate || replayed.RequestID != accepted.RequestID || replayed.ReservationToken != accepted.ReservationToken {
		t.Fatalf("accepted replay = %#v, %v", replayed, err)
	}
	limited, err := store.Reserve(ctx, successKeys, reservationCommand(now, 7, "over-limit"))
	if err != nil || limited.Code != ReservationLimitExceeded || limited.ReservedByBuyer != 1 {
		t.Fatalf("limit exceeded = %#v, %v", limited, err)
	}
	if stock, _ := raw.Get(ctx, successKeys.Stock).Int64(); stock != 1 {
		t.Fatalf("success/duplicate/limit stock = %d, want 1", stock)
	}
}

func TestReservationLuaConcurrentLastStockHasSingleWinner(t *testing.T) {
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
	now := time.Now().UTC()
	keys := reservationKeys(cache, 99)
	seedReservation(t, ctx, cache, keys, now.Add(-time.Minute), now.Add(time.Hour), "enabled", 1, 1)

	const contenders = 20
	results := make([]*ReservationResult, contenders)
	errorsByIndex := make([]error, contenders)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByIndex[index] = store.Reserve(ctx, keys, reservationCommand(now, uint64(index+1), fmt.Sprintf("race-%d", index)))
		}(index)
	}
	close(start)
	wait.Wait()
	accepted, soldOut := 0, 0
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("contender %d error = %v", index, err)
		}
		switch results[index].Code {
		case ReservationAccepted:
			accepted++
		case ReservationSoldOut:
			soldOut++
		default:
			t.Fatalf("contender %d result = %#v", index, results[index])
		}
	}
	if accepted != 1 || soldOut != contenders-1 {
		t.Fatalf("accepted/sold-out = %d/%d, want 1/%d", accepted, soldOut, contenders-1)
	}
	if stock, _ := raw.Get(ctx, keys.Stock).Int64(); stock != 0 {
		t.Fatalf("last stock = %d, want 0", stock)
	}
}

func reservationKeys(cache *rediscache.Client, campaignID uint64) ReservationKeys {
	return ReservationKeys{
		Metadata: CampaignMetadataKey(cache, campaignID, 1), Stock: CampaignStockKey(cache, campaignID, 1),
		Buyers: CampaignBuyersKey(cache, campaignID, 1), Requests: CampaignRequestsKey(cache, campaignID, 1),
		ResultPrefix: CampaignRequestResultPrefix(cache, campaignID, 1),
	}
}

func reservationCommand(now time.Time, buyerID uint64, key string) ReserveCommand {
	return ReserveCommand{
		BuyerID: buyerID, IdempotencyKey: key, Quantity: 1,
		RequestID: "request-" + key, ReservationToken: fmt.Sprintf("%064s", key),
		Now: now, TTL: time.Hour,
	}
}

func seedReservation(t *testing.T, ctx context.Context, cache *rediscache.Client, keys ReservationKeys, startsAt, endsAt time.Time, status string, stock int64, limit uint) {
	t.Helper()
	metadata, _ := json.Marshal(map[string]any{
		"campaign_id": 1, "version": 1, "sku_id": 1, "price_cent": 1000,
		"total_stock": stock, "per_user_limit": limit,
		"starts_at_ms": startsAt.UnixMilli(), "ends_at_ms": endsAt.UnixMilli(), "status": status,
	})
	if err := cache.SetBytes(ctx, keys.Metadata, metadata, time.Hour); err != nil {
		t.Fatalf("seed reservation metadata: %v", err)
	}
	if err := cache.SetBytes(ctx, keys.Stock, []byte(fmt.Sprintf("%d", stock)), time.Hour); err != nil {
		t.Fatalf("seed reservation stock: %v", err)
	}
}
