package storefront_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestProductRelatedPublicPostsDeletionOffSaleAndStablePagination(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行商品相关内容反向分页测试")
	}
	database, cleanup := openStorefrontTestDatabase(t, adminDSN)
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
	shop := seedPublicShop(t, database, 21, "反向内容店", "open")
	categoryResult, err := database.Exec("INSERT INTO categories (name,slug) VALUES ('反向内容','related-content')")
	if err != nil {
		t.Fatalf("seed related content category: %v", err)
	}
	categoryID, _ := categoryResult.LastInsertId()
	product := seedPublicProduct(t, database, shop, uint64(categoryID), "反向内容商品", "on_sale", time.Now().UTC())
	seedPublicSKU(t, database, product, "RELATED-SKU", `{"规格":"标准"}`, 9900, 10, "active")
	base := time.Date(2026, time.August, 24, 13, 0, 0, 0, time.UTC)
	postA := seedRelatedPost(t, database, 21, "A", base.Add(time.Minute))
	postB := seedRelatedPost(t, database, 21, "B", base.Add(2*time.Minute))
	postC := seedRelatedPost(t, database, 21, "C", base.Add(3*time.Minute))
	postD := seedRelatedPost(t, database, 21, "D", base.Add(3*time.Minute))
	for _, postID := range []uint64{postA, postB, postC, postD} {
		if _, err := database.Exec("INSERT INTO post_products (post_id,product_id,sort_order) VALUES (?,?,0)", postID, product); err != nil {
			t.Fatalf("attach related post %d: %v", postID, err)
		}
	}

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := storefront.NewRepository(gormDB)
	service, _ := storefront.NewService(repository)
	detail, err := service.GetPublicProduct(ctx, product)
	if err != nil || detail.RelatedPostsURL != "/api/products/"+strconv.FormatUint(product, 10)+"/posts" {
		t.Fatalf("product detail related entry = %#v, %v", detail, err)
	}

	first, err := service.ListRelatedPublicPosts(ctx, product, 2, nil)
	if err != nil {
		t.Fatalf("ListRelatedPublicPosts(first) error = %v", err)
	}
	if got, want := relatedPostIDs(first), []uint64{postD, postC}; !reflect.DeepEqual(got, want) || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("first related page = %v hasMore=%v cursor=%#v, want %v", got, first.HasMore, first.NextCursor, want)
	}
	deletedAt := time.Now().UTC()
	if _, err := database.Exec("UPDATE posts SET status='deleted', published_at=NULL, deleted_at=? WHERE id=?", deletedAt, postB); err != nil {
		t.Fatalf("soft delete related post B: %v", err)
	}
	second, err := service.ListRelatedPublicPosts(ctx, product, 2, first.NextCursor)
	if err != nil {
		t.Fatalf("ListRelatedPublicPosts(second) error = %v", err)
	}
	if got, want := relatedPostIDs(second), []uint64{postA}; !reflect.DeepEqual(got, want) || second.HasMore {
		t.Fatalf("second related page after deletion = %v hasMore=%v, want %v", got, second.HasMore, want)
	}
	seen := map[uint64]bool{}
	for _, id := range append(relatedPostIDs(first), relatedPostIDs(second)...) {
		if seen[id] {
			t.Fatalf("duplicate related post %d across pages", id)
		}
		seen[id] = true
	}
	for _, post := range append(first.Posts, second.Posts...) {
		if !reflect.DeepEqual(post.ProductIDs, []uint64{product}) || post.Images == nil || post.Tags == nil {
			t.Fatalf("related post enrichment = %#v", post)
		}
	}

	if _, err := database.Exec("UPDATE products SET status='off_sale' WHERE id=?", product); err != nil {
		t.Fatalf("off sale related product: %v", err)
	}
	if _, err := service.ListRelatedPublicPosts(ctx, product, 2, nil); !errors.Is(err, storefront.ErrProductNotFound) {
		t.Fatalf("ListRelatedPublicPosts(off sale) error = %v, want not found", err)
	}
}

func seedRelatedPost(t *testing.T, database *sql.DB, authorID uint64, title string, publishedAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at) VALUES (?,?,'商品反向内容','published',?)", authorID, title, publishedAt)
	if err != nil {
		t.Fatalf("seed related post %s: %v", title, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func relatedPostIDs(page storefront.RelatedPostPage) []uint64 {
	ids := make([]uint64, len(page.Posts))
	for index := range page.Posts {
		ids[index] = page.Posts[index].ID
	}
	return ids
}
