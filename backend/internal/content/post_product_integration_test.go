package content_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostProductOwnershipAvailabilityOrderingAndTransactionalRollback(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行内容关联商品集成测试")
	}
	database, cleanup := openContentTestDatabase(t, adminDSN)
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
	ownerShop := seedPostProductShop(t, database, 1, "作者店", "open")
	otherShop := seedPostProductShop(t, database, 2, "其他店", "open")
	ownerOne := seedPostProductCatalogItem(t, database, ownerShop, "作者商品一", "on_sale", "OWNER-ONE", "active", 5)
	ownerTwo := seedPostProductCatalogItem(t, database, ownerShop, "作者商品二", "on_sale", "OWNER-TWO", "active", 2)
	otherProduct := seedPostProductCatalogItem(t, database, otherShop, "跨店商品", "on_sale", "OTHER-ONE", "active", 5)
	offSaleProduct := seedPostProductCatalogItem(t, database, ownerShop, "已下架", "off_sale", "OWNER-OFF", "active", 5)
	soldOutProduct := seedPostProductCatalogItem(t, database, ownerShop, "已售罄", "on_sale", "OWNER-SOLDOUT", "active", 0)

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := content.NewRepository(gormDB)
	service, err := content.NewService(repository, config.ContentConfig{MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	post, err := service.Publish(ctx, content.PublishInput{
		AuthorID: 1, Title: "挂商品内容", Body: "按请求顺序保存", ProductIDs: []uint64{ownerTwo, ownerOne},
	})
	if err != nil {
		t.Fatalf("Publish(valid products) error = %v", err)
	}
	if !reflect.DeepEqual(post.ProductIDs, []uint64{ownerTwo, ownerOne}) {
		t.Fatalf("published product_ids = %v", post.ProductIDs)
	}
	assertPostProductOrder(t, database, post.ID, []uint64{ownerTwo, ownerOne})

	for label, productID := range map[string]uint64{
		"cross shop": otherProduct,
		"off sale":   offSaleProduct,
		"sold out":   soldOutProduct,
	} {
		postCount, outboxCount := countRows(t, database, "posts"), countRows(t, database, "outbox_events")
		_, err := service.Publish(ctx, content.PublishInput{AuthorID: 1, Title: label, Body: "不可关联", ProductIDs: []uint64{productID}})
		if !errors.Is(err, content.ErrProductUnavailable) {
			t.Fatalf("Publish(%s) error = %v, want product unavailable", label, err)
		}
		if countRows(t, database, "posts") != postCount || countRows(t, database, "outbox_events") != outboxCount {
			t.Fatalf("Publish(%s) left post/outbox rows", label)
		}
	}
	if _, err := service.Publish(ctx, content.PublishInput{AuthorID: 1, Title: "重复", Body: "重复商品", ProductIDs: []uint64{ownerOne, ownerOne}}); !errors.Is(err, content.ErrPostValidation) {
		t.Fatalf("Publish(duplicate product ids) error = %v, want validation", err)
	}

	updated, err := service.Update(ctx, content.UpdateInput{
		AuthorID: 1, PostID: post.ID, Title: "合法更新", Body: "只保留一个商品", ProductIDs: []uint64{ownerOne},
	})
	if err != nil || updated.Title != "合法更新" || !reflect.DeepEqual(updated.ProductIDs, []uint64{ownerOne}) {
		t.Fatalf("Update(valid product) = %#v, %v", updated, err)
	}
	_, err = service.Update(ctx, content.UpdateInput{
		AuthorID: 1, PostID: post.ID, Title: "不应提交", Body: "跨店导致整个编辑回滚", ProductIDs: []uint64{otherProduct},
	})
	if !errors.Is(err, content.ErrProductUnavailable) {
		t.Fatalf("Update(cross shop) error = %v, want product unavailable", err)
	}
	afterFailedUpdate, err := service.Detail(ctx, post.ID)
	if err != nil || afterFailedUpdate.Title != "合法更新" || !reflect.DeepEqual(afterFailedUpdate.ProductIDs, []uint64{ownerOne}) {
		t.Fatalf("post after failed update = %#v, %v", afterFailedUpdate, err)
	}

	if _, err := database.Exec(`
CREATE TRIGGER fail_post_products_insert
BEFORE INSERT ON post_products
FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced post_products failure'`); err != nil {
		t.Fatalf("create post_products failure trigger: %v", err)
	}
	defer database.Exec("DROP TRIGGER IF EXISTS fail_post_products_insert")
	imageID := seedUnattachedImage(t, database, 1, "uploads/owner-1/product-rollback.jpg")
	postCount, outboxCount, tagCount := countRows(t, database, "posts"), countRows(t, database, "outbox_events"), countRows(t, database, "tags")
	_, err = service.Publish(ctx, content.PublishInput{
		AuthorID: 1, Title: "事务回滚 #productrollback", Body: "关系插入失败", ImageIDs: []uint64{imageID}, ProductIDs: []uint64{ownerOne},
	})
	if err == nil {
		t.Fatal("Publish(forced post_products failure) error = nil")
	}
	if countRows(t, database, "posts") != postCount || countRows(t, database, "outbox_events") != outboxCount || countRows(t, database, "tags") != tagCount {
		t.Fatal("forced post_products failure left post/outbox/tag rows")
	}
	var attached sql.NullInt64
	if err := database.QueryRow("SELECT post_id FROM post_images WHERE id=?", imageID).Scan(&attached); err != nil {
		t.Fatalf("query rollback image: %v", err)
	}
	if attached.Valid {
		t.Fatalf("rollback image attached to post %d", attached.Int64)
	}
}

func seedPostProductShop(t *testing.T, database *sql.DB, ownerID uint64, name, status string) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?,?,?)", ownerID, name, status)
	if err != nil {
		t.Fatalf("seed shop %s: %v", name, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func seedPostProductCatalogItem(t *testing.T, database *sql.DB, shopID uint64, name, productStatus, skuCode, skuStatus string, stock int64) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,?,'关联商品',?)", shopID, name, productStatus)
	if err != nil {
		t.Fatalf("seed product %s: %v", name, err)
	}
	productID, _ := result.LastInsertId()
	if _, err := database.Exec(`
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status)
VALUES (?, ?, JSON_OBJECT('规格','标准'), 1000, ?, ?)`, productID, skuCode, stock, skuStatus); err != nil {
		t.Fatalf("seed product SKU %s: %v", skuCode, err)
	}
	return uint64(productID)
}

func assertPostProductOrder(t *testing.T, database *sql.DB, postID uint64, want []uint64) {
	t.Helper()
	rows, err := database.Query("SELECT product_id,sort_order FROM post_products WHERE post_id=? ORDER BY sort_order", postID)
	if err != nil {
		t.Fatalf("query post product order: %v", err)
	}
	defer rows.Close()
	var got []uint64
	for rows.Next() {
		var productID uint64
		var order int
		if err := rows.Scan(&productID, &order); err != nil {
			t.Fatalf("scan post product order: %v", err)
		}
		if order != len(got) {
			t.Fatalf("post product sort_order = %d at index %d", order, len(got))
		}
		got = append(got, productID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post %d product order = %v, want %v", postID, got, want)
	}
}
