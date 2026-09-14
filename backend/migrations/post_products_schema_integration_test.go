package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"
)

func TestPostProductsMigrationUniquenessOrderingAndReverseIndex(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容商品关联 Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "post_products")
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
	assertIndex(t, database, schema, "post_products", "PRIMARY", true, []string{"post_id", "product_id"})
	assertIndex(t, database, schema, "post_products", "uq_post_products_order", true, []string{"post_id", "sort_order"})
	assertIndex(t, database, schema, "post_products", "idx_post_products_product_post", false, []string{"product_id", "post_id"})

	postResult, err := database.ExecContext(ctx, "INSERT INTO posts (author_id,title,body,status,published_at) VALUES (1,'挂商品内容','正文','published',CURRENT_TIMESTAMP(6))")
	if err != nil {
		t.Fatalf("seed post: %v", err)
	}
	postID, _ := postResult.LastInsertId()
	shopResult, err := database.ExecContext(ctx, "INSERT INTO shops (owner_id,name,status) VALUES (1,'关联店铺','open')")
	if err != nil {
		t.Fatalf("seed shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productIDs := make([]int64, 0, 3)
	for _, name := range []string{"商品一", "商品二", "商品三"} {
		result, err := database.ExecContext(ctx, "INSERT INTO products (shop_id,name,description,status) VALUES (?,?,'详情','on_sale')", shopID, name)
		if err != nil {
			t.Fatalf("seed product %s: %v", name, err)
		}
		id, _ := result.LastInsertId()
		productIDs = append(productIDs, id)
	}
	for index, productID := range productIDs[:2] {
		if _, err := database.ExecContext(ctx, "INSERT INTO post_products (post_id,product_id,sort_order) VALUES (?,?,?)", postID, productID, index); err != nil {
			t.Fatalf("insert post product %d: %v", index, err)
		}
	}
	_, err = database.ExecContext(ctx, "INSERT INTO post_products (post_id,product_id,sort_order) VALUES (?,?,2)", postID, productIDs[0])
	assertDuplicateKey(t, err, "post/product relation")
	_, err = database.ExecContext(ctx, "INSERT INTO post_products (post_id,product_id,sort_order) VALUES (?,?,1)", postID, productIDs[2])
	assertDuplicateKey(t, err, "post product sort order")
	assertQueryUsesIndex(t, database, "SELECT post_id FROM post_products FORCE INDEX (idx_post_products_product_post) WHERE product_id = 1 ORDER BY product_id, post_id LIMIT 20", "idx_post_products_product_post")

	rollbackCount := migrationCountFrom(items, 8)
	if rolledBack, err := runner.Down(ctx, rollbackCount); err != nil || rolledBack != rollbackCount {
		t.Fatalf("Down(%d) = %d, %v; want %d, nil", rollbackCount, rolledBack, err, rollbackCount)
	}
	assertTablesAbsent(t, database, schema, []string{"post_products"})
}
