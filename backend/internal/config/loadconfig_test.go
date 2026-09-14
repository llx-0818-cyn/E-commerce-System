package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var configEnvironmentVariables = []string{
	"APP_ENV",
	"SERVER_PORT",
	"MYSQL_HOST",
	"MYSQL_PORT",
	"MYSQL_USER",
	"MYSQL_ROOT_PASSWORD",
	"MYSQL_PASSWORD",
	"MYSQL_DATABASE",
	"REDIS_HOST",
	"REDIS_PORT",
	"REDIS_PASSWORD",
	"REDIS_DB",
	"RABBITMQ_HOST",
	"RABBITMQ_PORT",
	"RABBITMQ_USER",
	"RABBITMQ_PASS",
	"CONTENT_MAX_IMAGES",
	"CONTENT_MAX_IMAGE_SIZE_BYTES",
	"CONTENT_MAX_BODY_LENGTH",
	"ORDER_PAYMENT_VALID_FOR",
	"FLASH_SALE_PAYMENT_VALID_FOR",
	"POPULARITY_LIKE_WEIGHT",
	"POPULARITY_FAVORITE_WEIGHT",
	"POPULARITY_COMMENT_WEIGHT",
	"POPULARITY_SHOP_VISIT_WEIGHT",
	"POPULARITY_PRODUCT_CLICK_WEIGHT",
	"POPULARITY_PAID_ORDER_WEIGHT",
	"POPULARITY_PAID_ORDER_CONTRIBUTION_CAP",
	"POPULARITY_DECAY_HALF_LIFE",
	"MOCK_PAYMENT_ROUTES_ENABLED",
	"MOCK_PAYMENT_CALLBACK_SECRET",
	"SEARCH_PROVIDER",
	"ELASTICSEARCH_ADDRESSES",
	"ELASTICSEARCH_USERNAME",
	"ELASTICSEARCH_PASSWORD",
	"ELASTICSEARCH_INDEX_PREFIX",
	"ELASTICSEARCH_REQUEST_TIMEOUT",
}

func TestDefaultLocalConfigCommerceDefaults(t *testing.T) {
	clearConfigEnvironment(t)
	cfg := DefaultLocalConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config Validate() error = %v", err)
	}
	if cfg.Runtime.Environment != "development" {
		t.Fatalf("environment = %q", cfg.Runtime.Environment)
	}
	if cfg.Content.MaxImages != 9 || cfg.Content.MaxImageSizeBytes != 10*1024*1024 || cfg.Content.MaxBodyLength != 10_000 {
		t.Fatalf("content defaults = %#v", cfg.Content)
	}
	if cfg.Commerce.OrderPaymentValidFor.Value() != 30*time.Minute || cfg.Commerce.FlashSalePaymentValidFor.Value() != 10*time.Minute {
		t.Fatalf("commerce defaults = %#v", cfg.Commerce)
	}
	if cfg.Popularity.PaidOrderWeight != 8 || cfg.Popularity.PaidOrderContributionCap != 24 || cfg.Popularity.DecayHalfLife.Value() != 24*time.Hour {
		t.Fatalf("popularity defaults = %#v", cfg.Popularity)
	}
	if !cfg.MockPayment.RoutesEnabled || len(cfg.MockPayment.CallbackSecret) < 16 || cfg.Search.Provider != "mysql" || len(cfg.Search.Elasticsearch.Addresses) != 0 {
		t.Fatalf("mock/search defaults = %#v / %#v", cfg.MockPayment, cfg.Search)
	}
}

func TestRepositoryConfigFilesLoad(t *testing.T) {
	clearConfigEnvironment(t)
	for _, filename := range []string{"config.yaml", "config.compose-local.yaml", "config.docker.yaml"} {
		t.Run(filename, func(t *testing.T) {
			cfg, err := Load(filepath.Join("..", "..", "configs", filename))
			if err != nil {
				t.Fatalf("Load(%s) error = %v", filename, err)
			}
			if cfg.Search.Provider != "mysql" || cfg.Content.MaxImages != 9 {
				t.Fatalf("Load(%s) new config = %#v", filename, cfg)
			}
		})
	}
}

func TestLoadPartialConfigKeepsDocumentedDefaults(t *testing.T) {
	clearConfigEnvironment(t)
	filename := writeConfig(t, "server:\n  port: 9090\n")
	cfg, err := Load(filename)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != 9090 || cfg.Content.MaxImages != 9 || cfg.Search.Provider != "mysql" {
		t.Fatalf("partial config = %#v", cfg)
	}
}

