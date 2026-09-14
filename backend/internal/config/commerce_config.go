package config

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultMaxImages        = 9
	defaultMaxImageSize     = int64(10 * 1024 * 1024)
	defaultMaxBodyLength    = 10_000
	maximumMaxImages        = 20
	maximumImageSize        = int64(50 * 1024 * 1024)
	maximumBodyLength       = 1_000_000
	maximumPaymentValidTime = 7 * 24 * time.Hour
)

var searchIndexPrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Duration 让 YAML 和环境变量统一使用 Go duration 文本，例如 30m、24h。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration 必须是字符串")
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value.Value))
	if err != nil {
		return fmt.Errorf("解析 duration %q: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

type RuntimeConfig struct {
	Environment string `yaml:"environment"`
}

type ContentConfig struct {
	MaxImages         int   `yaml:"max_images"`
	MaxImageSizeBytes int64 `yaml:"max_image_size_bytes"`
	MaxBodyLength     int   `yaml:"max_body_length"`
}

type CommerceConfig struct {
	OrderPaymentValidFor     Duration `yaml:"order_payment_valid_for"`
	FlashSalePaymentValidFor Duration `yaml:"flash_sale_payment_valid_for"`
}

type PopularityConfig struct {
	LikeWeight               float64  `yaml:"like_weight"`
	FavoriteWeight           float64  `yaml:"favorite_weight"`
	CommentWeight            float64  `yaml:"comment_weight"`
	ShopVisitWeight          float64  `yaml:"shop_visit_weight"`
	ProductClickWeight       float64  `yaml:"product_click_weight"`
	PaidOrderWeight          float64  `yaml:"paid_order_weight"`
	PaidOrderContributionCap float64  `yaml:"paid_order_contribution_cap"`
	DecayHalfLife            Duration `yaml:"decay_half_life"`
}

type MockPaymentConfig struct {
	RoutesEnabled  bool   `yaml:"routes_enabled"`
	CallbackSecret string `yaml:"callback_secret"`
}

type SearchConfig struct {
	Provider      string              `yaml:"provider"`
	Elasticsearch ElasticsearchConfig `yaml:"elasticsearch"`
}

type ElasticsearchConfig struct {
	Addresses      []string `yaml:"addresses"`
	Username       string   `yaml:"username"`
	Password       string   `yaml:"password"`
	IndexPrefix    string   `yaml:"index_prefix"`
	RequestTimeout Duration `yaml:"request_timeout"`
}

func defaultCommerceConfig() Config {
	return Config{
		Runtime: RuntimeConfig{Environment: "development"},
		Server:  ServerConfig{Port: 8080},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     3306,
			User:     "root",
			Password: "123456",
			DBName:   "feedsystem",
		},
		Redis: RedisConfig{
			Host:     "localhost",
			Port:     6379,
			Password: "123456",
			DB:       0,
		},
		RabbitMQ: RabbitMQConfig{
			Host:     "localhost",
			Port:     5672,
			Username: "admin",
			Password: "password123",
		},
		ObservabilityConfig: ObservabilityConfig{
			Pprof: PprofConfig{
				Enabled:    true,
				ApiAddr:    "localhost:6060",
				WorkerAddr: "localhost:6061",
			},
		},
		Content: ContentConfig{
			MaxImages:         defaultMaxImages,
			MaxImageSizeBytes: defaultMaxImageSize,
			MaxBodyLength:     defaultMaxBodyLength,
		},
		Commerce: CommerceConfig{
			OrderPaymentValidFor:     Duration(30 * time.Minute),
			FlashSalePaymentValidFor: Duration(10 * time.Minute),
		},
		Popularity: PopularityConfig{
			LikeWeight:               1,
			FavoriteWeight:           2,
			CommentWeight:            3,
			ShopVisitWeight:          1.5,
			ProductClickWeight:       2,
			PaidOrderWeight:          8,
			PaidOrderContributionCap: 24,
			DecayHalfLife:            Duration(24 * time.Hour),
		},
		MockPayment: MockPaymentConfig{RoutesEnabled: true, CallbackSecret: "mock-payment-local-secret"},
		Search: SearchConfig{
			Provider: "mysql",
			Elasticsearch: ElasticsearchConfig{
				IndexPrefix:    "feedsystem",
				RequestTimeout: Duration(2 * time.Second),
			},
		},
	}
}

func (cfg *Config) normalize() {
	cfg.Runtime.Environment = strings.ToLower(strings.TrimSpace(cfg.Runtime.Environment))
	cfg.Search.Provider = strings.ToLower(strings.TrimSpace(cfg.Search.Provider))
	cfg.Search.Elasticsearch.IndexPrefix = strings.ToLower(strings.TrimSpace(cfg.Search.Elasticsearch.IndexPrefix))
	for index := range cfg.Search.Elasticsearch.Addresses {
		cfg.Search.Elasticsearch.Addresses[index] = strings.TrimRight(strings.TrimSpace(cfg.Search.Elasticsearch.Addresses[index]), "/")
	}
}

// Validate 拒绝会改变安全边界或导致运行时无定义行为的配置。
func (cfg Config) Validate() error {
	switch cfg.Runtime.Environment {
	case "development", "test", "production":
	default:
		return fmt.Errorf("runtime.environment 必须是 development、test 或 production")
	}
	if cfg.Content.MaxImages < 1 || cfg.Content.MaxImages > maximumMaxImages {
		return fmt.Errorf("content.max_images 必须在 1..%d 之间", maximumMaxImages)
	}
	if cfg.Content.MaxImageSizeBytes < 1 || cfg.Content.MaxImageSizeBytes > maximumImageSize {
		return fmt.Errorf("content.max_image_size_bytes 必须在 1..%d 之间", maximumImageSize)
	}
	if cfg.Content.MaxBodyLength < 1 || cfg.Content.MaxBodyLength > maximumBodyLength {
		return fmt.Errorf("content.max_body_length 必须在 1..%d 之间", maximumBodyLength)
	}
	if cfg.Commerce.OrderPaymentValidFor.Value() <= 0 || cfg.Commerce.OrderPaymentValidFor.Value() > maximumPaymentValidTime {
		return fmt.Errorf("commerce.order_payment_valid_for 必须大于 0 且不超过 %s", maximumPaymentValidTime)
	}
	if cfg.Commerce.FlashSalePaymentValidFor.Value() <= 0 || cfg.Commerce.FlashSalePaymentValidFor.Value() > cfg.Commerce.OrderPaymentValidFor.Value() {
		return fmt.Errorf("commerce.flash_sale_payment_valid_for 必须大于 0 且不超过普通订单支付有效期")
	}

	weights := map[string]float64{
		"like_weight":                 cfg.Popularity.LikeWeight,
		"favorite_weight":             cfg.Popularity.FavoriteWeight,
		"comment_weight":              cfg.Popularity.CommentWeight,
		"shop_visit_weight":           cfg.Popularity.ShopVisitWeight,
		"product_click_weight":        cfg.Popularity.ProductClickWeight,
		"paid_order_weight":           cfg.Popularity.PaidOrderWeight,
		"paid_order_contribution_cap": cfg.Popularity.PaidOrderContributionCap,
	}
	for name, value := range weights {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("popularity.%s 必须是有限非负数", name)
		}
	}
	if cfg.Popularity.LikeWeight+cfg.Popularity.FavoriteWeight+cfg.Popularity.CommentWeight == 0 {
		return fmt.Errorf("popularity 至少一个内容互动权重必须大于 0")
	}
	if cfg.Popularity.PaidOrderContributionCap < cfg.Popularity.PaidOrderWeight {
		return fmt.Errorf("popularity.paid_order_contribution_cap 不得小于单次成交权重")
	}
	if cfg.Popularity.DecayHalfLife.Value() <= 0 {
		return fmt.Errorf("popularity.decay_half_life 必须大于 0")
	}
	if cfg.MockPayment.RoutesEnabled && cfg.Runtime.Environment == "production" {
		return fmt.Errorf("production 环境禁止启用 mock_payment.routes_enabled")
	}
	if len(strings.TrimSpace(cfg.MockPayment.CallbackSecret)) < 16 {
		return fmt.Errorf("mock_payment.callback_secret 长度不能少于 16")
	}

	switch cfg.Search.Provider {
	case "mysql":
	case "elasticsearch":
		if len(cfg.Search.Elasticsearch.Addresses) == 0 {
			return fmt.Errorf("search.provider=elasticsearch 时必须配置 search.elasticsearch.addresses")
		}
	default:
		return fmt.Errorf("search.provider 必须是 mysql 或 elasticsearch")
	}
	if !searchIndexPrefixPattern.MatchString(cfg.Search.Elasticsearch.IndexPrefix) {
		return fmt.Errorf("search.elasticsearch.index_prefix 必须是小写字母、数字、下划线或短横线，最长 64 字符")
	}
	if cfg.Search.Elasticsearch.RequestTimeout.Value() <= 0 {
		return fmt.Errorf("search.elasticsearch.request_timeout 必须大于 0")
	}
	for _, address := range cfg.Search.Elasticsearch.Addresses {
		parsed, err := url.Parse(address)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("无效 Elasticsearch 地址 %q", address)
		}
	}
	return nil
}
