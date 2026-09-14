package storefront_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestShopCreateUpdateOpenCloseOwnershipAndDisabledState(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行店铺 Service 集成测试")
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, err := storefront.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	service, err := storefront.NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	shop, err := service.CreateShop(ctx, 1, storefront.ShopProfileInput{
		Name: "  Alice 的店  ", Description: "  初始简介  ", AvatarURL: " /avatar.jpg ", BannerURL: " /banner.jpg ",
	})
	if err != nil {
		t.Fatalf("CreateShop() error = %v", err)
	}
	if shop.ID == 0 || shop.OwnerID != 1 || shop.Name != "Alice 的店" || shop.Description != "初始简介" || shop.Status != storefront.ShopStatusClosed {
		t.Fatalf("created shop = %#v", shop)
	}
	existing, err := service.CreateShop(ctx, 1, storefront.ShopProfileInput{Name: "第二家店"})
	if !errors.Is(err, storefront.ErrShopAlreadyExists) || existing == nil || existing.ID != shop.ID {
		t.Fatalf("duplicate CreateShop() = %#v, %v; want existing shop %d", existing, err, shop.ID)
	}
	var duplicateError *storefront.ShopAlreadyExistsError
	if !errors.As(err, &duplicateError) || duplicateError.ShopID != shop.ID {
		t.Fatalf("duplicate shop error = %#v, want shop_id %d", duplicateError, shop.ID)
	}
	assertShopCount(t, database, 1, 1)

	other, err := service.CreateShop(ctx, 2, storefront.ShopProfileInput{Name: "Bob 的店"})
	if err != nil {
		t.Fatalf("CreateShop(other) error = %v", err)
	}
	if _, err := service.UpdateShop(ctx, 2, shop.ID, storefront.ShopProfileInput{Name: "越权改名"}); !errors.Is(err, storefront.ErrShopForbidden) {
		t.Fatalf("UpdateShop(other owner) error = %v, want forbidden", err)
	}
	assertShopName(t, database, shop.ID, "Alice 的店")

	updated, err := service.UpdateShop(ctx, 1, shop.ID, storefront.ShopProfileInput{
		Name: "Alice 精选", Description: "更新简介", AvatarURL: "/new-avatar.jpg", BannerURL: "/new-banner.jpg",
	})
	if err != nil || updated.Name != "Alice 精选" || updated.Description != "更新简介" {
		t.Fatalf("UpdateShop(owner) = %#v, %v", updated, err)
	}
	opened, err := service.OpenShop(ctx, 1, shop.ID)
	if err != nil || opened.Status != storefront.ShopStatusOpen {
		t.Fatalf("OpenShop() = %#v, %v", opened, err)
	}
	openedAgain, err := service.OpenShop(ctx, 1, shop.ID)
	if err != nil || openedAgain.Status != storefront.ShopStatusOpen {
		t.Fatalf("OpenShop(idempotent) = %#v, %v", openedAgain, err)
	}
	if _, err := service.CloseShop(ctx, 2, shop.ID); !errors.Is(err, storefront.ErrShopForbidden) {
		t.Fatalf("CloseShop(other owner) error = %v, want forbidden", err)
	}
	closed, err := service.CloseShop(ctx, 1, shop.ID)
	if err != nil || closed.Status != storefront.ShopStatusClosed {
		t.Fatalf("CloseShop() = %#v, %v", closed, err)
	}
	closedAgain, err := service.CloseShop(ctx, 1, shop.ID)
	if err != nil || closedAgain.Status != storefront.ShopStatusClosed {
		t.Fatalf("CloseShop(idempotent) = %#v, %v", closedAgain, err)
	}

	if _, err := database.ExecContext(ctx, "UPDATE shops SET status='disabled' WHERE id=?", shop.ID); err != nil {
		t.Fatalf("disable shop fixture: %v", err)
	}
	if _, err := service.OpenShop(ctx, 1, shop.ID); !errors.Is(err, storefront.ErrShopDisabled) {
		t.Fatalf("OpenShop(disabled) error = %v, want disabled", err)
	}
	assertShopStatus(t, database, shop.ID, storefront.ShopStatusDisabled)
	if _, err := service.CloseShop(ctx, 2, other.ID); err != nil {
		t.Fatalf("owner closes second shop: %v", err)
	}

	if _, err := service.CreateShop(ctx, 3, storefront.ShopProfileInput{Name: "   "}); !errors.Is(err, storefront.ErrShopValidation) {
		t.Fatalf("CreateShop(blank name) error = %v, want validation", err)
	}
	if _, err := service.UpdateShop(ctx, 2, other.ID, storefront.ShopProfileInput{Name: "ok", Description: strings.Repeat("文", 1001)}); !errors.Is(err, storefront.ErrShopValidation) {
		t.Fatalf("UpdateShop(long description) error = %v, want validation", err)
	}
}

func openStorefrontTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
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
	databaseName := fmt.Sprintf("feedsystem_storefront_test_%d", time.Now().UnixNano())
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

func assertShopCount(t *testing.T, database *sql.DB, ownerID uint64, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM shops WHERE owner_id=?", ownerID).Scan(&count); err != nil {
		t.Fatalf("count shops: %v", err)
	}
	if count != want {
		t.Fatalf("shop count for owner %d = %d, want %d", ownerID, count, want)
	}
}

func assertShopName(t *testing.T, database *sql.DB, shopID uint64, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow("SELECT name FROM shops WHERE id=?", shopID).Scan(&got); err != nil {
		t.Fatalf("query shop name: %v", err)
	}
	if got != want {
		t.Fatalf("shop %d name = %q, want %q", shopID, got, want)
	}
}

func assertShopStatus(t *testing.T, database *sql.DB, shopID uint64, want storefront.ShopStatus) {
	t.Helper()
	var got string
	if err := database.QueryRow("SELECT status FROM shops WHERE id=?", shopID).Scan(&got); err != nil {
		t.Fatalf("query shop status: %v", err)
	}
	if storefront.ShopStatus(got) != want {
		t.Fatalf("shop %d status = %q, want %q", shopID, got, want)
	}
}
