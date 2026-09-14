package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
)

func TestCartAddressMigrationAndConcurrentUniqueConstraints(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行购物车地址 Migration 集成测试")
	}
	database, schema, cleanup := openTemporaryDatabase(t, adminDSN, "cart_addresses")
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
	assertIndex(t, database, schema, "cart_items", "uq_cart_items_account_sku", true, []string{"account_id", "sku_id"})
	assertIndex(t, database, schema, "cart_items", "idx_cart_items_account_page", false, []string{"account_id", "updated_at", "id"})
	assertIndex(t, database, schema, "addresses", "uq_addresses_single_default", true, []string{"default_account_id"})
	assertIndex(t, database, schema, "addresses", "idx_addresses_account_page", false, []string{"account_id", "updated_at", "id"})
	assertGeneratedColumn(t, database, schema, "addresses", "default_account_id")

	shopResult, err := database.Exec("INSERT INTO shops (owner_id,name,status) VALUES (1,'购物车店','open')")
	if err != nil {
		t.Fatalf("seed shop: %v", err)
	}
	shopID, _ := shopResult.LastInsertId()
	productResult, err := database.Exec("INSERT INTO products (shop_id,name,description,status) VALUES (?,'购物车商品','详情','on_sale')", shopID)
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := database.Exec("INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,available_stock,status) VALUES (?,'CART-SKU',JSON_OBJECT(),1000,10,'active')", productID)
	if err != nil {
		t.Fatalf("seed SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()

	cartErrors := runConcurrentInserts(2, func(index int) error {
		_, err := database.Exec("INSERT INTO cart_items (account_id,sku_id,quantity) VALUES (7,?,?)", skuID, index+1)
		return err
	})
	assertOneSuccessOneDuplicate(t, cartErrors, "concurrent cart account/SKU")
	var cartCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM cart_items WHERE account_id=7 AND sku_id=?", skuID).Scan(&cartCount); err != nil || cartCount != 1 {
		t.Fatalf("cart unique row count = %d, %v; want 1", cartCount, err)
	}
	_, err = database.Exec("INSERT INTO cart_items (account_id,sku_id,quantity) VALUES (8,?,0)", skuID)
	assertMySQLCode(t, err, 3819, "cart quantity check")

	addressErrors := runConcurrentInserts(2, func(index int) error {
		_, err := database.Exec(`
INSERT INTO addresses
    (account_id,recipient_name,phone,province,city,district,detail,is_default)
VALUES (9, ?, '13800000000', '浙江省', '杭州市', '西湖区', ?, TRUE)`,
			fmt.Sprintf("收件人%d", index), fmt.Sprintf("并发地址%d", index))
		return err
	})
	assertOneSuccessOneDuplicate(t, addressErrors, "concurrent single default address")
	if _, err := database.Exec(`
INSERT INTO addresses (account_id,recipient_name,phone,province,city,detail,is_default)
VALUES (9,'非默认一','13800000001','浙江省','杭州市','地址一',FALSE),
       (9,'非默认二','13800000002','浙江省','杭州市','地址二',FALSE)`); err != nil {
		t.Fatalf("insert multiple non-default addresses: %v", err)
	}
	var defaultCount, addressCount int
	if err := database.QueryRow("SELECT SUM(is_default),COUNT(*) FROM addresses WHERE account_id=9").Scan(&defaultCount, &addressCount); err != nil {
		t.Fatalf("count addresses: %v", err)
	}
	if defaultCount != 1 || addressCount != 3 {
		t.Fatalf("address counts default/all = %d/%d, want 1/3", defaultCount, addressCount)
	}

	rollbackCount := migrationCountFrom(items, 9)
	if rolledBack, err := runner.Down(ctx, rollbackCount); err != nil || rolledBack != rollbackCount {
		t.Fatalf("Down(%d) = %d, %v; want %d, nil", rollbackCount, rolledBack, err, rollbackCount)
	}
	assertTablesAbsent(t, database, schema, []string{"cart_items", "addresses"})
}

func runConcurrentInserts(count int, insert func(int) error) []error {
	start := make(chan struct{})
	errorsByIndex := make([]error, count)
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			errorsByIndex[index] = insert(index)
		}(index)
	}
	close(start)
	wait.Wait()
	return errorsByIndex
}

func assertOneSuccessOneDuplicate(t *testing.T, results []error, label string) {
	t.Helper()
	successes, duplicates := 0, 0
	for _, err := range results {
		if err == nil {
			successes++
			continue
		}
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			duplicates++
			continue
		}
		t.Fatalf("%s unexpected error: %v", label, err)
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("%s successes/duplicates = %d/%d, want 1/1", label, successes, duplicates)
	}
}

func assertGeneratedColumn(t *testing.T, database *sql.DB, schema, table, column string) {
	t.Helper()
	var extra, expression string
	if err := database.QueryRow(`
SELECT extra,generation_expression FROM information_schema.columns
WHERE table_schema=? AND table_name=? AND column_name=?`, schema, table, column).Scan(&extra, &expression); err != nil {
		t.Fatalf("query generated column %s.%s: %v", table, column, err)
	}
	if extra != "STORED GENERATED" || expression == "" {
		t.Fatalf("generated column %s.%s extra=%q expression=%q", table, column, extra, expression)
	}
}
