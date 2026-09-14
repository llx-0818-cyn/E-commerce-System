package promotion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	domainevent "feedsystem_video_go/internal/event"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/internal/storefront"

	"gorm.io/gorm"
)

var (
	ErrCampaignValidation  = errors.New("invalid flash sale campaign")
	ErrCampaignNotFound    = errors.New("flash sale campaign not found")
	ErrCampaignForbidden   = errors.New("flash sale campaign operation forbidden")
	ErrCampaignUnavailable = errors.New("flash sale campaign is unavailable")
	ErrCampaignExpired     = errors.New("flash sale campaign is expired")
	ErrPreheatUnavailable  = errors.New("flash sale preheat store is unavailable")
)

type Service struct {
	repository   *Repository
	cache        *rediscache.Client
	events       *domainevent.Repository
	consumer     *domainevent.ConsumerGuard
	compensation *domainevent.ConsumerGuard
	lifecycle    *domainevent.ConsumerGuard
	orders       FlashSaleOrderCreator
	now          func() time.Time
}

type FlashSaleOrderCreator interface {
	CreateFlashSaleOrder(context.Context, orderdomain.CreateFlashSaleOrderInput) (*orderdomain.OrderGroup, error)
}

func NewService(repository *Repository, cache *rediscache.Client, orderCreators ...FlashSaleOrderCreator) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("promotion service repository is nil")
	}
	if len(orderCreators) > 1 {
		return nil, errors.New("promotion service accepts at most one order creator")
	}
	events, err := domainevent.NewRepository(repository.db)
	if err != nil {
		return nil, err
	}
	consumer, err := domainevent.NewConsumerGuard(repository.db, "seckill-order-worker")
	if err != nil {
		return nil, err
	}
	compensation, err := domainevent.NewConsumerGuard(repository.db, "seckill-compensation-worker")
	if err != nil {
		return nil, err
	}
	lifecycle, err := domainevent.NewConsumerGuard(repository.db, "seckill-order-lifecycle-worker")
	if err != nil {
		return nil, err
	}
	service := &Service{repository: repository, cache: cache, events: events, consumer: consumer, compensation: compensation, lifecycle: lifecycle, now: func() time.Time { return time.Now().UTC() }}
	if len(orderCreators) == 1 {
		service.orders = orderCreators[0]
	}
	return service, nil
}

