package promotion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	orderdomain "feedsystem_video_go/internal/order"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SeckillOrderFailedEvent = "seckill.order.failed"

type seckillOrderPayload struct {
	RequestID        string `json:"request_id"`
	ReservationToken string `json:"reservation_token"`
	CampaignID       uint64 `json:"campaign_id"`
	CampaignVersion  uint64 `json:"campaign_version"`
	BuyerID          uint64 `json:"buyer_id"`
	SKUID            uint64 `json:"sku_id"`
	Quantity         uint   `json:"quantity"`
	PriceCent        int64  `json:"price_cent"`
}

type seckillOrderContext struct {
	RequestID        string     `gorm:"column:request_id"`
	ReservationToken string     `gorm:"column:reservation_token"`
	CampaignID       uint64     `gorm:"column:campaign_id"`
	CampaignVersion  uint64     `gorm:"column:campaign_version"`
	BuyerID          uint64     `gorm:"column:buyer_id"`
	IdempotencyKey   string     `gorm:"column:idempotency_key"`
	Quantity         uint       `gorm:"column:quantity"`
	RequestStatus    string     `gorm:"column:request_status"`
	OrderID          *uint64    `gorm:"column:order_id"`
	SKUID            uint64     `gorm:"column:sku_id"`
	PriceCent        int64      `gorm:"column:price_cent"`
	EndsAt           time.Time  `gorm:"column:ends_at"`
	ShopID           uint64     `gorm:"column:shop_id"`
	AddressID        uint64     `gorm:"column:address_id"`
	OrderNo          string     `gorm:"column:order_no"`
	ExpireAt         *time.Time `gorm:"column:expire_at"`
}

