package analytics_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"feedsystem_video_go/internal/analytics"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestStatisticsConsumerFailureLeavesCoreSuccessAndAllBehaviorEventsPending(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容电商行为事件集成测试")
	}
	database, cleanup := openAnalyticsTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	items, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(items) {
		t.Fatalf("Up() = %d, %v; want %d, nil", count, err, len(items))
	}
	postResult, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at) VALUES (1,'核心内容','已提交','published',CURRENT_TIMESTAMP(6))")
	if err != nil {
		t.Fatalf("seed core post: %v", err)
	}
	postIDValue, _ := postResult.LastInsertId()
	postID := uint64(postIDValue)
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (1,'行为店铺','open')")
	if err != nil {
		t.Fatalf("seed core shop: %v", err)
	}
	shopIDValue, _ := shopResult.LastInsertId()
	shopID := uint64(shopIDValue)
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'行为商品','核心商品','on_sale')", shopID)
	if err != nil {
		t.Fatalf("seed core product: %v", err)
	}
	productIDValue, _ := productResult.LastInsertId()
	productID := uint64(productIDValue)
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'BEHAVIOR-SKU',JSON_OBJECT(),9900,10,'active')", productID)
	if err != nil {
		t.Fatalf("seed core SKU: %v", err)
	}
	skuIDValue, _ := skuResult.LastInsertId()
	skuID := uint64(skuIDValue)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	recorder, err := analytics.NewRecorder(gormDB)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.RecordContentExposures(ctx, 0, []uint64{postID, postID}); err != nil {
		t.Fatalf("RecordContentExposures() error = %v", err)
	}
	for label, record := range map[string]func() error{
		"content click": func() error { return recorder.RecordContentClick(ctx, 7, postID) },
		"shop visit":    func() error { return recorder.RecordShopVisit(ctx, 7, shopID) },
		"product click": func() error { return recorder.RecordProductClick(ctx, 7, productID, shopID) },
		"cart add":      func() error { return recorder.RecordCartItemAdded(ctx, 7, skuID, productID, 2) },
		"order create":  func() error { return recorder.RecordOrderCreated(ctx, 7, 9001, shopID, 19800) },
		"order paid":    func() error { return recorder.RecordOrderPaid(ctx, 7, 9001, 8001, 19800) },
	} {
		if err := record(); err != nil {
			t.Fatalf("record %s: %v", label, err)
		}
	}

	rows, err := database.Query("SELECT event_type,status FROM outbox_events WHERE event_type LIKE 'behavior.%'")
	if err != nil {
		t.Fatalf("query behavior outbox: %v", err)
	}
	defer rows.Close()
	var eventTypes []string
	for rows.Next() {
		var eventType, status string
		if err := rows.Scan(&eventType, &status); err != nil {
			t.Fatalf("scan behavior outbox: %v", err)
		}
		if status != "pending" {
			t.Fatalf("behavior event %s status = %s, want pending", eventType, status)
		}
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)
	wantTypes := []string{
		analytics.CartItemAddedEvent,
		analytics.ContentClickedEvent,
		analytics.ContentExposedEvent,
		analytics.OrderCreatedEvent,
		analytics.OrderPaidEvent,
		analytics.ProductClickedEvent,
		analytics.ShopVisitedEvent,
	}
	sort.Strings(wantTypes)
	if fmt.Sprint(eventTypes) != fmt.Sprint(wantTypes) {
		t.Fatalf("behavior event types = %v, want %v", eventTypes, wantTypes)
	}

	sinkErr := errors.New("statistics storage unavailable")
	consumer, err := analytics.NewConsumer(failingStatisticsSink{err: sinkErr})
	if err != nil {
		t.Fatalf("NewConsumer() error = %v", err)
	}
	var eventType string
	var payload []byte
	if err := database.QueryRow("SELECT event_type,payload_json FROM outbox_events WHERE event_type=? LIMIT 1", analytics.ProductClickedEvent).Scan(&eventType, &payload); err != nil {
		t.Fatalf("load behavior payload: %v", err)
	}
	if err := consumer.Handle(ctx, eventType, payload); !errors.Is(err, sinkErr) {
		t.Fatalf("statistics Consumer.Handle() error = %v, want %v", err, sinkErr)
	}
	assertAnalyticsCoreRow(t, database, "posts", postID)
	assertAnalyticsCoreRow(t, database, "products", productID)
	var pending int
	if err := database.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE event_type LIKE 'behavior.%' AND status='pending'").Scan(&pending); err != nil {
		t.Fatalf("count pending behavior events: %v", err)
	}
	if pending != 7 {
		t.Fatalf("pending behavior events after consumer failure = %d, want 7", pending)
	}
}

type failingStatisticsSink struct{ err error }

func (s failingStatisticsSink) Apply(context.Context, analytics.BehaviorEvent) error { return s.err }

func assertAnalyticsCoreRow(t *testing.T, database *sql.DB, table string, id uint64) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM `"+table+"` WHERE id=?", id).Scan(&count); err != nil {
		t.Fatalf("query core table %s: %v", table, err)
	}
	if count != 1 {
		t.Fatalf("core row %s/%d count = %d, want 1", table, id, count)
	}
}

func openAnalyticsTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
	t.Helper()
	adminConfig, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatalf("parse MIGRATION_TEST_DSN: %v", err)
	}
	adminConfig.DBName = ""
	adminConfig.MultiStatements = true
	adminConfig.ParseTime = true
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	databaseName := fmt.Sprintf("feedsystem_analytics_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create temporary database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open temporary database: %v", err)
	}
	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop temporary database: %v", err)
		}
		adminDB.Close()
	}
	return database, cleanup
}
