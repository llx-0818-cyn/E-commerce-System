package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"
)

func TestOrdersInventoryMigrationSubmissionIdempotencySnapshotsAndIndexes(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行订单库存 Schema Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "orders_inventory")
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

	assertIndex(t, database, schema, "order_submissions", "uq_order_submissions_buyer_key", true, []string{"buyer_id", "idempotency_key"})
	assertIndex(t, database, schema, "order_submissions", "idx_order_submissions_buyer_page", false, []string{"buyer_id", "created_at", "id"})
	assertIndex(t, database, schema, "orders", "uq_orders_order_no", true, []string{"order_no"})
	assertIndex(t, database, schema, "orders", "uq_orders_submission_shop", true, []string{"submission_id", "shop_id"})
	assertIndex(t, database, schema, "orders", "idx_orders_buyer_status_page", false, []string{"buyer_id", "status", "created_at", "id"})
	assertIndex(t, database, schema, "orders", "idx_orders_shop_status_page", false, []string{"shop_id", "status", "created_at", "id"})
	assertIndex(t, database, schema, "orders", "idx_orders_expire_scan", false, []string{"status", "expire_at", "id"})
	assertIndex(t, database, schema, "order_items", "uq_order_items_order_sku", true, []string{"order_id", "sku_id"})
	assertIndex(t, database, schema, "inventory_ledger", "uq_inventory_ledger_event", true, []string{"event_id"})
	assertIndex(t, database, schema, "inventory_ledger", "uq_inventory_ledger_order_sku_movement", true, []string{"order_id", "sku_id", "movement_type"})
	assertIndex(t, database, schema, "inventory_ledger", "idx_inventory_ledger_sku_page", false, []string{"sku_id", "created_at", "id"})

	shopOne := insertOrderTestShop(t, database, 101, "订单店一")
	shopTwo := insertOrderTestShop(t, database, 102, "订单店二")
	skuID := insertOrderTestSKU(t, database, shopOne)

	submissionResult, err := database.ExecContext(ctx, `
INSERT INTO order_submissions (buyer_id,idempotency_key,request_fingerprint,grand_total_cent)
VALUES (7,'checkout-key-1',REPEAT('a',64),3000)`)
	if err != nil {
		t.Fatalf("insert order submission: %v", err)
	}
	submissionID, _ := submissionResult.LastInsertId()
	_, err = database.ExecContext(ctx, `
INSERT INTO order_submissions (buyer_id,idempotency_key,request_fingerprint,grand_total_cent)
VALUES (7,'checkout-key-1',REPEAT('b',64),3000)`)
	assertDuplicateKey(t, err, "order submission buyer/idempotency key")
	otherSubmissionResult, err := database.ExecContext(ctx, `
INSERT INTO order_submissions (buyer_id,idempotency_key,request_fingerprint,grand_total_cent)
VALUES (8,'checkout-key-1',REPEAT('b',64),3000)`)
	if err != nil {
		t.Fatalf("same idempotency key for another buyer: %v", err)
	}
	otherSubmissionID, _ := otherSubmissionResult.LastInsertId()

	orderOne := insertOrderTestOrder(t, database, submissionID, 7, shopOne, "ORD-20260824-0001", 1000)
	insertOrderTestOrder(t, database, submissionID, 7, shopTwo, "ORD-20260824-0002", 2000)
	_, err = database.ExecContext(ctx, `
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,total_cent,address_snapshot_json,expire_at)
VALUES (?, 'ORD-20260824-0003', 7, ?, 1000, JSON_OBJECT('recipient_name','重复店铺'), CURRENT_TIMESTAMP(6) + INTERVAL 30 MINUTE)`, submissionID, shopOne)
	assertDuplicateKey(t, err, "order submission/shop")
	_, err = database.ExecContext(ctx, `
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,total_cent,address_snapshot_json,expire_at)
VALUES (?, 'ORD-20260824-0001', 8, ?, 1000, JSON_OBJECT('recipient_name','重复订单号'), CURRENT_TIMESTAMP(6) + INTERVAL 30 MINUTE)`, otherSubmissionID, shopOne)
	assertDuplicateKey(t, err, "order number")
	_, err = database.ExecContext(ctx, `
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,status,total_cent,address_snapshot_json,expire_at)
VALUES (?, 'ORD-BAD-STATE', 7, ?, 'unknown', 1000, JSON_OBJECT(), CURRENT_TIMESTAMP(6) + INTERVAL 30 MINUTE)`, submissionID, shopOne)
	assertMySQLCode(t, err, 3819, "order state check")
	_, err = database.ExecContext(ctx, `
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,total_cent,address_snapshot_json,expire_at)
VALUES (?, 'ORD-BAD-ADDRESS', 7, ?, 1000, JSON_ARRAY('not-object'), CURRENT_TIMESTAMP(6) + INTERVAL 30 MINUTE)`, submissionID, shopOne)
	assertMySQLCode(t, err, 3819, "order address snapshot object check")

	if _, err := database.ExecContext(ctx, `
INSERT INTO order_items
    (order_id,product_id,product_name,cover_url,sku_id,sku_code,specs_json,unit_price_cent,quantity,subtotal_cent)
VALUES (?, 1, '订单快照商品', '/cover.jpg', ?, 'ORDER-SKU-1', JSON_OBJECT('颜色','白色'), 500, 2, 1000)`, orderOne, skuID); err != nil {
		t.Fatalf("insert order item snapshot: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO order_items
    (order_id,product_id,product_name,sku_id,sku_code,specs_json,unit_price_cent,quantity,subtotal_cent)
VALUES (?, 1, '重复订单项', ?, 'ORDER-SKU-1', JSON_OBJECT(), 500, 2, 1000)`, orderOne, skuID)
	assertDuplicateKey(t, err, "order item order/SKU")
	_, err = database.ExecContext(ctx, `
INSERT INTO order_items
    (order_id,product_id,product_name,sku_id,sku_code,specs_json,unit_price_cent,quantity,subtotal_cent)
VALUES (?, 1, '错误小计', 999, 'BAD-SUBTOTAL', JSON_OBJECT(), 500, 2, 999)`, orderOne)
	assertMySQLCode(t, err, 3819, "order item subtotal check")

	if _, err := database.ExecContext(ctx, `
INSERT INTO inventory_ledger
    (event_id,order_id,sku_id,movement_type,quantity,available_delta,locked_delta,sold_delta,sku_version)
VALUES ('inventory-lock-1', ?, ?, 'lock', 2, -2, 2, 0, 1)`, orderOne, skuID); err != nil {
		t.Fatalf("insert inventory lock ledger: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO inventory_ledger
    (event_id,order_id,sku_id,movement_type,quantity,available_delta,locked_delta,sold_delta,sku_version)
VALUES ('inventory-lock-1', ?, 999, 'lock', 1, -1, 1, 0, 2)`, orderOne)
	assertDuplicateKey(t, err, "inventory ledger event")
	_, err = database.ExecContext(ctx, `
INSERT INTO inventory_ledger
    (event_id,order_id,sku_id,movement_type,quantity,available_delta,locked_delta,sold_delta,sku_version)
VALUES ('inventory-lock-2', ?, ?, 'lock', 2, -2, 2, 0, 2)`, orderOne, skuID)
	assertDuplicateKey(t, err, "inventory ledger order/SKU/movement")
	_, err = database.ExecContext(ctx, `
INSERT INTO inventory_ledger
    (event_id,order_id,sku_id,movement_type,quantity,available_delta,locked_delta,sold_delta,sku_version)
VALUES ('inventory-bad-release', ?, 999, 'release', 2, -2, 2, 0, 2)`, orderOne)
	assertMySQLCode(t, err, 3819, "inventory movement conservation check")

	assertQueryUsesIndex(t, database, "SELECT id FROM orders FORCE INDEX (idx_orders_expire_scan) WHERE status='pending_payment' AND expire_at <= CURRENT_TIMESTAMP(6) ORDER BY status,expire_at,id LIMIT 100", "idx_orders_expire_scan")
	assertQueryUsesIndex(t, database, "SELECT id FROM orders FORCE INDEX (idx_orders_buyer_status_page) WHERE buyer_id=7 AND status='pending_payment' ORDER BY buyer_id,status,created_at DESC,id DESC LIMIT 20", "idx_orders_buyer_status_page")
	assertQueryUsesIndex(t, database, "SELECT id FROM inventory_ledger FORCE INDEX (idx_inventory_ledger_sku_page) WHERE sku_id=1 ORDER BY sku_id,created_at DESC,id DESC LIMIT 20", "idx_inventory_ledger_sku_page")

	rollbackCount := migrationCountFrom(items, 11)
	if rolledBack, err := runner.Down(ctx, rollbackCount); err != nil || rolledBack != rollbackCount {
		t.Fatalf("Down(%d) = %d, %v; want %d, nil", rollbackCount, rolledBack, err, rollbackCount)
	}
	assertTablesAbsent(t, database, schema, []string{"order_submissions", "orders", "order_items", "inventory_ledger"})
}

func insertOrderTestShop(t *testing.T, database *sql.DB, ownerID uint64, name string) int64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?,?,'open')", ownerID, name)
	if err != nil {
		t.Fatalf("insert order test shop: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func insertOrderTestSKU(t *testing.T, database *sql.DB, shopID int64) int64 {
	t.Helper()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'订单商品','订单 Schema 测试','on_sale')", shopID)
	if err != nil {
		t.Fatalf("insert order test product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'ORDER-SKU-1',JSON_OBJECT(),500,10,'active')", productID)
	if err != nil {
		t.Fatalf("insert order test SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	return skuID
}

func insertOrderTestOrder(t *testing.T, database *sql.DB, submissionID int64, buyerID uint64, shopID int64, orderNo string, total int64) int64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO orders
    (submission_id,order_no,buyer_id,shop_id,total_cent,address_snapshot_json,expire_at)
VALUES (?, ?, ?, ?, ?, JSON_OBJECT('recipient_name','测试买家','phone','13800000000'), CURRENT_TIMESTAMP(6) + INTERVAL 30 MINUTE)`, submissionID, orderNo, buyerID, shopID, total)
	if err != nil {
		t.Fatalf("insert order test order: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}