func (s *Service) CreateCampaign(ctx context.Context, input CreateCampaignInput) (*FlashSaleCampaign, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.StartsAt = input.StartsAt.UTC()
	input.EndsAt = input.EndsAt.UTC()
	now := s.now().UTC()
	if input.OwnerID == 0 || input.SKUID == 0 || input.Title == "" || utf8.RuneCountInString(input.Title) > 200 ||
		input.PriceCent <= 0 || input.TotalStock <= 0 || input.PerUserLimit == 0 || int64(input.PerUserLimit) > input.TotalStock ||
		input.StartsAt.IsZero() || input.EndsAt.IsZero() || !input.EndsAt.After(input.StartsAt) || !input.EndsAt.After(now) {
		return nil, ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var created *FlashSaleCampaign
	err := s.repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sku, err := loadOwnedCampaignSKU(ctx, tx, input.OwnerID, input.SKUID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCampaignForbidden
		}
		if err != nil {
			return err
		}
		if sku.ShopStatus != storefront.ShopStatusOpen || sku.ProductStatus != storefront.ProductStatusOnSale ||
			sku.SKUStatus != storefront.SKUStatusActive || sku.AvailableStock < input.TotalStock || input.PriceCent > sku.PriceCent {
			return ErrCampaignUnavailable
		}
		campaignNo, err := newCampaignNumber(now)
		if err != nil {
			return err
		}
		value := &FlashSaleCampaign{
			CampaignNo: campaignNo, SKUID: input.SKUID, Title: input.Title,
			CampaignVersion: 1, Status: CampaignStatusDraft, PriceCent: input.PriceCent,
			TotalStock: input.TotalStock, PerUserLimit: input.PerUserLimit,
			StartsAt: input.StartsAt, EndsAt: input.EndsAt,
		}
		if err := tx.WithContext(ctx).Create(value).Error; err != nil {
			return err
		}
		created = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Service) SetCampaignEnabled(ctx context.Context, ownerID, campaignID uint64, enabled bool) (*FlashSaleCampaign, error) {
	if ownerID == 0 || campaignID == 0 {
		return nil, ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var output *FlashSaleCampaign
	err := s.repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		value, err := loadOwnedCampaignForUpdate(ctx, tx, ownerID, campaignID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCampaignForbidden
		}
		if err != nil {
			return err
		}
		if !value.EndsAt.After(s.now().UTC()) {
			return ErrCampaignExpired
		}
		if value.Status == CampaignStatusEnded {
			return ErrCampaignUnavailable
		}
		target := CampaignStatusDisabled
		updates := map[string]any{"status": target}
		if enabled {
			target = CampaignStatusEnabled
			updates["status"] = target
			if value.Status == CampaignStatusDisabled {
				updates["campaign_version"] = gorm.Expr("campaign_version + 1")
				value.CampaignVersion++
			}
		}
		if value.Status != target {
			if err := tx.WithContext(ctx).Model(&FlashSaleCampaign{}).Where("id = ?", value.ID).Updates(updates).Error; err != nil {
				return err
			}
			value.Status = target
		}
		output = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

func (s *Service) GetCampaign(ctx context.Context, campaignID uint64) (*FlashSaleCampaign, error) {
	if campaignID == 0 {
		return nil, ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value, err := s.repository.getCampaign(ctx, campaignID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCampaignNotFound
	}
	return value, err
}

func (s *Service) PreheatCampaign(ctx context.Context, ownerID, campaignID uint64) (*PreheatResult, error) {
	if ownerID == 0 || campaignID == 0 {
		return nil, ErrCampaignValidation
	}
	if s.cache == nil {
		return nil, ErrPreheatUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value, err := s.repository.getCampaign(ctx, campaignID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	var actualOwner uint64
	if err := s.repository.db.WithContext(ctx).Table("product_skus AS sku").
		Select("shop.owner_id").Joins("JOIN products AS product ON product.id=sku.product_id").
		Joins("JOIN shops AS shop ON shop.id=product.shop_id").Where("sku.id=?", value.SKUID).Scan(&actualOwner).Error; err != nil {
		return nil, err
	}
	if actualOwner != ownerID {
		return nil, ErrCampaignForbidden
	}
	now := s.now().UTC()
	if !value.EndsAt.After(now) {
		return nil, ErrCampaignExpired
	}
	if value.Status != CampaignStatusEnabled {
		return nil, ErrCampaignUnavailable
	}
	metadataKey := CampaignMetadataKey(s.cache, value.ID, value.CampaignVersion)
	stockKey := CampaignStockKey(s.cache, value.ID, value.CampaignVersion)
	ttl := value.EndsAt.Sub(now) + time.Hour
	metadata, _ := json.Marshal(map[string]any{
		"campaign_id": value.ID, "version": value.CampaignVersion, "sku_id": value.SKUID,
		"price_cent": value.PriceCent, "total_stock": value.TotalStock,
		"per_user_limit": value.PerUserLimit, "starts_at_ms": value.StartsAt.UnixMilli(),
		"ends_at_ms": value.EndsAt.UnixMilli(), "status": value.Status,
	})
	if err := s.cache.SetBytes(ctx, metadataKey, metadata, ttl); err != nil {
		return nil, err
	}
	initialized, err := s.cache.SetNXBytes(ctx, stockKey, []byte(strconv.FormatInt(value.TotalStock, 10)), ttl)
	if err != nil {
		return nil, err
	}
	if !initialized {
		if err := s.cache.Expire(ctx, stockKey, ttl); err != nil {
			return nil, err
		}
	}
	return &PreheatResult{
		CampaignID: value.ID, CampaignVersion: value.CampaignVersion,
		MetadataKey: metadataKey, StockKey: stockKey,
		BuyersKey:        CampaignBuyersKey(s.cache, value.ID, value.CampaignVersion),
		RequestsKey:      CampaignRequestsKey(s.cache, value.ID, value.CampaignVersion),
		StockInitialized: initialized,
	}, nil
}

func CampaignMetadataKey(cache *rediscache.Client, campaignID, version uint64) string {
	return cache.Key("flashsale:campaign:%d:v%d:meta", campaignID, version)
}

func CampaignStockKey(cache *rediscache.Client, campaignID, version uint64) string {
	return cache.Key("flashsale:campaign:%d:v%d:stock", campaignID, version)
}

func CampaignBuyersKey(cache *rediscache.Client, campaignID, version uint64) string {
	return cache.Key("flashsale:campaign:%d:v%d:buyers", campaignID, version)
}

func CampaignRequestsKey(cache *rediscache.Client, campaignID, version uint64) string {
	return cache.Key("flashsale:campaign:%d:v%d:requests", campaignID, version)
}

func newCampaignNumber(now time.Time) (string, error) {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return fmt.Sprintf("FS-%s-%s", now.UTC().Format("20060102150405"), hex.EncodeToString(buffer)), nil
}