func TestLoadRejectsInvalidCommerceConfig(t *testing.T) {
	clearConfigEnvironment(t)
	tests := []struct {
		name    string
		yaml    string
		message string
	}{
		{"zero image count", "content:\n  max_images: 0\n", "content.max_images"},
		{"oversized image", "content:\n  max_image_size_bytes: 52428801\n", "content.max_image_size_bytes"},
		{"zero body length", "content:\n  max_body_length: 0\n", "content.max_body_length"},
		{"invalid duration", "commerce:\n  order_payment_valid_for: never\n", "parse config"},
		{"flash ttl exceeds normal", "commerce:\n  order_payment_valid_for: 5m\n  flash_sale_payment_valid_for: 10m\n", "flash_sale_payment_valid_for"},
		{"negative weight", "popularity:\n  like_weight: -1\n", "like_weight"},
		{"zero interaction weights", "popularity:\n  like_weight: 0\n  favorite_weight: 0\n  comment_weight: 0\n", "至少一个内容互动权重"},
		{"paid cap below one event", "popularity:\n  paid_order_weight: 8\n  paid_order_contribution_cap: 7\n", "paid_order_contribution_cap"},
		{"mock routes in production", "runtime:\n  environment: production\nmock_payment:\n  routes_enabled: true\n", "production 环境禁止"},
		{"short mock callback secret", "mock_payment:\n  callback_secret: short\n", "callback_secret"},
		{"unknown search provider", "search:\n  provider: magic\n", "search.provider"},
		{"elasticsearch without address", "search:\n  provider: elasticsearch\n  elasticsearch:\n    addresses: []\n", "必须配置"},
		{"invalid elasticsearch address", "search:\n  provider: elasticsearch\n  elasticsearch:\n    addresses: [redis://localhost:6379]\n", "无效 Elasticsearch 地址"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Load() error = %v, want message containing %q", err, test.message)
			}
		})
	}
}

func TestApplyEnvOverridesForCommerceAndSearch(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("CONTENT_MAX_IMAGES", "6")
	t.Setenv("CONTENT_MAX_IMAGE_SIZE_BYTES", "2097152")
	t.Setenv("CONTENT_MAX_BODY_LENGTH", "5000")
	t.Setenv("ORDER_PAYMENT_VALID_FOR", "20m")
	t.Setenv("FLASH_SALE_PAYMENT_VALID_FOR", "5m")
	t.Setenv("POPULARITY_PAID_ORDER_WEIGHT", "9.5")
	t.Setenv("POPULARITY_PAID_ORDER_CONTRIBUTION_CAP", "30")
	t.Setenv("POPULARITY_DECAY_HALF_LIFE", "12h")
	t.Setenv("MOCK_PAYMENT_ROUTES_ENABLED", "true")
	t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "environment-mock-secret")
	t.Setenv("SEARCH_PROVIDER", "elasticsearch")
	t.Setenv("ELASTICSEARCH_ADDRESSES", " http://localhost:9200/, https://search.example.test ")
	t.Setenv("ELASTICSEARCH_INDEX_PREFIX", "FeedSystem_Test")
	t.Setenv("ELASTICSEARCH_REQUEST_TIMEOUT", "1500ms")

	cfg := defaultCommerceConfig()
	if err := ApplyEnvOverrides(&cfg); err != nil {
		t.Fatalf("ApplyEnvOverrides() error = %v", err)
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Content.MaxImages != 6 || cfg.Content.MaxImageSizeBytes != 2*1024*1024 || cfg.Content.MaxBodyLength != 5000 {
		t.Fatalf("content env overrides = %#v", cfg.Content)
	}
	if cfg.Commerce.OrderPaymentValidFor.Value() != 20*time.Minute || cfg.Commerce.FlashSalePaymentValidFor.Value() != 5*time.Minute {
		t.Fatalf("commerce env overrides = %#v", cfg.Commerce)
	}
	if cfg.Search.Provider != "elasticsearch" || cfg.Search.Elasticsearch.IndexPrefix != "feedsystem_test" {
		t.Fatalf("search env overrides = %#v", cfg.Search)
	}
	if len(cfg.Search.Elasticsearch.Addresses) != 2 || cfg.Search.Elasticsearch.Addresses[0] != "http://localhost:9200" {
		t.Fatalf("elasticsearch addresses = %#v", cfg.Search.Elasticsearch.Addresses)
	}
}

func TestApplyEnvOverridesRejectsInvalidValue(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("CONTENT_MAX_IMAGES", "many")
	cfg := defaultCommerceConfig()
	if err := ApplyEnvOverrides(&cfg); err == nil || !strings.Contains(err.Error(), "CONTENT_MAX_IMAGES") {
		t.Fatalf("ApplyEnvOverrides() error = %v", err)
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range configEnvironmentVariables {
		t.Setenv(name, "")
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(filename, []byte(body), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return filename
}
