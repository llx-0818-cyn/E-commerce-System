package feed_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestFeedAssemblerBatchesOpenShopAndLimitedSellableProductCards(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Feed 商城批量富化集成测试")
	}
	database, cleanup := openFeedTestDatabase(t, adminDSN)
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
	createFollowingFixtures(t, database)
	base := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	openPost := seedCommerceFeedPost(t, database, 1, "开放店内容", base.Add(5*time.Second))
	noShopPost := seedCommerceFeedPost(t, database, 2, "无店内容", base.Add(4*time.Second))
	closedPost := seedCommerceFeedPost(t, database, 3, "闭店内容", base.Add(3*time.Second))
	_ = seedCommerceFeedPost(t, database, 1, "开放店第二内容", base.Add(2*time.Second))
	_ = seedCommerceFeedPost(t, database, 2, "无店第二内容", base.Add(time.Second))
	openShop := seedCommerceFeedShop(t, database, 1, "作者一开放店", "open")
	closedShop := seedCommerceFeedShop(t, database, 3, "作者三闭店", "closed")

	productA := seedCommerceFeedProduct(t, database, openShop, "卡片 A", "on_sale", 1000, 5)
	productB := seedCommerceFeedProduct(t, database, openShop, "已下架 B", "off_sale", 2000, 5)
	productC := seedCommerceFeedProduct(t, database, openShop, "卡片 C", "on_sale", 3000, 5)
	productD := seedCommerceFeedProduct(t, database, openShop, "卡片 D", "on_sale", 4000, 5)
	productE := seedCommerceFeedProduct(t, database, openShop, "超出上限 E", "on_sale", 5000, 5)
	closedProduct := seedCommerceFeedProduct(t, database, closedShop, "闭店商品", "on_sale", 6000, 5)
	for index, productID := range []uint64{productA, productB, productC, productD, productE} {
		if _, err := database.Exec("INSERT INTO post_products (post_id,product_id,sort_order) VALUES (?,?,?)", openPost, productID, index); err != nil {
			t.Fatalf("attach open post product %d: %v", index, err)
		}
	}
	if _, err := database.Exec("INSERT INTO post_products (post_id,product_id,sort_order) VALUES (?,?,0)", closedPost, closedProduct); err != nil {
		t.Fatalf("attach closed post product: %v", err)
	}

	counter := newSQLTraceCounter()
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{Logger: counter})
	if err != nil {
		t.Fatalf("open counted GORM database: %v", err)
	}
	feedRepository := feed.NewFeedRepository(gormDB)
	contentRepository, _ := content.NewRepository(gormDB)
	interactionRepository, _ := interaction.NewRepository(gormDB)
	interactionService, _ := interaction.NewService(interactionRepository)
	storefrontRepository, _ := storefront.NewRepository(gormDB)
	storefrontService, _ := storefront.NewService(storefrontRepository)
	assembler, err := feed.NewFeedAssembler(contentRepository, account.NewAccountRepository(gormDB), interactionService, storefrontService)
	if err != nil {
		t.Fatalf("NewFeedAssembler(commerce) error = %v", err)
	}

	counter.Reset()
	posts, err := feedRepository.ListPublishedPosts(ctx, 10, nil)
	if err != nil {
		t.Fatalf("ListPublishedPosts() error = %v", err)
	}
	feedItems, err := assembler.Assemble(ctx, posts, 4)
	if err != nil {
		t.Fatalf("Assemble(commerce) error = %v", err)
	}
	if got := counter.Count(); got != 7 {
		t.Fatalf("five-item commerce feed query count = %d, want 7 fixed queries", got)
	}
	openItem := findCommerceFeedItem(t, feedItems, openPost)
	if openItem.Shop == nil || openItem.Shop.ID != openShop || openItem.Shop.Name != "作者一开放店" {
		t.Fatalf("open post shop summary = %#v", openItem.Shop)
	}
	if got, want := feedProductCardIDs(openItem.Products), []uint64{productA, productC, productD}; !reflect.DeepEqual(got, want) {
		t.Fatalf("open post product cards = %v, want %v", got, want)
	}
	if openItem.Products[0].MinPriceCent != 1000 || openItem.Products[1].MinPriceCent != 3000 || openItem.Products[2].MinPriceCent != 4000 {
		t.Fatalf("open post product card prices = %#v", openItem.Products)
	}
	noShopItem := findCommerceFeedItem(t, feedItems, noShopPost)
	if noShopItem.Shop != nil || len(noShopItem.Products) != 0 {
		t.Fatalf("no-shop post commerce = shop:%#v products:%#v", noShopItem.Shop, noShopItem.Products)
	}
	closedItem := findCommerceFeedItem(t, feedItems, closedPost)
	if closedItem.Shop != nil || len(closedItem.Products) != 0 {
		t.Fatalf("closed-shop post commerce = shop:%#v products:%#v", closedItem.Shop, closedItem.Products)
	}

	counter.Reset()
	onePost, err := feedRepository.ListPublishedPosts(ctx, 1, nil)
	if err != nil {
		t.Fatalf("ListPublishedPosts(one) error = %v", err)
	}
	if _, err := assembler.Assemble(ctx, onePost, 4); err != nil {
		t.Fatalf("Assemble(one commerce) error = %v", err)
	}
	if got := counter.Count(); got != 7 {
		t.Fatalf("one-item commerce feed query count = %d, want same fixed 7", got)
	}
}

func seedCommerceFeedPost(t *testing.T, database *sql.DB, authorID uint64, title string, publishedAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at) VALUES (?,?,'商城富化','published',?)", authorID, title, publishedAt)
	if err != nil {
		t.Fatalf("seed commerce feed post %s: %v", title, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func seedCommerceFeedShop(t *testing.T, database *sql.DB, ownerID uint64, name, status string) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (?,?,?)", ownerID, name, status)
	if err != nil {
		t.Fatalf("seed commerce feed shop %s: %v", name, err)
	}
	id, _ := result.LastInsertId()
	return uint64(id)
}

func seedCommerceFeedProduct(t *testing.T, database *sql.DB, shopID uint64, name, status string, price, stock int64) uint64 {
	t.Helper()
	result, err := database.Exec("INSERT INTO products (shop_id,name,description,cover_url,status) VALUES (?,?,'卡片详情',?,?)", shopID, name, "/products/"+name+".jpg", status)
	if err != nil {
		t.Fatalf("seed commerce feed product %s: %v", name, err)
	}
	productID, _ := result.LastInsertId()
	if _, err := database.Exec(`
INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status)
VALUES (?, ?, JSON_OBJECT('规格','标准'), ?, ?, 'active')`, productID, fmt.Sprintf("FEED-SKU-%d", productID), price, stock); err != nil {
		t.Fatalf("seed commerce feed SKU %s: %v", name, err)
	}
	return uint64(productID)
}

func findCommerceFeedItem(t *testing.T, items []feed.ContentFeedItem, postID uint64) feed.ContentFeedItem {
	t.Helper()
	for _, item := range items {
		if item.ID == postID {
			return item
		}
	}
	t.Fatalf("post %d not found in commerce feed", postID)
	return feed.ContentFeedItem{}
}

func feedProductCardIDs(cards []storefront.FeedProductCard) []uint64 {
	ids := make([]uint64, len(cards))
	for index := range cards {
		ids[index] = cards[index].ID
	}
	return ids
}
