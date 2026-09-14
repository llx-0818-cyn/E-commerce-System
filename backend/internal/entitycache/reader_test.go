package entitycache

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

type cachedProbe struct {
	ID    uint64 `json:"id"`
	Label string `json:"label"`
}

func TestUnifiedEntityKeys(t *testing.T) {
	cache := rediscache.NewClient(nil, "v1:")
	tests := map[string]string{
		ContentEntityKey(cache, 11):     "v1:content:entity:11",
		ShopSummaryKey(cache, 12):       "v1:shop:summary:12",
		ProductDetailKey(cache, 13):     "v1:product:detail:13",
		ProductSKUKey(cache, 14):        "v1:product:sku:14",
		ProductInventoryKey(cache, 15):  "v1:product:inventory:15",
		ProductPriceRangeKey(cache, 16): "v1:product:price-range:16",
	}
	for got, want := range tests {
		if got != want {
			t.Fatalf("entity key = %q, want %q", got, want)
		}
	}
}

func TestReaderUsesL1L2L3AndPreservesRequestedOrder(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer raw.Close()
	redisClient := rediscache.NewClient(raw, "v1:")

	available := map[uint64]cachedProbe{
		1: {ID: 1, Label: "from-l3-then-l1"},
		3: {ID: 3, Label: "from-l3"},
	}
	var mu sync.Mutex
	loaderCalls := make([][]uint64, 0, 2)
	reader, err := NewReader(redisClient, func(id uint64) string {
		return ProductDetailKey(redisClient, id)
	}, func(_ context.Context, ids []uint64) (map[uint64]cachedProbe, error) {
		mu.Lock()
		loaderCalls = append(loaderCalls, append([]uint64(nil), ids...))
		mu.Unlock()
		result := make(map[uint64]cachedProbe, len(ids))
		for _, id := range ids {
			if value, ok := available[id]; ok {
				result[id] = value
			}
		}
		return result, nil
	}, Options{})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}

	if _, found, err := reader.Get(context.Background(), 1); err != nil || !found {
		t.Fatalf("prime L1 Get() = found %v, err %v", found, err)
	}
	if err := redisClient.Del(context.Background(), ProductDetailKey(redisClient, 1)); err != nil {
		t.Fatalf("remove L2 copy for L1 proof: %v", err)
	}
	payload, _ := json.Marshal(cachedProbe{ID: 2, Label: "from-l2"})
	if err := redisClient.SetBytes(context.Background(), ProductDetailKey(redisClient, 2), payload, defaultL2TTL); err != nil {
		t.Fatalf("seed L2: %v", err)
	}

	values, err := reader.GetMany(context.Background(), []uint64{1, 2, 3, 1, 4})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	gotIDs := make([]uint64, len(values))
	for index := range values {
		gotIDs[index] = values[index].ID
	}
	if want := []uint64{1, 2, 3, 1}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("ordered IDs = %v, want %v", gotIDs, want)
	}
	if want := [][]uint64{{1}, {3, 4}}; !reflect.DeepEqual(loaderCalls, want) {
		t.Fatalf("L3 loader calls = %v, want %v", loaderCalls, want)
	}
}

