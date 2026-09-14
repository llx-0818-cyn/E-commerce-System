package checkout_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/checkout"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestBuildOrderDraftsSplitsTwoShopsAndUsesCurrentServerAmounts(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行跨店订单草稿集成测试")
	}
	database, cleanup := openCheckoutTestDatabase(t, adminDSN)
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
	repository, _ := checkout.NewRepository(gormDB)
	service, _ := checkout.NewService(repository)
	address, err := service.CreateAddress(ctx, 77, checkout.CreateAddressInput{AddressInput: validAddressInput("草稿买家", "草稿默认地址"), IsDefault: true})
	if err != nil {
		t.Fatalf("CreateAddress() error = %v", err)
	}
	shopOne, _, skuOne := seedPreviewSKU(t, database, 41, "草稿店一", "DRAFT-SKU-1", 1100, 10)
	shopTwo, _, skuTwo := seedPreviewSKU(t, database, 42, "草稿店二", "DRAFT-SKU-2", 2500, 10)
	mustAddPreviewCart(t, service, ctx, 77, skuOne, 2)
	mustAddPreviewCart(t, service, ctx, 77, skuTwo, 3)

	plan, err := service.BuildOrderDrafts(ctx, checkout.CheckoutPreviewInput{AccountID: 77, AddressID: address.ID})
	if err != nil {
		t.Fatalf("BuildOrderDrafts() error = %v", err)
	}
	if len(plan.Drafts) != 2 || plan.Drafts[0].ShopID != shopOne || plan.Drafts[1].ShopID != shopTwo || plan.GrandTotal != 9700 {
		t.Fatalf("two-shop order plan = %#v", plan)
	}
	if plan.Drafts[0].TotalCent != 2200 || plan.Drafts[1].TotalCent != 7500 || len(plan.Drafts[0].Items) != 1 || len(plan.Drafts[1].Items) != 1 {
		t.Fatalf("two-shop draft totals/items = %#v", plan.Drafts)
	}
	for _, draft := range plan.Drafts {
		if draft.BuyerID != 77 || draft.AddressSnapshot.AddressID != address.ID || draft.AddressSnapshot.Detail != "草稿默认地址" {
			t.Fatalf("draft buyer/address snapshot = %#v", draft)
		}
		for _, item := range draft.Items {
			if item.SubtotalCent != item.UnitPriceCent*int64(item.Quantity) {
				t.Fatalf("draft item amount = %#v", item)
			}
		}
	}

	if _, err := database.Exec("UPDATE product_skus SET price_cent=1300 WHERE id=?", skuOne); err != nil {
		t.Fatalf("change draft SKU price: %v", err)
	}
	if _, err := service.BuildOrderDrafts(ctx, checkout.CheckoutPreviewInput{AccountID: 77, AddressID: address.ID}); !errors.Is(err, checkout.ErrCheckoutNotReady) {
		t.Fatalf("BuildOrderDrafts(unaccepted price) error = %v, want not ready", err)
	}
	repriced, err := service.BuildOrderDrafts(ctx, checkout.CheckoutPreviewInput{AccountID: 77, AddressID: address.ID, AcceptPriceChanges: true})
	if err != nil || repriced.GrandTotal != 10100 || repriced.Drafts[0].TotalCent != 2600 {
		t.Fatalf("BuildOrderDrafts(accepted current price) = %#v, %v", repriced, err)
	}
	if _, err := database.Exec("UPDATE product_skus SET available_stock=1 WHERE id=?", skuTwo); err != nil {
		t.Fatalf("reduce draft SKU stock: %v", err)
	}
	if _, err := service.BuildOrderDrafts(ctx, checkout.CheckoutPreviewInput{AccountID: 77, AddressID: address.ID, AcceptPriceChanges: true}); !errors.Is(err, checkout.ErrCheckoutNotReady) {
		t.Fatalf("BuildOrderDrafts(insufficient stock) error = %v, want not ready", err)
	}
}
