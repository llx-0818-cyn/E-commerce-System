package entitycache

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"

	localcache "github.com/patrickmn/go-cache"
	"golang.org/x/sync/singleflight"
)

const (
	defaultL1TTL        = 5 * time.Second
	defaultL2TTL        = time.Hour
	defaultNullTTL      = 30 * time.Second
	defaultLockTTL      = 2 * time.Second
	defaultLockWait     = 150 * time.Millisecond
	defaultRedisTimeout = 50 * time.Millisecond
	nullPayload         = "__entity_cache_null_v1__"
)

type Loader[T any] func(context.Context, []uint64) (map[uint64]T, error)

type KeyBuilder func(uint64) string

type Options struct {
	L1TTL        time.Duration
	L2TTL        time.Duration
	NullTTL      time.Duration
	TTLJitter    time.Duration
	LockTTL      time.Duration
	LockWait     time.Duration
	RedisTimeout time.Duration
}

type Reader[T any] struct {
	redis        *rediscache.Client
	local        *localcache.Cache
	key          KeyBuilder
	load         Loader[T]
	loads        singleflight.Group
	pending      sync.Map
	l1TTL        time.Duration
	l2TTL        time.Duration
	nullTTL      time.Duration
	ttlJitter    time.Duration
	lockTTL      time.Duration
	lockWait     time.Duration
	redisTimeout time.Duration
}

func NewReader[T any](redis *rediscache.Client, key KeyBuilder, load Loader[T], options Options) (*Reader[T], error) {
	if key == nil || load == nil {
		return nil, errors.New("entity cache key builder and loader are required")
	}
	if options.L1TTL <= 0 {
		options.L1TTL = defaultL1TTL
	}
	if options.L2TTL <= 0 {
		options.L2TTL = defaultL2TTL
	}
	if options.NullTTL <= 0 {
		options.NullTTL = defaultNullTTL
	}
	if options.TTLJitter <= 0 {
		options.TTLJitter = options.L2TTL / 10
	}
	if options.LockTTL <= 0 {
		options.LockTTL = defaultLockTTL
	}
	if options.LockWait <= 0 {
		options.LockWait = defaultLockWait
	}
	if options.RedisTimeout <= 0 {
		options.RedisTimeout = defaultRedisTimeout
	}
	return &Reader[T]{
		redis:        redis,
		local:        localcache.New(options.L1TTL, options.L1TTL),
		key:          key,
		load:         load,
		l1TTL:        options.L1TTL,
		l2TTL:        options.L2TTL,
		nullTTL:      options.NullTTL,
		ttlJitter:    options.TTLJitter,
		lockTTL:      options.LockTTL,
		lockWait:     options.LockWait,
		redisTimeout: options.RedisTimeout,
	}, nil
}

func (r *Reader[T]) Get(ctx context.Context, id uint64) (T, bool, error) {
	var zero T
	values, err := r.GetMany(ctx, []uint64{id})
	if err != nil {
		return zero, false, err
	}
	if len(values) == 0 {
		return zero, false, nil
	}
	return values[0], true, nil
}

