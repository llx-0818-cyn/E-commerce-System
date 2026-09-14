package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

const (
	scriptVersion = "readbench-v1"
	maxBodyBytes  = 8 << 20
)

type datasetSnapshot struct {
	PublishedPosts int64 `json:"published_posts"`
	PublicProducts int64 `json:"public_products"`
	ActiveSKUs     int64 `json:"active_skus"`
	Shops          int64 `json:"shops"`
}

type inventorySnapshot struct {
	SKUs           int64 `json:"skus"`
	AvailableStock int64 `json:"available_stock"`
	LockedStock    int64 `json:"locked_stock"`
	SoldStock      int64 `json:"sold_stock"`
	TotalUnits     int64 `json:"total_units"`
	NegativeRows   int64 `json:"negative_rows"`
}

type sample struct {
	latency    time.Duration
	statusCode int
	bytes      int
	errorKind  string
}

type benchmarkReport struct {
	ScriptVersion        string             `json:"script_version"`
	StartedAt            time.Time          `json:"started_at"`
	Machine              map[string]any     `json:"machine"`
	Scenario             string             `json:"scenario"`
	Config               map[string]any     `json:"config"`
	Dependencies         map[string]any     `json:"dependencies"`
	Dataset              datasetSnapshot    `json:"dataset"`
	DurationMS           float64            `json:"duration_ms"`
	ThroughputRPS        float64            `json:"throughput_rps"`
	LatencyMS            map[string]float64 `json:"latency_ms"`
	StatusCodes          map[string]int     `json:"status_codes"`
	ResponseBytes        map[string]float64 `json:"response_bytes"`
	TechnicalErrors      int                `json:"technical_errors"`
	TechnicalFailureRate float64            `json:"technical_failure_rate"`
	ErrorKinds           map[string]int     `json:"error_kinds"`
	Inventory            map[string]any     `json:"inventory"`
	Correctness          map[string]any     `json:"correctness"`
}

