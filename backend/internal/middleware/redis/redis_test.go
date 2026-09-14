package redis

import (
	"context"
	"feedsystem_video_go/internal/config"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestNewFromEnvUsesFailFastDependencyTimeouts(t *testing.T) {
	client, err := NewFromEnv(&config.RedisConfig{Host: "redis.invalid", Port: 6379})
	if err != nil {
		t.Fatalf("NewFromEnv() error = %v", err)
	}
	defer client.Close()

	options := client.rdb.Options()
	// go-redis normalizes the configured -1 sentinel to zero attempts after initialization.
	if options.MaxRetries != 0 {
		t.Fatalf("normalized MaxRetries = %d, want 0", options.MaxRetries)
	}
	for name, value := range map[string]time.Duration{
		"DialTimeout":  options.DialTimeout,
		"ReadTimeout":  options.ReadTimeout,
		"WriteTimeout": options.WriteTimeout,
		"PoolTimeout":  options.PoolTimeout,
	} {
		if value != 250*time.Millisecond {
			t.Fatalf("%s = %v, want 250ms", name, value)
		}
	}
}

func TestIncrementWithExpireSetsTTLWithoutExtendingWindow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	client := &Client{
		rdb: goredis.NewClient(&goredis.Options{Addr: mr.Addr()}),
	}
	defer client.Close()

	ctx := context.Background()
	key := "feedsystem:ratelimit:test"
	expire := 30 * time.Second

	count, err := client.IncrementWithExpire(ctx, key, expire)
	if err != nil {
		t.Fatalf("first increment: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}

	firstTTL := mr.TTL(key)
	if firstTTL <= 0 || firstTTL > expire {
		t.Fatalf("expected ttl in (0, %s], got %s", expire, firstTTL)
	}

	mr.FastForward(5 * time.Second)
	ttlBeforeSecond := mr.TTL(key)

	count, err = client.IncrementWithExpire(ctx, key, expire)
	if err != nil {
		t.Fatalf("second increment: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	ttlAfterSecond := mr.TTL(key)
	if ttlAfterSecond != ttlBeforeSecond {
		t.Fatalf("expected ttl to stay at %s, got %s", ttlBeforeSecond, ttlAfterSecond)
	}
}
