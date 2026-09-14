package redis

import (
	"context"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"
)

func (c *Client) ZincrBy(ctx context.Context, key string, member string, score float64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZIncrBy(ctx, key, score, member).Err()
}

func (c *Client) ZAdd(ctx context.Context, key string, members ...redis.Z) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZAdd(ctx, key, members...).Err()
}

func (c *Client) ZRemRangeByRank(ctx context.Context, key string, start int64, stop int64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZRemRangeByRank(ctx, key, start, stop).Err()
}

func (c *Client) ZRangeWithScores(ctx context.Context, key string, start int64, stop int64) ([]redis.Z, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("redis client not initialized")
	}
	return c.rdb.ZRangeWithScores(ctx, key, start, stop).Result()
}

func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Expire(ctx, key, ttl).Err()
}

func (c *Client) ZUnionStore(ctx context.Context, dst string, keys []string, aggregate string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZUnionStore(ctx, dst, &redis.ZStore{
		Keys:      keys,
		Aggregate: aggregate,
	}).Err()
}

func (c *Client) ZUnionStoreWeighted(ctx context.Context, dst string, keys []string, weights []float64, aggregate string) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client not initialized")
	}
	if len(keys) == 0 || len(keys) != len(weights) {
		return errors.New("zunion keys and weights must be non-empty and aligned")
	}
	return c.rdb.ZUnionStore(ctx, dst, &redis.ZStore{Keys: keys, Weights: weights, Aggregate: aggregate}).Err()
}

var addCappedSupplementScript = redis.NewScript(`
local rows = redis.call('ZRANGE', KEYS[2], 0, -1, 'WITHSCORES')
local applied = 0
local cap = tonumber(ARGV[1])
for i = 1, #rows, 2 do
  local member = rows[i]
  local base = redis.call('ZSCORE', KEYS[1], member)
  if base then
    local addition = math.min(tonumber(rows[i + 1]), cap)
    if addition > 0 then
      redis.call('ZINCRBY', KEYS[1], addition, member)
      applied = applied + 1
    end
  end
end
return applied
`)

func (c *Client) AddCappedZSetSupplement(ctx context.Context, destination, supplement string, cap float64) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client not initialized")
	}
	if cap < 0 {
		return errors.New("zset supplement cap must not be negative")
	}
	return addCappedSupplementScript.Run(ctx, c.rdb, []string{destination, supplement}, cap).Err()
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, nil
	}
	n, err := c.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

func (c *Client) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	return c.rdb.ZRevRange(ctx, key, start, stop).Result()
}

func (c *Client) ZRevRangeByScore(ctx context.Context, key string, max, min string, offset, count int64) ([]string, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	return c.rdb.ZRevRangeByScore(ctx, key, &redis.ZRangeBy{
		Max:    max,
		Min:    min,
		Offset: offset,
		Count:  count,
	}).Result()
}
