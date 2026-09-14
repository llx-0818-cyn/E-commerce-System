package order_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPersistedOrderItemSnapshotSurvivesCatalogRepricing(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单快照集成测试")
	}
	database, cleanup := openOrderTestDatabase(t, adminDSN)
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}

	shopResult, err := database.ExecContext(ctx, "INSERT INTO shops (owner_id,name,status) VALUES (1,'快照店铺','open')")
	if err != nil {
		t.Fatalf("insert shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productResult, err := database.ExecContext(ctx, "INSERT INTO products (shop_id,name,description,cover_url,status) VALUES (?,'下单时商品名','详情','/old.jpg','on_sale')", shopID)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.ExecContext(ctx, "INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'SNAPSHOT-SKU',JSON_OBJECT('颜色','白色'),1200,10,'active')", productID)
	if err != nil {
		t.Fatalf("insert SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()

	submission := orderdomain.OrderSubmission{BuyerID: 7, IdempotencyKey: "snapshot-key", RequestFingerprint: strings.Repeat("a", 64), GrandTotalCent: 2400}
	if err := gormDB.WithContext(ctx).Create(&submission).Error; err != nil {
		t.Fatalf("create submission: %v", err)
	}
	createdOrder := orderdomain.Order{
		SubmissionID: submission.ID, OrderNo: "ORD-SNAPSHOT-1", BuyerID: 7, ShopID: uint64(shopID),
		Status: orderdomain.StatusPendingPayment, TotalCent: 2400,
		AddressSnapshot: orderdomain.AddressSnapshot{AddressID: 9, RecipientName: "快照买家", Phone: "13800000000", Province: "浙江省", City: "杭州市", Detail: "文三路 1 号"},
		ExpireAt:        time.Now().Add(30 * time.Minute),
	}
	if err := gormDB.WithContext(ctx).Create(&createdOrder).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	snapshot, err := orderdomain.NewOrderItemSnapshot(orderdomain.OrderItemSnapshotInput{
		ProductID: uint64(productID), ProductName: "下单时商品名", CoverURL: "/old.jpg",
		SKUID: uint64(skuID), SKUCode: "SNAPSHOT-SKU", Specs: storefront.SKUSpecs{"颜色": "白色"},
		UnitPriceCent: 1200, Quantity: 2,
	})
	if err != nil {
		t.Fatalf("build item snapshot: %v", err)
	}
	snapshot.OrderID = createdOrder.ID
	if err := gormDB.WithContext(ctx).Create(snapshot).Error; err != nil {
		t.Fatalf("create order item: %v", err)
	}

	if _, err := database.ExecContext(ctx, "UPDATE products SET name='改名后商品',cover_url='/new.jpg' WHERE id=?", productID); err != nil {
		t.Fatalf("rename product: %v", err)
	}
	if _, err := database.ExecContext(ctx, "UPDATE product_skus SET price_cent=1800,specs_json=JSON_OBJECT('颜色','黑色') WHERE id=?", skuID); err != nil {
		t.Fatalf("reprice SKU: %v", err)
	}
	var loaded orderdomain.OrderItem
	if err := gormDB.WithContext(ctx).First(&loaded, snapshot.ID).Error; err != nil {
		t.Fatalf("reload order item: %v", err)
	}
	if loaded.ProductName != "下单时商品名" || loaded.CoverURL != "/old.jpg" || loaded.UnitPriceCent != 1200 ||
		loaded.Quantity != 2 || loaded.SubtotalCent != 2400 || loaded.Specs["颜色"] != "白色" {
		t.Fatalf("persisted snapshot changed after catalog update: %#v", loaded)
	}
}

func openOrderTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_order_test_%d", time.Now().UnixNano())
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