func main() {
	scenario := flag.String("scenario", "", "feed or product")
	targetURL := flag.String("url", "", "absolute public API URL")
	requests := flag.Int("requests", 3000, "measured requests")
	concurrency := flag.Int("concurrency", 60, "concurrent workers")
	warmup := flag.Int("warmup", 100, "warmup requests excluded from metrics")
	timeout := flag.Duration("timeout", 10*time.Second, "per-request timeout")
	expectedProductID := flag.Uint64("expected-product-id", 0, "required product ID for product scenario")
	mysqlDSN := flag.String("mysql-dsn", "", "MySQL DSN used only for dataset and inventory snapshots")
	output := flag.String("output", "", "JSON report path")
	flag.Parse()

	if (*scenario != "feed" && *scenario != "product") || *targetURL == "" || *requests < 1 || *concurrency < 1 || *warmup < 0 || *timeout <= 0 || *mysqlDSN == "" {
		fatalf("invalid read benchmark configuration")
	}
	if *scenario == "product" && *expectedProductID == 0 {
		fatalf("product scenario requires -expected-product-id")
	}
	parsedDSN, err := mysql.ParseDSN(*mysqlDSN)
	if err != nil {
		fatalf("parse MySQL DSN: %v", err)
	}
	database, err := sql.Open("mysql", *mysqlDSN)
	if err != nil {
		fatalf("open MySQL: %v", err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		fatalf("ping MySQL: %v", err)
	}
	dataset, err := loadDataset(ctx, database)
	if err != nil {
		fatalf("load dataset: %v", err)
	}
	inventoryBefore, err := loadInventory(ctx, database)
	if err != nil {
		fatalf("load inventory before: %v", err)
	}

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency * 2,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: *timeout}
	defer transport.CloseIdleConnections()
	validator := func(body []byte) error {
		if *scenario == "feed" {
			return validateFeed(body)
		}
		return validateProduct(body, *expectedProductID)
	}
	for index := 0; index < *warmup; index++ {
		result := performRequest(ctx, client, *targetURL, validator)
		if result.errorKind != "" {
			fatalf("warmup request %d failed: %s", index+1, result.errorKind)
		}
	}

	startedAt := time.Now().UTC()
	started := time.Now()
	jobs := make(chan struct{})
	results := make(chan sample, *requests)
	var workers sync.WaitGroup
	for index := 0; index < *concurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				results <- performRequest(ctx, client, *targetURL, validator)
			}
		}()
	}
	go func() {
		for index := 0; index < *requests; index++ {
			jobs <- struct{}{}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	latencies := make([]time.Duration, 0, *requests)
	statusCodes := make(map[string]int)
	errorKinds := make(map[string]int)
	technicalErrors := 0
	totalResponseBytes := 0
	for result := range results {
		latencies = append(latencies, result.latency)
		statusCodes[strconv.Itoa(result.statusCode)]++
		totalResponseBytes += result.bytes
		if result.errorKind != "" {
			technicalErrors++
			errorKinds[result.errorKind]++
		}
	}
	duration := time.Since(started)
	inventoryAfter, err := loadInventory(ctx, database)
	if err != nil {
		fatalf("load inventory after: %v", err)
	}
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	inventoryConserved := inventoryBefore.TotalUnits == inventoryAfter.TotalUnits
	inventoryNonNegative := inventoryBefore.NegativeRows == 0 && inventoryAfter.NegativeRows == 0
	passed := technicalErrors == 0 && len(latencies) == *requests && inventoryConserved && inventoryNonNegative

	report := benchmarkReport{
		ScriptVersion: scriptVersion,
		StartedAt:     startedAt,
		Machine: map[string]any{
			"goos": runtime.GOOS, "goarch": runtime.GOARCH,
			"go_version": runtime.Version(), "logical_cpus": runtime.NumCPU(),
		},
		Scenario: *scenario,
		Config: map[string]any{
			"url": *targetURL, "requests": *requests, "concurrency": *concurrency,
			"warmup_requests": *warmup, "timeout_ms": timeout.Milliseconds(),
			"cache_state": "warmed", "expected_product_id": *expectedProductID,
		},
		Dependencies: map[string]any{
			"api": endpoint(*targetURL), "mysql": parsedDSN.Addr,
			"mysql_database": parsedDSN.DBName, "redis": "API configured dependency",
			"rabbitmq": "not on synchronous read path",
		},
		Dataset:       dataset,
		DurationMS:    durationMS(duration),
		ThroughputRPS: float64(*requests) / duration.Seconds(),
		LatencyMS: map[string]float64{
			"p50": durationMS(percentile(latencies, 0.50)),
			"p95": durationMS(percentile(latencies, 0.95)),
			"p99": durationMS(percentile(latencies, 0.99)),
			"max": durationMS(percentile(latencies, 1.00)),
		},
		StatusCodes: statusCodes,
		ResponseBytes: map[string]float64{
			"total":   float64(totalResponseBytes),
			"average": float64(totalResponseBytes) / float64(*requests),
		},
		TechnicalErrors:      technicalErrors,
		TechnicalFailureRate: float64(technicalErrors) / float64(*requests),
		ErrorKinds:           errorKinds,
		Inventory: map[string]any{
			"before": inventoryBefore, "after": inventoryAfter,
			"total_units_conserved":     inventoryConserved,
			"all_segments_non_negative": inventoryNonNegative,
		},
		Correctness: map[string]any{
			"passed": passed, "responses_validated": *requests - technicalErrors,
			"all_requests_successful": technicalErrors == 0,
		},
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			fatalf("create report directory: %v", err)
		}
		if err := os.WriteFile(*output, append(encoded, '\n'), 0o644); err != nil {
			fatalf("write report: %v", err)
		}
	}
	fmt.Println(string(encoded))
	if !passed {
		os.Exit(1)
	}
}

func performRequest(ctx context.Context, client *http.Client, targetURL string, validator func([]byte) error) sample {
	started := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return sample{latency: time.Since(started), errorKind: "build_request"}
	}
	response, err := client.Do(request)
	if err != nil {
		return sample{latency: time.Since(started), errorKind: "http_request"}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	result := sample{latency: time.Since(started), statusCode: response.StatusCode, bytes: len(body)}
	if err != nil {
		result.errorKind = "read_body"
		return result
	}
	if len(body) > maxBodyBytes {
		result.errorKind = "body_too_large"
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.errorKind = "http_status"
		return result
	}
	if err := validator(body); err != nil {
		result.errorKind = "response_contract"
	}
	return result
}

