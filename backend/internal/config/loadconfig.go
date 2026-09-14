package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server              ServerConfig        `yaml:"server"`
	Database            DatabaseConfig      `yaml:"database"`
	Redis               RedisConfig         `yaml:"redis"`
	RabbitMQ            RabbitMQConfig      `yaml:"rabbitmq"`
	ObservabilityConfig ObservabilityConfig `yaml:"observability"`
	Runtime             RuntimeConfig       `yaml:"runtime"`
	Content             ContentConfig       `yaml:"content"`
	Commerce            CommerceConfig      `yaml:"commerce"`
	Popularity          PopularityConfig    `yaml:"popularity"`
	MockPayment         MockPaymentConfig   `yaml:"mock_payment"`
	Search              SearchConfig        `yaml:"search"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
}

type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type RabbitMQConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type ObservabilityConfig struct {
	Pprof PprofConfig `yaml:"pprof"`
}
type PprofConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ApiAddr    string `yaml:"api_addr"`
	WorkerAddr string `yaml:"worker_addr"`
}

func Load(filename string) (Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := defaultCommerceConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", filename, err)
	}

	if err := ApplyEnvOverrides(&cfg); err != nil {
		return Config{}, fmt.Errorf("apply environment overrides: %w", err)
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", filename, err)
	}
	return cfg, nil
}

func ApplyEnvOverrides(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if v := os.Getenv("APP_ENV"); v != "" {
		cfg.Runtime.Environment = v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		port, err := parseEnvInt("SERVER_PORT", v)
		if err != nil {
			return err
		}
		cfg.Server.Port = port
	}
	if v := os.Getenv("MYSQL_HOST"); v != "" {
		cfg.Database.Host = v
	}
	if v := os.Getenv("MYSQL_PORT"); v != "" {
		port, err := parseEnvInt("MYSQL_PORT", v)
		if err != nil {
			return err
		}
		cfg.Database.Port = port
	}
	if v := os.Getenv("MYSQL_USER"); v != "" {
		cfg.Database.User = v
	}
	if v := os.Getenv("MYSQL_ROOT_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("MYSQL_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("MYSQL_DATABASE"); v != "" {
		cfg.Database.DBName = v
	}
	if v := os.Getenv("REDIS_HOST"); v != "" {
		cfg.Redis.Host = v
	}
	if v := os.Getenv("REDIS_PORT"); v != "" {
		port, err := parseEnvInt("REDIS_PORT", v)
		if err != nil {
			return err
		}
		cfg.Redis.Port = port
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		cfg.Redis.Password = v
	}
	if v := os.Getenv("REDIS_DB"); v != "" {
		database, err := parseEnvInt("REDIS_DB", v)
		if err != nil {
			return err
		}
		cfg.Redis.DB = database
	}
	if v := os.Getenv("RABBITMQ_HOST"); v != "" {
		cfg.RabbitMQ.Host = v
	}
	if v := os.Getenv("RABBITMQ_PORT"); v != "" {
		port, err := parseEnvInt("RABBITMQ_PORT", v)
		if err != nil {
			return err
		}
		cfg.RabbitMQ.Port = port
	}
	if v := os.Getenv("RABBITMQ_USER"); v != "" {
		cfg.RabbitMQ.Username = v
	}
	if v := os.Getenv("RABBITMQ_PASS"); v != "" {
		cfg.RabbitMQ.Password = v
	}
	if v := os.Getenv("CONTENT_MAX_IMAGES"); v != "" {
		value, err := parseEnvInt("CONTENT_MAX_IMAGES", v)
		if err != nil {
			return err
		}
		cfg.Content.MaxImages = value
	}
	if v := os.Getenv("CONTENT_MAX_IMAGE_SIZE_BYTES"); v != "" {
		value, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("CONTENT_MAX_IMAGE_SIZE_BYTES 必须是整数: %w", err)
		}
		cfg.Content.MaxImageSizeBytes = value
	}
	if v := os.Getenv("CONTENT_MAX_BODY_LENGTH"); v != "" {
		value, err := parseEnvInt("CONTENT_MAX_BODY_LENGTH", v)
		if err != nil {
			return err
		}
		cfg.Content.MaxBodyLength = value
	}
	if v := os.Getenv("ORDER_PAYMENT_VALID_FOR"); v != "" {
		value, err := parseEnvDuration("ORDER_PAYMENT_VALID_FOR", v)
		if err != nil {
			return err
		}
		cfg.Commerce.OrderPaymentValidFor = Duration(value)
	}
	if v := os.Getenv("FLASH_SALE_PAYMENT_VALID_FOR"); v != "" {
		value, err := parseEnvDuration("FLASH_SALE_PAYMENT_VALID_FOR", v)
		if err != nil {
			return err
		}
		cfg.Commerce.FlashSalePaymentValidFor = Duration(value)
	}
	if err := applyPopularityEnv(cfg); err != nil {
		return err
	}
	if v := os.Getenv("MOCK_PAYMENT_ROUTES_ENABLED"); v != "" {
		value, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("MOCK_PAYMENT_ROUTES_ENABLED 必须是布尔值: %w", err)
		}
		cfg.MockPayment.RoutesEnabled = value
	}
	if v := os.Getenv("MOCK_PAYMENT_CALLBACK_SECRET"); v != "" {
		cfg.MockPayment.CallbackSecret = v
	}
	if v := os.Getenv("SEARCH_PROVIDER"); v != "" {
		cfg.Search.Provider = v
	}
	if v := os.Getenv("ELASTICSEARCH_ADDRESSES"); v != "" {
		cfg.Search.Elasticsearch.Addresses = splitNonEmpty(v)
	}
	if v := os.Getenv("ELASTICSEARCH_USERNAME"); v != "" {
		cfg.Search.Elasticsearch.Username = v
	}
	if v := os.Getenv("ELASTICSEARCH_PASSWORD"); v != "" {
		cfg.Search.Elasticsearch.Password = v
	}
	if v := os.Getenv("ELASTICSEARCH_INDEX_PREFIX"); v != "" {
		cfg.Search.Elasticsearch.IndexPrefix = v
	}
	if v := os.Getenv("ELASTICSEARCH_REQUEST_TIMEOUT"); v != "" {
		value, err := parseEnvDuration("ELASTICSEARCH_REQUEST_TIMEOUT", v)
		if err != nil {
			return err
		}
		cfg.Search.Elasticsearch.RequestTimeout = Duration(value)
	}
	return nil
}

// bool用来表示是否使用了默认配置，true表示使用了默认配置
func LoadLocalDev(filename string) (Config, bool, error) {
	cfg, err := Load(filename)
	if err == nil {
		return cfg, false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		cfg := defaultCommerceConfig()
		if overrideErr := ApplyEnvOverrides(&cfg); overrideErr != nil {
			return Config{}, false, fmt.Errorf("apply environment overrides: %w", overrideErr)
		}
		cfg.normalize()
		if validateErr := cfg.Validate(); validateErr != nil {
			return Config{}, false, fmt.Errorf("validate default config: %w", validateErr)
		}
		return cfg, true, nil
	}
	return Config{}, false, err
}

func DefaultLocalConfig() Config {
	cfg := defaultCommerceConfig()
	_ = ApplyEnvOverrides(&cfg)
	cfg.normalize()
	return cfg
}

func parseEnvInt(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数: %w", name, err)
	}
	return parsed, nil
}

func parseEnvDuration(name, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是 Go duration: %w", name, err)
	}
	return parsed, nil
}

func parseEnvFloat(name, value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是数字: %w", name, err)
	}
	return parsed, nil
}

func applyPopularityEnv(cfg *Config) error {
	items := []struct {
		name   string
		target *float64
	}{
		{"POPULARITY_LIKE_WEIGHT", &cfg.Popularity.LikeWeight},
		{"POPULARITY_FAVORITE_WEIGHT", &cfg.Popularity.FavoriteWeight},
		{"POPULARITY_COMMENT_WEIGHT", &cfg.Popularity.CommentWeight},
		{"POPULARITY_SHOP_VISIT_WEIGHT", &cfg.Popularity.ShopVisitWeight},
		{"POPULARITY_PRODUCT_CLICK_WEIGHT", &cfg.Popularity.ProductClickWeight},
		{"POPULARITY_PAID_ORDER_WEIGHT", &cfg.Popularity.PaidOrderWeight},
		{"POPULARITY_PAID_ORDER_CONTRIBUTION_CAP", &cfg.Popularity.PaidOrderContributionCap},
	}
	for _, item := range items {
		if raw := os.Getenv(item.name); raw != "" {
			value, err := parseEnvFloat(item.name, raw)
			if err != nil {
				return err
			}
			*item.target = value
		}
	}
	if raw := os.Getenv("POPULARITY_DECAY_HALF_LIFE"); raw != "" {
		value, err := parseEnvDuration("POPULARITY_DECAY_HALF_LIFE", raw)
		if err != nil {
			return err
		}
		cfg.Popularity.DecayHalfLife = Duration(value)
	}
	return nil
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
