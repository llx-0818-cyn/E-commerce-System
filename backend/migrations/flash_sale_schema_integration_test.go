package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"
)

func TestFlashSaleMigrationCampaignTimeSKURequestAndReconciliationConstraints(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行秒杀 Schema Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "flash_sale")
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

	assertIndex(t, database, schema, "flash_sale_campaigns", "uq_flash_sale_campaign_no", true, []string{"campaign_no"})
	assertIndex(t, database, schema, "flash_sale_campaigns", "uq_flash_sale_sku_window_version", true, []string{"sku_id", "starts_at", "ends_at", "campaign_version"})
	assertIndex(t, database, schema, "flash_sale_campaigns", "idx_flash_sale_active_window", false, []string{"status", "starts_at", "ends_at", "id"})
	assertIndex(t, database, schema, "flash_sale_requests", "uq_flash_sale_request_id", true, []string{"request_id"})
	assertIndex(t, database, schema, "flash_sale_requests", "uq_flash_sale_campaign_buyer_key", true, []string{"campaign_id", "buyer_id", "idempotency_key"})
	assertIndex(t, database, schema, "flash_sale_requests", "uq_flash_sale_request_order", true, []string{"order_id"})
	assertIndex(t, database, schema, "flash_sale_reconciliation_anomalies", "uq_flash_sale_reconciliation_key", true, []string{"campaign_id", "anomaly_key"})
	assertIndex(t, database, schema, "flash_sale_reconciliation_anomalies", "idx_flash_sale_reconciliation_open", false, []string{"status", "created_at", "id"})

	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (1401,'秒杀店','open')")
	if err != nil {
		t.Fatalf("insert flash sale shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'秒杀商品','详情','on_sale')", shopID)
	if err != nil {
		t.Fatalf("insert flash sale product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'FLASH-SKU',JSON_OBJECT(),2000,100,'active')", productID)
	if err != nil {
		t.Fatalf("insert flash sale SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	campaignResult, err := database.Exec(`
INSERT INTO flash_sale_campaigns
    (campaign_no,sku_id,title,status,price_cent,total_stock,per_user_limit,starts_at,ends_at)
VALUES ('FLASH-20260824-1',?,'限时秒杀','enabled',1000,20,2,CURRENT_TIMESTAMP(6)-INTERVAL 1 MINUTE,CURRENT_TIMESTAMP(6)+INTERVAL 10 MINUTE)`, skuID)
	if err != nil {
		t.Fatalf("insert flash sale campaign: %v", err)
	}
	campaignID, _ := campaignResult.LastInsertId()
	_, err = database.Exec(`
INSERT INTO flash_sale_campaigns
    (campaign_no,sku_id,title,price_cent,total_stock,per_user_limit,starts_at,ends_at)
VALUES ('FLASH-BAD-TIME',?,'错误时间',1000,10,1,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)-INTERVAL 1 MINUTE)`, skuID)
	assertMySQLCode(t, err, 3819, "flash sale time check")
	_, err = database.Exec(`
INSERT INTO flash_sale_campaigns
    (campaign_no,sku_id,title,price_cent,total_stock,per_user_limit,starts_at,ends_at)
VALUES ('FLASH-BAD-SKU',999999,'错误 SKU',1000,10,1,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)+INTERVAL 1 MINUTE)`)
	assertMySQLCode(t, err, 1452, "flash sale SKU foreign key")

	if _, err := database.Exec(`
INSERT INTO flash_sale_requests
    (request_id,campaign_id,buyer_id,idempotency_key,reservation_token,quantity,status)
VALUES ('request-1',?,7,'idem-1',REPEAT('a',64),1,'accepted')`, campaignID); err != nil {
		t.Fatalf("insert flash sale request: %v", err)
	}
	_, err = database.Exec(`
INSERT INTO flash_sale_requests
    (request_id,campaign_id,buyer_id,idempotency_key,reservation_token,quantity,status)
VALUES ('request-2',?,7,'idem-1',REPEAT('b',64),1,'accepted')`, campaignID)
	assertDuplicateKey(t, err, "flash sale campaign/buyer/idempotency key")
	_, err = database.Exec(`
INSERT INTO flash_sale_requests
    (request_id,campaign_id,buyer_id,idempotency_key,reservation_token,quantity,status)
VALUES ('request-1',?,8,'idem-2',REPEAT('c',64),1,'accepted')`, campaignID)
	assertDuplicateKey(t, err, "flash sale request id")
	_, err = database.Exec(`
INSERT INTO flash_sale_requests
    (request_id,campaign_id,buyer_id,idempotency_key,reservation_token,quantity,status)
VALUES ('request-missing-order',?,9,'idem-missing-order',REPEAT('d',64),1,'order_created')`, campaignID)
	assertMySQLCode(t, err, 3819, "flash sale order-created relation check")
	orderID := insertPaymentTestOrder(t, database, 9, shopID, "FLASH-ORDER-1")
	if _, err := database.Exec(`
INSERT INTO flash_sale_requests
    (request_id,campaign_id,buyer_id,idempotency_key,reservation_token,quantity,status,order_id)
VALUES ('request-order-1',?,9,'idem-order-1',REPEAT('d',64),1,'order_created',?)`, campaignID, orderID); err != nil {
		t.Fatalf("insert flash sale request/order relation: %v", err)
	}
	_, err = database.Exec(`
INSERT INTO flash_sale_requests
    (request_id,campaign_id,buyer_id,idempotency_key,reservation_token,quantity,status,order_id)
VALUES ('request-order-2',?,10,'idem-order-2',REPEAT('e',64),1,'order_created',?)`, campaignID, orderID)
	assertDuplicateKey(t, err, "flash sale request/order")

	if _, err := database.Exec(`
INSERT INTO flash_sale_reconciliation_anomalies
    (campaign_id,anomaly_key,anomaly_type,expected_value,actual_value,detail,evidence_json)
VALUES (?,'stock:redis:mysql','stock_mismatch',20,19,'Redis/MySQL stock mismatch',JSON_OBJECT('redis',20,'mysql',19))`, campaignID); err != nil {
		t.Fatalf("insert flash sale reconciliation anomaly: %v", err)
	}
	_, err = database.Exec(`
INSERT INTO flash_sale_reconciliation_anomalies
    (campaign_id,anomaly_key,anomaly_type,detail,evidence_json)
VALUES (?,'stock:redis:mysql','stock_mismatch','duplicate mismatch',JSON_OBJECT())`, campaignID)
	assertDuplicateKey(t, err, "flash sale reconciliation key")
	assertQueryUsesIndex(t, database, "SELECT id FROM flash_sale_reconciliation_anomalies FORCE INDEX (idx_flash_sale_reconciliation_open) WHERE status='open' ORDER BY status,created_at,id LIMIT 100", "idx_flash_sale_reconciliation_open")

	if rolledBack, err := runner.Down(ctx, 1); err != nil || rolledBack != 1 {
		t.Fatalf("Down(1) = %d, %v; want 1, nil", rolledBack, err)
	}
	assertTablesAbsent(t, database, schema, []string{"flash_sale_campaigns", "flash_sale_requests", "flash_sale_reconciliation_anomalies"})
}