func (r *Reader[T]) GetMany(ctx context.Context, ids []uint64) ([]T, error) {
	if len(ids) == 0 {
		return []T{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	values, unresolved := r.readCaches(ctx, ids)
	if len(unresolved) > 0 {
		loaded, err := r.loadThrough(ctx, unresolved)
		if err != nil {
			return nil, err
		}
		for id, value := range loaded {
			values[id] = value
		}
	}
	ordered := make([]T, 0, len(ids))
	for _, id := range ids {
		if value, ok := values[id]; ok {
			ordered = append(ordered, value)
		}
	}
	return ordered, nil
}

func (r *Reader[T]) Delete(ctx context.Context, ids ...uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var result error
	for _, id := range uniqueIDs(ids) {
		key := r.key(id)
		if r.local != nil {
			r.local.Delete(key)
		}
		r.pending.Delete(key)
		if r.redis != nil {
			operationCtx, cancel := r.redisOperationContext(ctx)
			result = errors.Join(result, r.redis.Del(operationCtx, key))
			cancel()
		}
	}
	return result
}

func (r *Reader[T]) readCaches(ctx context.Context, ids []uint64) (map[uint64]T, []uint64) {
	values := make(map[uint64]T, len(ids))
	missedL1 := make([]uint64, 0, len(ids))
	for _, id := range uniqueIDs(ids) {
		key := r.key(id)
		if payload, ok := r.localPayload(key); ok {
			r.backfillRedis(ctx, key)
			if isNullPayload(payload) {
				continue
			}
			var value T
			if json.Unmarshal(payload, &value) == nil {
				values[id] = value
				continue
			}
			r.local.Delete(key)
		}
		missedL1 = append(missedL1, id)
	}
	return values, r.loadRedis(ctx, missedL1, values)
}

func (r *Reader[T]) loadRedis(ctx context.Context, ids []uint64, values map[uint64]T) []uint64 {
	if len(ids) == 0 || r.redis == nil {
		return ids
	}
	keys := make([]string, len(ids))
	for index, id := range ids {
		keys[index] = r.key(id)
	}
	operationCtx, cancel := r.redisOperationContext(ctx)
	results, err := r.redis.MGet(operationCtx, keys...)
	cancel()
	if err != nil || len(results) != len(ids) {
		return ids
	}
	missed := make([]uint64, 0, len(ids))
	for index, result := range results {
		payload, ok := redisPayload(result)
		if !ok {
			missed = append(missed, ids[index])
			continue
		}
		if isNullPayload(payload) {
			r.setLocal(keys[index], payload, r.nullTTL)
			continue
		}
		var value T
		if err := json.Unmarshal(payload, &value); err != nil {
			missed = append(missed, ids[index])
			continue
		}
		values[ids[index]] = value
		r.pending.Delete(keys[index])
		r.setLocal(keys[index], payload, r.l1TTL)
	}
	return missed
}

func (r *Reader[T]) loadThrough(ctx context.Context, ids []uint64) (map[uint64]T, error) {
	flightKey, lockKey := r.loadKeys(ids)
	shared, err, _ := r.loads.Do(flightKey, func() (any, error) {
		values, unresolved := r.readCaches(ctx, ids)
		if len(unresolved) == 0 {
			return values, nil
		}

		var token string
		locked := false
		lockAvailable := true
		if r.redis != nil {
			var lockErr error
			operationCtx, cancel := r.redisOperationContext(ctx)
			token, locked, lockErr = r.redis.Lock(operationCtx, lockKey, r.lockTTL)
			cancel()
			if lockErr != nil {
				locked = false
				lockAvailable = false
			}
		}
		if locked {
			defer r.unlock(lockKey, token)
		} else if r.redis != nil && lockAvailable {
			waited, stillMissing := r.waitForCache(ctx, unresolved)
			for id, value := range waited {
				values[id] = value
			}
			unresolved = stillMissing
			if len(unresolved) == 0 {
				return values, nil
			}
		}

		loaded, loadErr := r.load(ctx, unresolved)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, id := range unresolved {
			value, found := loaded[id]
			if !found {
				r.cacheNull(ctx, id)
				continue
			}
			values[id] = value
			r.cacheValue(ctx, id, value)
		}
		return values, nil
	})
	if err != nil {
		return nil, err
	}
	return shared.(map[uint64]T), nil
}

func (r *Reader[T]) waitForCache(ctx context.Context, ids []uint64) (map[uint64]T, []uint64) {
	deadline := time.NewTimer(r.lockWait)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return map[uint64]T{}, ids
		case <-deadline.C:
			return map[uint64]T{}, ids
		case <-ticker.C:
			values, unresolved := r.readCaches(ctx, ids)
			if len(unresolved) == 0 {
				return values, nil
			}
		}
	}
}