func TestReaderCachesHighFrequencyMissingEntity(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer raw.Close()
	redisClient := rediscache.NewClient(raw, "v1:")
	var loads atomic.Int64
	reader, err := NewReader(redisClient, func(id uint64) string {
		return ProductDetailKey(redisClient, id)
	}, func(_ context.Context, _ []uint64) (map[uint64]cachedProbe, error) {
		loads.Add(1)
		time.Sleep(30 * time.Millisecond)
		return map[uint64]cachedProbe{}, nil
	}, Options{NullTTL: 20 * time.Second, TTLJitter: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < 50; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if _, found, getErr := reader.Get(context.Background(), 404); getErr != nil || found {
				t.Errorf("Get(missing) = found %v, err %v", found, getErr)
			}
		}()
	}
	close(start)
	wait.Wait()
	for index := 0; index < 20; index++ {
		if _, found, getErr := reader.Get(context.Background(), 404); getErr != nil || found {
			t.Fatalf("cached Get(missing) = found %v, err %v", found, getErr)
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("missing entity loader calls = %d, want 1", got)
	}
	key := ProductDetailKey(redisClient, 404)
	if value, err := raw.Get(context.Background(), key).Result(); err != nil || value != nullPayload {
		t.Fatalf("null cache = %q, %v", value, err)
	}
	if ttl := mr.TTL(key); ttl < 20*time.Second || ttl > 25*time.Second {
		t.Fatalf("null cache TTL = %s, want 20s..25s", ttl)
	}
}

func TestReadersUseRedisShortLockAfterHotKeyExpiry(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer raw.Close()
	redisClient := rediscache.NewClient(raw, "v1:")
	var loads atomic.Int64
	loader := func(_ context.Context, ids []uint64) (map[uint64]cachedProbe, error) {
		loads.Add(1)
		time.Sleep(60 * time.Millisecond)
		return map[uint64]cachedProbe{ids[0]: {ID: ids[0], Label: "rebuilt"}}, nil
	}
	options := Options{LockTTL: time.Second, LockWait: 500 * time.Millisecond}
	readerOne, _ := NewReader(redisClient, func(id uint64) string { return ProductDetailKey(redisClient, id) }, loader, options)
	readerTwo, _ := NewReader(redisClient, func(id uint64) string { return ProductDetailKey(redisClient, id) }, loader, options)

	start := make(chan struct{})
	results := make([]cachedProbe, 2)
	errorsByIndex := make([]error, 2)
	var wait sync.WaitGroup
	for index, reader := range []*Reader[cachedProbe]{readerOne, readerTwo} {
		wait.Add(1)
		go func(index int, reader *Reader[cachedProbe]) {
			defer wait.Done()
			<-start
			value, found, getErr := reader.Get(context.Background(), 7)
			if getErr == nil && !found {
				getErr = errors.New("hot entity was not found")
			}
			results[index], errorsByIndex[index] = value, getErr
		}(index, reader)
	}
	close(start)
	wait.Wait()
	for index, getErr := range errorsByIndex {
		if getErr != nil || results[index].ID != 7 {
			t.Fatalf("reader %d result = %#v, %v", index, results[index], getErr)
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("cross-reader loader calls = %d, want 1", got)
	}
}

func TestTTLJitterStaysBoundedAndVaries(t *testing.T) {
	values := make(map[time.Duration]struct{})
	for index := 0; index < 20; index++ {
		value := ttlWithJitter(time.Minute, 10*time.Second)
		if value < time.Minute || value > 70*time.Second {
			t.Fatalf("jittered TTL = %s, want 60s..70s", value)
		}
		values[value] = struct{}{}
	}
	if len(values) < 2 {
		t.Fatalf("jittered TTLs did not vary: %v", values)
	}
}

func TestReaderFallsBackDuringRedisFailureAndBackfillsAfterRecovery(t *testing.T) {
	first, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	address := first.Addr()
	raw := goredis.NewClient(&goredis.Options{Addr: address, MaxRetries: 0})
	defer raw.Close()
	redisClient := rediscache.NewClient(raw, "v1:")
	var loads atomic.Int64
	reader, err := NewReader(redisClient, func(id uint64) string {
		return ContentEntityKey(redisClient, id)
	}, func(_ context.Context, ids []uint64) (map[uint64]cachedProbe, error) {
		loads.Add(1)
		return map[uint64]cachedProbe{ids[0]: {ID: ids[0], Label: "database"}}, nil
	}, Options{})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}

	first.Close()
	failureCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	value, found, err := reader.Get(failureCtx, 9)
	if err != nil || !found || value.Label != "database" {
		t.Fatalf("Get() while Redis is down = %#v, found %v, err %v", value, found, err)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("fallback loader calls = %d, want 1", got)
	}

	recovered := miniredis.NewMiniRedis()
	if err := recovered.StartAddr(address); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
	defer recovered.Close()
	value, found, err = reader.Get(context.Background(), 9)
	if err != nil || !found || value.Label != "database" {
		t.Fatalf("Get() after Redis recovery = %#v, found %v, err %v", value, found, err)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("recovery unexpectedly reloaded DB %d times", got)
	}
	if exists := recovered.Exists(ContentEntityKey(redisClient, 9)); !exists {
		t.Fatal("recovered Redis was not backfilled from L1")
	}
}
