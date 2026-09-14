package checkout_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/checkout"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAddressCRUDSingleDefaultSwitchAndOwnership(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行地址 Service 集成测试")
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

	addressOne, err := service.CreateAddress(ctx, 7, checkout.CreateAddressInput{
		AddressInput: validAddressInput("张三", "西湖路 1 号"), IsDefault: false,
	})
	if err != nil || addressOne.IsDefault {
		t.Fatalf("CreateAddress(one) = %#v, %v", addressOne, err)
	}
	addressTwo, err := service.CreateAddress(ctx, 7, checkout.CreateAddressInput{
		AddressInput: validAddressInput("李四", "西湖路 2 号"), IsDefault: true,
	})
	if err != nil || !addressTwo.IsDefault {
		t.Fatalf("CreateAddress(two default) = %#v, %v", addressTwo, err)
	}
	assertDefaultAddressCount(t, database, 7, addressTwo.ID)

	defaultOne, err := service.SetDefaultAddress(ctx, 7, addressOne.ID)
	if err != nil || !defaultOne.IsDefault {
		t.Fatalf("SetDefaultAddress(one) = %#v, %v", defaultOne, err)
	}
	assertDefaultAddressCount(t, database, 7, addressOne.ID)
	loadedDefault, err := service.GetDefaultAddress(ctx, 7)
	if err != nil || loadedDefault.ID != addressOne.ID {
		t.Fatalf("GetDefaultAddress() = %#v, %v", loadedDefault, err)
	}

	updatedInput := validAddressInput("张三（新）", "文三路 99 号")
	updated, err := service.UpdateAddress(ctx, 7, addressOne.ID, updatedInput)
	if err != nil || updated.RecipientName != "张三（新）" || updated.Detail != "文三路 99 号" || !updated.IsDefault {
		t.Fatalf("UpdateAddress(owner) = %#v, %v", updated, err)
	}
	if _, err := service.UpdateAddress(ctx, 8, addressOne.ID, validAddressInput("越权", "越权地址")); !errors.Is(err, checkout.ErrAddressNotFound) {
		t.Fatalf("UpdateAddress(other) error = %v, want not found", err)
	}
	if _, err := service.SetDefaultAddress(ctx, 8, addressOne.ID); !errors.Is(err, checkout.ErrAddressNotFound) {
		t.Fatalf("SetDefaultAddress(other) error = %v, want not found", err)
	}
	if err := service.DeleteAddress(ctx, 8, addressOne.ID); !errors.Is(err, checkout.ErrAddressNotFound) {
		t.Fatalf("DeleteAddress(other) error = %v, want not found", err)
	}
	otherAddresses, err := service.ListAddresses(ctx, 8)
	if err != nil || len(otherAddresses) != 0 {
		t.Fatalf("ListAddresses(other) = %#v, %v", otherAddresses, err)
	}

	addressThree, err := service.CreateAddress(ctx, 7, checkout.CreateAddressInput{
		AddressInput: validAddressInput("王五", "滨江路 3 号"), IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateAddress(third default) error = %v", err)
	}
	assertDefaultAddressCount(t, database, 7, addressThree.ID)
	addresses, err := service.ListAddresses(ctx, 7)
	if err != nil || len(addresses) != 3 || addresses[0].ID != addressThree.ID || !addresses[0].IsDefault {
		t.Fatalf("ListAddresses(owner) = %#v, %v", addresses, err)
	}
	if err := service.DeleteAddress(ctx, 7, addressThree.ID); err != nil {
		t.Fatalf("DeleteAddress(default owner) error = %v", err)
	}
	if _, err := service.GetDefaultAddress(ctx, 7); !errors.Is(err, checkout.ErrAddressNotFound) {
		t.Fatalf("GetDefaultAddress(after delete) error = %v, want not found", err)
	}
	if _, err := service.SetDefaultAddress(ctx, 7, addressOne.ID); err != nil {
		t.Fatalf("restore default address: %v", err)
	}
	if err := service.DeleteAddress(ctx, 7, addressTwo.ID); err != nil {
		t.Fatalf("DeleteAddress(non-default owner) error = %v", err)
	}
	addresses, err = service.ListAddresses(ctx, 7)
	if err != nil || len(addresses) != 1 || addresses[0].ID != addressOne.ID || !addresses[0].IsDefault {
		t.Fatalf("ListAddresses(final) = %#v, %v", addresses, err)
	}

	for label, input := range map[string]checkout.CreateAddressInput{
		"blank recipient": {AddressInput: checkout.AddressInput{Phone: "1", Province: "省", City: "市", Detail: "地址"}},
		"long detail":     {AddressInput: validAddressInput("合法名", strings.Repeat("地", 501))},
	} {
		if _, err := service.CreateAddress(ctx, 9, input); !errors.Is(err, checkout.ErrAddressValidation) {
			t.Fatalf("CreateAddress(%s) error = %v, want validation", label, err)
		}
	}
}

func validAddressInput(name, detail string) checkout.AddressInput {
	return checkout.AddressInput{
		RecipientName: name,
		Phone:         "13800000000",
		Province:      "浙江省",
		City:          "杭州市",
		District:      "西湖区",
		Detail:        detail,
		PostalCode:    "310000",
	}
}

func assertDefaultAddressCount(t *testing.T, database *sql.DB, accountID, wantAddressID uint64) {
	t.Helper()
	var count int
	var addressID uint64
	if err := database.QueryRow("SELECT COUNT(*),COALESCE(MAX(id),0) FROM addresses WHERE account_id=? AND is_default=TRUE", accountID).Scan(&count, &addressID); err != nil {
		t.Fatalf("query default address: %v", err)
	}
	if count != 1 || addressID != wantAddressID {
		t.Fatalf("default address count/id = %d/%d, want 1/%d", count, addressID, wantAddressID)
	}
}