func (s *Service) ProcessOrderRequest(ctx context.Context, eventID string, payloadJSON []byte) error {
	if s == nil || s.consumer == nil || s.orders == nil || s.cache == nil || eventID == "" {
		return ErrCampaignUnavailable
	}
	var payload seckillOrderPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil || payload.RequestID == "" || payload.ReservationToken == "" ||
		payload.CampaignID == 0 || payload.CampaignVersion == 0 || payload.BuyerID == 0 || payload.SKUID == 0 ||
		payload.Quantity == 0 || payload.PriceCent <= 0 {
		return ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.consumer.Process(ctx, domainevent.ConsumedEvent{
		EventID: eventID, EventType: SeckillOrderRequestedEvent,
		AggregateType: "flash_sale_request", AggregateID: payload.RequestID,
	}, func(tx *gorm.DB) error {
		value, err := loadSeckillOrderContext(ctx, tx, payload.RequestID)
		if err != nil {
			return err
		}
		if value.CampaignID != payload.CampaignID || value.CampaignVersion != payload.CampaignVersion ||
			value.BuyerID != payload.BuyerID || value.SKUID != payload.SKUID || value.Quantity != payload.Quantity ||
			value.PriceCent != payload.PriceCent || value.ReservationToken != payload.ReservationToken {
			return fmt.Errorf("%w: request=%s/%d/%d/%d/%d/%d token_match=%v payload=%s/%d/%d/%d/%d/%d",
				ErrCampaignValidation, value.RequestID, value.CampaignID, value.CampaignVersion, value.BuyerID,
				value.SKUID, value.Quantity, value.ReservationToken == payload.ReservationToken,
				payload.RequestID, payload.CampaignID, payload.CampaignVersion, payload.BuyerID, payload.SKUID, payload.Quantity)
		}
		if value.RequestStatus == "order_created" && value.OrderID != nil {
			return nil
		}
		if value.RequestStatus == "failed" || value.RequestStatus == "compensated" {
			return nil
		}
		group, err := s.orders.CreateFlashSaleOrder(ctx, orderdomain.CreateFlashSaleOrderInput{
			RequestID: payload.RequestID, BuyerID: payload.BuyerID, AddressID: value.AddressID,
			ShopID: value.ShopID, SKUID: payload.SKUID, Quantity: payload.Quantity, PriceCent: payload.PriceCent,
		})
		if err != nil {
			if isTerminalSeckillOrderError(err) {
				return markSeckillOrderFailed(ctx, tx, payload, err, s.now().UTC())
			}
			return err
		}
		if group == nil || len(group.Orders) != 1 {
			return errors.New("flash sale order creator returned an invalid group")
		}
		orderID := group.Orders[0].ID
		result := tx.WithContext(ctx).Model(&FlashSaleRequest{}).
			Where("request_id=? AND status IN ?", payload.RequestID, []string{"accepted", "processing"}).
			Updates(map[string]any{"status": "order_created", "order_id": orderID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			var existing FlashSaleRequest
			if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id=?", payload.RequestID).First(&existing).Error; err != nil {
				return err
			}
			if existing.Status == "order_created" && existing.OrderID != nil && *existing.OrderID == orderID {
				return nil
			}
			return ErrCampaignUnavailable
		}
		return nil
	})
	if err != nil {
		return err
	}
	value, err := loadSeckillOrderContext(ctx, s.repository.db, payload.RequestID)
	if err != nil {
		return err
	}
	if value.RequestStatus == "failed" {
		result := ReservationResult{
			Code: ReservationFailed, RequestID: value.RequestID,
			ReservationToken: value.ReservationToken, BuyerID: value.BuyerID, Quantity: value.Quantity,
		}
		return s.writeReservationResult(ctx, value, result)
	}
	if value.OrderID == nil || value.OrderNo == "" || value.ExpireAt == nil {
		return ErrCampaignUnavailable
	}
	result := ReservationResult{
		Code: ReservationOrderCreated, RequestID: value.RequestID,
		ReservationToken: value.ReservationToken, BuyerID: value.BuyerID,
		Quantity: value.Quantity, OrderID: *value.OrderID, OrderNo: value.OrderNo,
		PaymentExpiresAt: value.ExpireAt,
	}
	ttl := value.EndsAt.Sub(s.now().UTC()) + time.Hour
	if ttl <= 0 {
		ttl = time.Hour
	}
	return s.writeReservationResultWithTTL(ctx, value, result, ttl)
}

func (s *Service) writeReservationResult(ctx context.Context, value *seckillOrderContext, result ReservationResult) error {
	ttl := value.EndsAt.Sub(s.now().UTC()) + time.Hour
	if ttl <= 0 {
		ttl = time.Hour
	}
	return s.writeReservationResultWithTTL(ctx, value, result, ttl)
}

func (s *Service) writeReservationResultWithTTL(ctx context.Context, value *seckillOrderContext, result ReservationResult, ttl time.Duration) error {
	data, _ := json.Marshal(result)
	if err := s.cache.SetBytes(ctx, CampaignRequestResultKey(s.cache, value.CampaignID, value.CampaignVersion, value.RequestID), data, ttl); err != nil {
		return fmt.Errorf("%w: %v", ErrPreheatUnavailable, err)
	}
	return nil
}

func isTerminalSeckillOrderError(err error) bool {
	return errors.Is(err, orderdomain.ErrStockInsufficient) || errors.Is(err, orderdomain.ErrCatalogUnavailable) ||
		errors.Is(err, orderdomain.ErrPriceChanged) || errors.Is(err, orderdomain.ErrAddressNotFound) ||
		errors.Is(err, orderdomain.ErrOrderValidation)
}

func markSeckillOrderFailed(ctx context.Context, tx *gorm.DB, payload seckillOrderPayload, cause error, failedAt time.Time) error {
	detail := cause.Error()
	result := tx.WithContext(ctx).Model(&FlashSaleRequest{}).
		Where("request_id=? AND status IN ?", payload.RequestID, []string{"accepted", "processing"}).
		Updates(map[string]any{"status": "failed", "failure_code": "order_creation_failed", "failure_reason": detail})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return nil
	}
	failurePayload := map[string]any{
		"request_id": payload.RequestID, "reservation_token": payload.ReservationToken,
		"campaign_id": payload.CampaignID, "campaign_version": payload.CampaignVersion,
		"buyer_id": payload.BuyerID, "sku_id": payload.SKUID, "quantity": payload.Quantity,
		"price_cent": payload.PriceCent, "reason": detail, "failed_at": failedAt.Format(time.RFC3339Nano),
	}
	encoded, _ := json.Marshal(failurePayload)
	digest := sha256.Sum256([]byte("seckill-order-failed:" + payload.RequestID))
	record := &domainevent.OutboxEvent{
		EventID: hex.EncodeToString(digest[:]), AggregateType: "flash_sale_request",
		AggregateID: payload.RequestID, EventType: SeckillOrderFailedEvent,
		PayloadJSON: string(encoded), Status: domainevent.OutboxStatusPending,
		NextAttemptAt: failedAt,
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error
}

func loadSeckillOrderContext(ctx context.Context, db *gorm.DB, requestID string) (*seckillOrderContext, error) {
	var value seckillOrderContext
	err := db.WithContext(ctx).Table("flash_sale_requests AS request").
		Select(`request.request_id,request.reservation_token,request.campaign_id,campaign.campaign_version,
request.buyer_id,request.idempotency_key,request.quantity,request.status AS request_status,request.order_id,
campaign.sku_id,campaign.price_cent,campaign.ends_at,shop.id AS shop_id,
address.id AS address_id,orders.order_no,orders.expire_at`).
		Joins("JOIN flash_sale_campaigns AS campaign ON campaign.id=request.campaign_id").
		Joins("JOIN product_skus AS sku ON sku.id=campaign.sku_id").
		Joins("JOIN products AS product ON product.id=sku.product_id").
		Joins("JOIN shops AS shop ON shop.id=product.shop_id").
		Joins("LEFT JOIN addresses AS address ON address.account_id=request.buyer_id AND address.is_default=TRUE").
		Joins("LEFT JOIN orders ON orders.id=request.order_id").
		Where("request.request_id=?", requestID).Take(&value).Error
	if err != nil {
		return nil, err
	}
	return &value, nil
}