func (r *Reader[T]) cacheValue(ctx context.Context, id uint64, value T) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	key := r.key(id)
	r.setLocal(key, payload, r.l1TTL)
	if r.redis != nil {
		ttl := ttlWithJitter(r.l2TTL, r.ttlJitter)
		operationCtx, cancel := r.redisOperationContext(ctx)
		err := r.redis.SetBytes(operationCtx, key, payload, ttl)
		cancel()
		if err != nil {
			r.pending.Store(key, pendingBackfill{payload: append([]byte(nil), payload...), ttl: ttl})
		} else {
			r.pending.Delete(key)
		}
	}
}

func (r *Reader[T]) cacheNull(ctx context.Context, id uint64) {
	payload := []byte(nullPayload)
	key := r.key(id)
	r.setLocal(key, payload, r.nullTTL)
	if r.redis != nil {
		nullJitter := r.ttlJitter
		if maximum := r.nullTTL / 10; nullJitter > maximum {
			nullJitter = maximum
		}
		ttl := ttlWithJitter(r.nullTTL, nullJitter)
		operationCtx, cancel := r.redisOperationContext(ctx)
		err := r.redis.SetBytes(operationCtx, key, payload, ttl)
		cancel()
		if err != nil {
			r.pending.Store(key, pendingBackfill{payload: append([]byte(nil), payload...), ttl: ttl})
		} else {
			r.pending.Delete(key)
		}
	}
}

type pendingBackfill struct {
	payload []byte
	ttl     time.Duration
}

func (r *Reader[T]) backfillRedis(ctx context.Context, key string) {
	if r.redis == nil {
		return
	}
	value, ok := r.pending.Load(key)
	if !ok {
		return
	}
	pending, ok := value.(pendingBackfill)
	if !ok {
		r.pending.Delete(key)
		return
	}
	operationCtx, cancel := r.redisOperationContext(ctx)
	err := r.redis.SetBytes(operationCtx, key, pending.payload, pending.ttl)
	cancel()
	if err == nil {
		r.pending.Delete(key)
	}
}

func (r *Reader[T]) redisOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, r.redisTimeout)
}

func (r *Reader[T]) loadKeys(ids []uint64) (string, string) {
	ordered := append([]uint64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	var raw strings.Builder
	for _, id := range ordered {
		raw.WriteString(strconv.FormatUint(id, 10))
		raw.WriteByte(',')
	}
	sum := sha256.Sum256([]byte(raw.String()))
	digest := hex.EncodeToString(sum[:8])
	if len(ordered) == 1 {
		return digest, r.key(ordered[0]) + ":load-lock"
	}
	return digest, r.key(ordered[0]) + ":batch-load-lock:" + digest
}

func (r *Reader[T]) unlock(key, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = r.redis.Unlock(ctx, key, token)
}

func (r *Reader[T]) localPayload(key string) ([]byte, bool) {
	if r == nil || r.local == nil {
		return nil, false
	}
	value, ok := r.local.Get(key)
	if !ok {
		return nil, false
	}
	payload, ok := value.([]byte)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), payload...), true
}

func (r *Reader[T]) setLocal(key string, payload []byte, ttl time.Duration) {
	if r == nil || r.local == nil {
		return
	}
	r.local.Set(key, append([]byte(nil), payload...), ttl)
}

func uniqueIDs(ids []uint64) []uint64 {
	result := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func redisPayload(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case string:
		return []byte(typed), true
	case []byte:
		return typed, true
	default:
		return nil, false
	}
}

func isNullPayload(payload []byte) bool {
	return string(payload) == nullPayload
}

func ttlWithJitter(base, jitter time.Duration) time.Duration {
	if base <= 0 || jitter <= 0 {
		return base
	}
	maximum := big.NewInt(int64(jitter) + 1)
	value, err := cryptorand.Int(cryptorand.Reader, maximum)
	if err != nil {
		return base
	}
	return base + time.Duration(value.Int64())
}
