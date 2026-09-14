package migration

import (
	"errors"
	"feedsystem_video_go/migrations"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsLoad(t *testing.T) {
	items, err := Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(embedded) error = %v", err)
	}
	if len(items) != 13 || items[0].Version != 1 || items[0].Name != "legacy_schema_baseline" || items[1].Version != 2 || items[1].Name != "create_reliable_event_tables" || items[2].Version != 3 || items[2].Name != "create_content_tables" || items[3].Version != 4 || items[3].Name != "adopt_tags_for_content" || items[4].Version != 5 || items[4].Name != "create_content_interaction_tables" || items[5].Version != 6 || items[5].Name != "create_content_notifications" || items[6].Version != 7 || items[6].Name != "create_storefront_catalog" || items[7].Version != 8 || items[7].Name != "create_post_products" || items[8].Version != 9 || items[8].Name != "create_cart_addresses" || items[9].Version != 10 || items[9].Name != "add_cart_observed_price" || items[10].Version != 11 || items[10].Name != "create_orders_inventory" || items[11].Version != 12 || items[11].Name != "create_mock_payments" || items[12].Version != 13 || items[12].Name != "create_flash_sales" {
		t.Fatalf("embedded migrations = %#v", items)
	}
	if items[0].DownSQL == "" {
		t.Fatal("baseline migration must be reversible without deleting legacy tables")
	}
}

func TestLoadSortsAndChecksumsMigrations(t *testing.T) {
	source := fstest.MapFS{
		"000002_second.up.sql":  {Data: []byte("ALTER TABLE example ADD COLUMN note VARCHAR(32);")},
		"000001_first.down.sql": {Data: []byte("DROP TABLE example;")},
		"000001_first.up.sql":   {Data: []byte("CREATE TABLE example (id BIGINT PRIMARY KEY);")},
	}

	items, err := Load(source)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Load() count = %d, want 2", len(items))
	}
	if items[0].Version != 1 || items[0].Name != "first" || items[0].DownSQL == "" {
		t.Fatalf("first migration = %#v", items[0])
	}
	if items[1].Version != 2 || items[1].Name != "second" || items[1].DownSQL != "" {
		t.Fatalf("second migration = %#v", items[1])
	}
	if items[0].Checksum == "" || items[0].Checksum == items[1].Checksum {
		t.Fatalf("unexpected checksums: %q %q", items[0].Checksum, items[1].Checksum)
	}
}

func TestLoadRejectsDownWithoutUp(t *testing.T) {
	_, err := Load(fstest.MapFS{
		"000001_missing_up.down.sql": {Data: []byte("SELECT 1;")},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want missing up error")
	}
}

func TestValidateAppliedRejectsDirtyAndChecksumDrift(t *testing.T) {
	source := []Migration{{Version: 1, Name: "first", Checksum: "expected"}}

	err := validateApplied(source, map[uint64]AppliedMigration{
		1: {Version: 1, Name: "first", Checksum: "expected", Dirty: true},
	})
	if !errors.Is(err, ErrDirtyMigration) {
		t.Fatalf("dirty error = %v, want ErrDirtyMigration", err)
	}

	err = validateApplied(source, map[uint64]AppliedMigration{
		1: {Version: 1, Name: "first", Checksum: "changed"},
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("checksum error = %v, want ErrChecksumMismatch", err)
	}
}