func validateFeed(body []byte) error {
	for _, forbidden := range []string{"\"play_url\"", "\"video_url\"", "\"audio_url\"", "\"video_id\""} {
		if strings.Contains(string(body), forbidden) {
			return fmt.Errorf("forbidden response field %s", forbidden)
		}
	}
	var payload struct {
		Items []struct {
			ID     uint64 `json:"id"`
			Title  string `json:"title"`
			Author struct {
				ID uint64 `json:"id"`
			} `json:"author"`
			Images   []json.RawMessage `json:"images"`
			Tags     []json.RawMessage `json:"tags"`
			Products []json.RawMessage `json:"products"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if len(payload.Items) == 0 {
		return fmt.Errorf("feed is empty")
	}
	seen := make(map[uint64]struct{}, len(payload.Items))
	for _, item := range payload.Items {
		if item.ID == 0 || item.Author.ID == 0 || strings.TrimSpace(item.Title) == "" || item.Images == nil || item.Tags == nil || item.Products == nil {
			return fmt.Errorf("feed item is incomplete")
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return fmt.Errorf("duplicate feed item %d", item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	return nil
}

func validateProduct(body []byte, expectedID uint64) error {
	var payload struct {
		Product struct {
			ID     uint64 `json:"id"`
			Status string `json:"status"`
		} `json:"product"`
		Shop struct {
			AcceptingOrders bool `json:"accepting_orders"`
		} `json:"shop"`
		Images []json.RawMessage `json:"images"`
		SKUs   []struct {
			ID             uint64 `json:"id"`
			PriceCent      int64  `json:"price_cent"`
			AvailableStock int64  `json:"available_stock"`
			Status         string `json:"status"`
			CanPurchase    bool   `json:"can_purchase"`
		} `json:"skus"`
		MinPriceCent *int64 `json:"min_price_cent"`
		CanPurchase  bool   `json:"can_purchase"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if payload.Product.ID != expectedID || payload.Product.Status != "on_sale" || payload.Images == nil || len(payload.SKUs) == 0 || payload.MinPriceCent == nil || *payload.MinPriceCent <= 0 {
		return fmt.Errorf("product detail is incomplete")
	}
	for _, sku := range payload.SKUs {
		if sku.ID == 0 || sku.PriceCent <= 0 || sku.AvailableStock < 0 || sku.Status != "active" {
			return fmt.Errorf("public SKU invariant failed")
		}
	}
	return nil
}

func loadDataset(ctx context.Context, database *sql.DB) (datasetSnapshot, error) {
	var result datasetSnapshot
	queries := []struct {
		query string
		value *int64
	}{
		{"SELECT COUNT(*) FROM posts WHERE status='published' AND deleted_at IS NULL", &result.PublishedPosts},
		{"SELECT COUNT(*) FROM products WHERE status='on_sale'", &result.PublicProducts},
		{"SELECT COUNT(*) FROM product_skus WHERE status='active'", &result.ActiveSKUs},
		{"SELECT COUNT(*) FROM shops WHERE status='open'", &result.Shops},
	}
	for _, item := range queries {
		if err := database.QueryRowContext(ctx, item.query).Scan(item.value); err != nil {
			return datasetSnapshot{}, err
		}
	}
	return result, nil
}

func loadInventory(ctx context.Context, database *sql.DB) (inventorySnapshot, error) {
	var result inventorySnapshot
	err := database.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(available_stock),0), COALESCE(SUM(locked_stock),0),
		       COALESCE(SUM(sold_stock),0),
		       COALESCE(SUM(available_stock + locked_stock + sold_stock),0),
		       COALESCE(SUM(CASE WHEN available_stock < 0 OR locked_stock < 0 OR sold_stock < 0 THEN 1 ELSE 0 END),0)
		FROM product_skus`).Scan(
		&result.SKUs, &result.AvailableStock, &result.LockedStock,
		&result.SoldStock, &result.TotalUnits, &result.NegativeRows,
	)
	return result, err
}

func endpoint(raw string) string {
	parts := strings.SplitN(raw, "/", 4)
	if len(parts) >= 3 {
		return parts[0] + "//" + parts[2]
	}
	return raw
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
