package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/promotion"

	redis "github.com/redis/go-redis/v9"
)

const scriptVersion = "seckillbench-v1"

type benchmarkReport struct {
	ScriptVersion         string             `json:"script_version"`
	StartedAt             time.Time          `json:"started_at"`
	Machine               map[string]any     `json:"machine"`
	Config                map[string]any     `json:"config"`
	DurationMS            float64            `json:"duration_ms"`
	ThroughputRPS         float64            `json:"throughput_rps"`
	LatencyMS             map[string]float64 `json:"latency_ms"`
	Results               map[string]int     `json:"results"`
	TechnicalErrors       int                `json:"technical_errors"`
	TechnicalFailureRate  float64            `json:"technical_failure_rate"`
	BusinessRejectionRate float64            `json:"business_rejection_rate"`
	DuplicateChecks       int                `json:"duplicate_checks"`
	Correctness           map[string]any     `json:"correctness"`
}

func main() {
	redisAddr := flag.String("redis-addr", "127.0.0.1:6379", "Redis host:port")
	redisPassword := flag.String("redis-password", "", "Redis password")
	redisDB := flag.Int("redis-db", 0, "Redis database")
	requests := flag.Int("requests", 5000, "total unique-idempotency requests")
	concurrency := flag.Int("concurrency", 100, "concurrent workers")
	users := flag.Int("users", 2000, "distinct buyers")
	stock := flag.Int64("stock", 1000, "campaign total stock")
	perUserLimit := flag.Int("per-user-limit", 1, "per buyer quantity limit")
	duplicateChecks := flag.Int("duplicate-checks", 100, "accepted requests to replay")
	output := flag.String("output", "", "JSON report path")
	flag.Parse()
	if *requests < 1 || *concurrency < 1 || *users < 1 || *stock < 1 || *perUserLimit < 1 || *duplicateChecks < 0 {
		fatalf("invalid benchmark configuration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	raw := redis.NewClient(&redis.Options{Addr: *redisAddr, Password: *redisPassword, DB: *redisDB})
	defer raw.Close()
	if err := raw.Ping(ctx).Err(); err != nil {
		fatalf("Redis ping failed: %v", err)
	}
	prefix := fmt.Sprintf("bench:%d:", time.Now().UnixNano())
	cache := rediscache.NewClient(raw, prefix)
	defer cleanupPrefix(context.Background(), raw, prefix)
	store, err := promotion.NewLuaReservationStore(cache)
	if err != nil {
		fatalf("create reservation store: %v", err)
	}
	now := time.Now().UTC()
	keys := promotion.ReservationKeys{
		Metadata:     promotion.CampaignMetadataKey(cache, 1, 1),
		Stock:        promotion.CampaignStockKey(cache, 1, 1),
		Buyers:       promotion.CampaignBuyersKey(cache, 1, 1),
		Requests:     promotion.CampaignRequestsKey(cache, 1, 1),
		ResultPrefix: promotion.CampaignRequestResultPrefix(cache, 1, 1),
	}
	metadata, _ := json.Marshal(map[string]any{
		"campaign_id": 1, "version": 1, "sku_id": 1, "price_cent": 1000,
		"total_stock": *stock, "per_user_limit": *perUserLimit,
		"starts_at_ms": now.Add(-time.Minute).UnixMilli(), "ends_at_ms": now.Add(10 * time.Minute).UnixMilli(),
		"status": "enabled",
	})
	if err := cache.SetBytes(ctx, keys.Metadata, metadata, 20*time.Minute); err != nil {
		fatalf("seed metadata: %v", err)
	}
	if err := cache.SetBytes(ctx, keys.Stock, []byte(strconv.FormatInt(*stock, 10)), 20*time.Minute); err != nil {
		fatalf("seed stock: %v", err)
	}

	startedAt := time.Now().UTC()
	started := time.Now()
	jobs := make(chan int)
	latencies := make([]time.Duration, 0, *requests)
	resultCounts := make(map[string]int)
	acceptedCommands := make([]promotion.ReserveCommand, 0, min(*duplicateChecks, int(*stock)))
	acceptedIDs := make(map[string]struct{})
	technicalErrors := 0
	var mu sync.Mutex
	var workers sync.WaitGroup
	for worker := 0; worker < *concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				buyerID := uint64(index%*users + 1)
				command := promotion.ReserveCommand{
					BuyerID: buyerID, IdempotencyKey: fmt.Sprintf("request-%d", index), Quantity: 1,
					RequestID: fmt.Sprintf("c1-v1-bench-%d", index), ReservationToken: fmt.Sprintf("%064x", index+1),
					Now: now, TTL: 20 * time.Minute,
				}
				requestStarted := time.Now()
				result, err := store.Reserve(ctx, keys, command)
				latency := time.Since(requestStarted)
				mu.Lock()
				latencies = append(latencies, latency)
				if err != nil {
					technicalErrors++
					resultCounts["technical_error"]++
				} else {
					resultCounts[string(result.Code)]++
					if result.Code == promotion.ReservationAccepted {
						acceptedIDs[result.RequestID] = struct{}{}
						if len(acceptedCommands) < *duplicateChecks {
							acceptedCommands = append(acceptedCommands, command)
						}
					}
				}
				mu.Unlock()
			}
		}()
	}
	for index := 0; index < *requests; index++ {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	duration := time.Since(started)

	duplicatePasses := 0
	for _, command := range acceptedCommands {
		result, err := store.Reserve(ctx, keys, command)
		if err == nil && result.Code == promotion.ReservationAccepted && result.Duplicate && result.RequestID == command.RequestID {
			duplicatePasses++
		}
	}
	remaining, err := raw.Get(ctx, keys.Stock).Int64()
	if err != nil {
		fatalf("read final stock: %v", err)
	}
	buyerReservations, err := raw.HGetAll(ctx, keys.Buyers).Result()
	if err != nil {
		fatalf("read buyer reservations: %v", err)
	}
	limitOK := true
	reservedTotal := int64(0)
	for _, rawQuantity := range buyerReservations {
		quantity, parseErr := strconv.ParseInt(rawQuantity, 10, 64)
		if parseErr != nil || quantity < 0 || quantity > int64(*perUserLimit) {
			limitOK = false
		}
		reservedTotal += quantity
	}
	accepted := resultCounts[string(promotion.ReservationAccepted)]
	correctness := map[string]any{
		"passed": duplicatePasses == len(acceptedCommands) && technicalErrors == 0 && remaining >= 0 &&
			accepted <= int(*stock) && remaining == *stock-int64(accepted) && len(acceptedIDs) == accepted &&
			reservedTotal == int64(accepted) && limitOK,
		"initial_stock": *stock, "remaining_stock": remaining, "accepted": accepted,
		"unique_accepted_request_ids": len(acceptedIDs), "reserved_total": reservedTotal,
		"per_user_limit_respected": limitOK, "duplicate_replays_passed": duplicatePasses,
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report := benchmarkReport{
		ScriptVersion: scriptVersion, StartedAt: startedAt,
		Machine:       map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go_version": runtime.Version(), "logical_cpus": runtime.NumCPU()},
		Config:        map[string]any{"redis_addr": *redisAddr, "redis_db": *redisDB, "requests": *requests, "concurrency": *concurrency, "users": *users, "stock": *stock, "per_user_limit": *perUserLimit},
		DurationMS:    float64(duration.Microseconds()) / 1000,
		ThroughputRPS: float64(*requests) / duration.Seconds(),
		LatencyMS: map[string]float64{
			"p50": durationMS(percentile(latencies, 0.50)), "p95": durationMS(percentile(latencies, 0.95)),
			"p99": durationMS(percentile(latencies, 0.99)), "max": durationMS(percentile(latencies, 1)),
		},
		Results: resultCounts, TechnicalErrors: technicalErrors,
		TechnicalFailureRate:  float64(technicalErrors) / float64(*requests),
		BusinessRejectionRate: float64(*requests-accepted-technicalErrors) / float64(*requests),
		DuplicateChecks:       len(acceptedCommands), Correctness: correctness,
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			fatalf("create report directory: %v", err)
		}
		if err := os.WriteFile(*output, append(encoded, '\n'), 0o644); err != nil {
			fatalf("write report: %v", err)
		}
	}
	fmt.Println(string(encoded))
	if passed, _ := correctness["passed"].(bool); !passed {
		os.Exit(1)
	}
}

func percentile(values []time.Duration, fraction float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * fraction)
	return values[index]
}

func durationMS(value time.Duration) float64 {
	return float64(value.Microseconds()) / 1000
}

func cleanupPrefix(ctx context.Context, client *redis.Client, prefix string) {
	keys, _ := client.Keys(ctx, prefix+"*").Result()
	if len(keys) > 0 {
		_ = client.Del(ctx, keys...).Err()
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
