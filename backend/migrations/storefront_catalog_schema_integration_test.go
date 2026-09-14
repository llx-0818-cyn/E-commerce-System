package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
)

func TestStorefrontCatalogMigrationOwnershipStatusesSKUCodeAndPaginationIndexes(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行店铺商品 Schema Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "storefront_catalog")
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

	assertIndex(t, database, schema, "shops", "uq_shops_owner", true, []string{"owner_id"})
	assertIndex(t, database, schema, "shops", "idx_shops_public_page", false, []string{"status", "created_at", "id"})
	assertIndex(t, database, schema, "categories", "uq_categories_slug", true, []string{"slug"})
	assertIndex(t, database, schema, "categories", "idx_categories_parent_page", false, []string{"parent_id", "status", "sort_order", "id"})
	assertIndex(t, database, schema, "products", "idx_products_shop_page", false, []string{"shop_id", "status", "created_at", "id"})
	assertIndex(t, database, schema, "products", "idx_products_public_page", false, []string{"status", "created_at", "id"})
	assertIndex(t, database, schema, "products", "idx_products_category_page", false, []string{"category_id", "status", "created_at", "id"})
	assertIndex(t, database, schema, "product_images", "uq_product_images_order", true, []string{"product_id", "sort_order"})
	assertIndex(t, database, schema, "product_skus", "uq_product_skus_code", true, []string{"sku_code"})
	assertIndex(t, database, schema, "product_skus", "idx_product_skus_product_sale", false, []string{"product_id", "status", "price_cent", "id"})
	assertColumnType(t, database, schema, "product_skus", "price_cent", "bigint")
	assertColumnType(t, database, schema, "product_skus", "original_price_cent", "bigint")

	shopResult, err := database.ExecContext(ctx, "INSERT INTO shops (owner_id,name,status) VALUES (1,'一号店','open')")
	if err != nil {
		t.Fatalf("insert shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	_, err = database.ExecContext(ctx, "INSERT INTO shops (owner_id,name) VALUES (1,'重复店')")
	assertDuplicateKey(t, err, "shop owner")
	_, err = database.ExecContext(ctx, "UPDATE shops SET status='unknown' WHERE id=?", shopID)
	assertMySQLCode(t, err, 3819, "shop status check")

	categoryResult, err := database.ExecContext(ctx, "INSERT INTO categories (name,slug,status,sort_order) VALUES ('服饰','clothing','active',10)")
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}
	categoryID, _ := categoryResult.LastInsertId()
	_, err = database.ExecContext(ctx, "INSERT INTO categories (name,slug) VALUES ('重复','clothing')")
	assertDuplicateKey(t, err, "category slug")

	productResult, err := database.ExecContext(ctx, `
INSERT INTO products (shop_id,category_id,name,subtitle,description,cover_url,status)
VALUES (?, ?, '纯棉 T 恤', '多规格', '商品详情', '/products/tshirt-cover.jpg', 'on_sale')`, shopID, categoryID)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	_, err = database.ExecContext(ctx, "INSERT INTO products (shop_id,name,description) VALUES (999999,'越界商品','无店铺')")
	assertMySQLCode(t, err, 1452, "product shop ownership foreign key")
	secondProductResult, err := database.ExecContext(ctx, "INSERT INTO products (shop_id,name,description) VALUES (?,'第二商品','SKU 唯一性')", shopID)
	if err != nil {
		t.Fatalf("insert second product: %v", err)
	}
	secondProductID, _ := secondProductResult.LastInsertId()

	for order := 0; order < 2; order++ {
		_, err := database.ExecContext(ctx, `
INSERT INTO product_images
    (product_id,storage_key,image_url,mime_type,byte_size,width,height,sort_order)
VALUES (?, ?, ?, 'image/jpeg', 1024, 800, 800, ?)`,
			productID,
			"products/tshirt-"+strconv.Itoa(order)+".jpg",
			"/static/products/tshirt-"+strconv.Itoa(order)+".jpg",
			order,
		)
		if err != nil {
			t.Fatalf("insert product image %d: %v", order, err)
		}
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO product_images
    (product_id,storage_key,image_url,mime_type,byte_size,width,height,sort_order)
VALUES (?, 'products/duplicate.jpg', '/static/products/duplicate.jpg', 'image/jpeg', 1, 1, 1, 1)`, productID)
	assertDuplicateKey(t, err, "product image order")

	_, err = database.ExecContext(ctx, `
INSERT INTO product_skus
    (product_id,sku_code,specs_json,price_cent,original_price_cent,available_stock,status)
VALUES (?, 'TSHIRT-WHITE-L', JSON_OBJECT('颜色','白色','尺寸','L'), 9900, 12900, 20, 'active')`, productID)
	if err != nil {
		t.Fatalf("insert SKU: %v", err)
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,status)
VALUES (?, 'TSHIRT-WHITE-L', JSON_OBJECT('颜色','黑色'), 9900, 'active')`, secondProductID)
	assertDuplicateKey(t, err, "global SKU code")
	_, err = database.ExecContext(ctx, `
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent)
VALUES (?, 'BAD-SPECS', JSON_ARRAY('L'), 9900)`, productID)
	assertMySQLCode(t, err, 3819, "SKU specs object check")
	_, err = database.ExecContext(ctx, `
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent)
VALUES (?, 'BAD-PRICE', JSON_OBJECT(), -1)`, productID)
	assertMySQLCode(t, err, 3819, "SKU positive integer price check")
	_, err = database.ExecContext(ctx, `
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock)
VALUES (?, 'BAD-STOCK', JSON_OBJECT(), 100, -1)`, productID)
	assertMySQLCode(t, err, 3819, "SKU non-negative stock check")
	_, err = database.ExecContext(ctx, `
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,original_price_cent)
VALUES (?, 'BAD-ORIGINAL-PRICE', JSON_OBJECT(), 200, 100)`, productID)
	assertMySQLCode(t, err, 3819, "SKU original price check")

	assertQueryUsesIndex(t, database, "SELECT id FROM products FORCE INDEX (idx_products_shop_page) WHERE shop_id = 1 AND status = 'on_sale' ORDER BY shop_id, status, created_at DESC, id DESC LIMIT 20", "idx_products_shop_page")
	assertQueryUsesIndex(t, database, "SELECT id FROM product_skus FORCE INDEX (idx_product_skus_product_sale) WHERE product_id = 1 AND status = 'active' ORDER BY product_id, status, price_cent, id LIMIT 20", "idx_product_skus_product_sale")

	rollbackCount := migrationCountFrom(items, 7)
	if rolledBack, err := runner.Down(ctx, rollbackCount); err != nil || rolledBack != rollbackCount {
		t.Fatalf("Down(%d) = %d, %v; want %d, nil", rollbackCount, rolledBack, err, rollbackCount)
	}
	assertTablesAbsent(t, database, schema, []string{"shops", "categories", "products", "product_images", "product_skus"})
}

func assertColumnType(t *testing.T, database *sql.DB, schema, table, column, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow(`
SELECT data_type FROM information_schema.columns
WHERE table_schema=? AND table_name=? AND column_name=?`, schema, table, column).Scan(&got); err != nil {
		t.Fatalf("query column type %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("column type %s.%s = %q, want %q", table, column, got, want)
	}
}

func assertMySQLCode(t *testing.T, err error, want uint16, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", label)
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != want {
		t.Fatalf("%s error = %v, want MySQL %d", label, err, want)
	}
}

func assertTablesAbsent(t *testing.T, database *sql.DB, schema string, tables []string) {
	t.Helper()
	for _, table := range tables {
		var count int
		if err := database.QueryRow(`
SELECT COUNT(*) FROM information_schema.tables
WHERE table_schema=? AND table_name=?`, schema, table).Scan(&count); err != nil {
			t.Fatalf("query dropped table %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("table %s still exists after storefront down migration", table)
		}
	}
}
