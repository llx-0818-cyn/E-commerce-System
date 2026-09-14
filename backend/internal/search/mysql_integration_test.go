package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLSearchMatchesContentTagsProductsShopsAndPaginatesWithoutElasticsearch(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 MySQL 基础搜索集成测试")
	}
	database, cleanup := openSearchTestDatabase(t, adminDSN)
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
	seedSearchData(t, database)
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := NewRepository(gormDB)
	service, err := NewService(repository, config.SearchConfig{Provider: "mysql"})
	if err != nil {
		t.Fatalf("NewService(mysql without Elasticsearch) error = %v", err)
	}

	for keyword, wantTitle := range map[string]string{
		"露营":   "露营清单",
		"手冲咖啡": "正文命中",
		"器具":   "标签命中",
	} {
		page, err := service.Search(ctx, Query{Keyword: keyword, Scope: ScopeContent, Limit: 10})
		if err != nil || len(page.Items) != 1 || page.Items[0].Title != wantTitle || page.Items[0].Type != ScopeContent {
			t.Fatalf("content search %q = %#v, %v", keyword, page, err)
		}
	}
	productPage, err := service.Search(ctx, Query{Keyword: "咖啡杯", Scope: ScopeProduct, Limit: 10})
	if err != nil || len(productPage.Items) != 1 || productPage.Items[0].Title != "手冲咖啡杯" || productPage.Items[0].PriceCent == nil || *productPage.Items[0].PriceCent != 6900 {
		t.Fatalf("product search = %#v, %v", productPage, err)
	}
	shopPage, err := service.Search(ctx, Query{Keyword: "星光", Scope: ScopeShop, Limit: 10})
	if err != nil || len(shopPage.Items) != 1 || shopPage.Items[0].Title != "星光生活店" {
		t.Fatalf("shop search = %#v, %v", shopPage, err)
	}

	first, err := service.Search(ctx, Query{Keyword: "分页词", Scope: ScopeContent, Limit: 2, Offset: 0})
	if err != nil || !first.HasMore || first.NextOffset == nil || *first.NextOffset != 2 {
		t.Fatalf("first search page = %#v, %v", first, err)
	}
	second, err := service.Search(ctx, Query{Keyword: "分页词", Scope: ScopeContent, Limit: 2, Offset: *first.NextOffset})
	if err != nil || second.HasMore || len(second.Items) != 1 {
		t.Fatalf("second search page = %#v, %v", second, err)
	}
	allIDs := []uint64{first.Items[0].ID, first.Items[1].ID, second.Items[0].ID}
	if want := []uint64{6, 5, 4}; !reflect.DeepEqual(allIDs, want) {
		t.Fatalf("paged content IDs = %v, want %v", allIDs, want)
	}
	if _, err := service.Search(ctx, Query{Keyword: "x", Scope: ScopeContent, Limit: 10, Offset: 10_001}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("unbounded offset error = %v, want invalid query", err)
	}
	categoryID := uint64(1)
	if _, err := service.Search(ctx, Query{Keyword: "咖啡杯", Scope: ScopeProduct, Limit: 10, CategoryID: &categoryID}); !errors.Is(err, ErrEnhancedSearchRequired) {
		t.Fatalf("MySQL advanced filter error = %v, want enhanced search required", err)
	}
}

func seedSearchData(t *testing.T, database *sql.DB) {
	t.Helper()
	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,description,status) VALUES (1,'星光生活店','公开店铺','open')")
	if err != nil {
		t.Fatalf("seed public shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	disabledShopResult, _ := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (2,'星光禁用店','disabled')")
	disabledShopID, _ := disabledShopResult.LastInsertId()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,subtitle,description,cover_url,status) VALUES (?,'手冲咖啡杯','耐热玻璃','商品详情','/cup.jpg','on_sale')", shopID)
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	_, _ = database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'SEARCH-CUP',JSON_OBJECT(),6900,5,'active')", productID)
	_, _ = database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'咖啡杯下架','详情','off_sale')", shopID)
	_, _ = database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'咖啡杯禁用店商品','详情','on_sale')", disabledShopID)

	published := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	postIDs := make([]int64, 0, 6)
	for _, value := range []struct{ title, body, status string }{
		{"露营清单", "户外正文", "published"},
		{"正文命中", "这里介绍手冲咖啡", "published"},
		{"标签命中", "普通正文", "published"},
		{"分页词一", "分页词", "published"},
		{"分页词二", "分页词", "published"},
		{"分页词三", "分页词", "published"},
	} {
		result, err := database.Exec("INSERT INTO posts (author_id,title,body,status,published_at) VALUES (1,?,?,?,?)", value.title, value.body, value.status, published)
		if err != nil {
			t.Fatalf("seed post %q: %v", value.title, err)
		}
		id, _ := result.LastInsertId()
		postIDs = append(postIDs, id)
	}
	tagResult, _ := database.Exec("INSERT INTO tags (name) VALUES ('器具')")
	tagID, _ := tagResult.LastInsertId()
	_, _ = database.Exec("INSERT INTO post_tags (post_id,tag_id) VALUES (?,?)", postIDs[2], tagID)
	_, _ = database.Exec("INSERT INTO posts (author_id,title,body,status) VALUES (1,'露营隐藏内容','不应命中','hidden')")
}

func openSearchTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_search_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create search database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open search database: %v", err)
	}
	return database, func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop search database: %v", err)
		}
		adminDB.Close()
	}
}
