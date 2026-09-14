package promotion

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domainevent "feedsystem_video_go/internal/event"

	"gorm.io/gorm"
)

type seckillFailurePayload struct {
	RequestID        string `json:"request_id"`
	ReservationToken string `json:"reservation_token"`
	CampaignID       uint64 `json:"campaign_id"`
	CampaignVersion  uint64 `json:"campaign_version"`
	BuyerID          uint64 `json:"buyer_id"`
	Quantity         uint   `json:"quantity"`
	Reason           string `json:"reason"`
}

func (s *Service) ProcessCompensation(ctx context.Context, eventID string, payloadJSON []byte) error {
	if s == nil || s.compensation == nil || s.cache == nil || eventID == "" {
		return ErrCampaignUnavailable
	}
	var payload seckillFailurePayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil || payload.RequestID == "" || payload.ReservationToken == "" ||
		payload.CampaignID == 0 || payload.CampaignVersion == 0 || payload.BuyerID == 0 || payload.Quantity == 0 || payload.Reason == "" {
		return ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value, err := loadSeckillOrderContext(ctx, s.repository.db, payload.RequestID)
	if err != nil {
		return err
	}
	if value.CampaignID != payload.CampaignID || value.CampaignVersion != payload.CampaignVersion ||
		value.BuyerID != payload.BuyerID || value.ReservationToken != payload.ReservationToken || value.Quantity != payload.Quantity {
		return ErrCampaignValidation
	}
	store, _ := NewLuaReservationStore(s.cache)
	ttl := value.EndsAt.Sub(s.now().UTC()) + time.Hour
	if ttl <= 0 {
		ttl = time.Hour
	}
	compensated, err := store.Compensate(ctx, ReservationKeys{
		Metadata:     CampaignMetadataKey(s.cache, value.CampaignID, value.CampaignVersion),
		Stock:        CampaignStockKey(s.cache, value.CampaignID, value.CampaignVersion),
		Buyers:       CampaignBuyersKey(s.cache, value.CampaignID, value.CampaignVersion),
		Requests:     CampaignRequestsKey(s.cache, value.CampaignID, value.CampaignVersion),
		ResultPrefix: CampaignRequestResultPrefix(s.cache, value.CampaignID, value.CampaignVersion),
	}, CompensationCommand{
		BuyerID: value.BuyerID, IdempotencyKey: value.IdempotencyKey,
		RequestID: value.RequestID, ReservationToken: value.ReservationToken,
		Quantity: value.Quantity, Reason: payload.Reason, TTL: ttl,
		ResultCode: ReservationFailed,
	})
	if err != nil {
		return err
	}
	if compensated.Code != CompensationApplied && compensated.Code != CompensationDuplicate {
		return ErrCampaignUnavailable
	}
	_, err = s.compensation.Process(ctx, domainevent.ConsumedEvent{
		EventID: eventID, EventType: SeckillOrderFailedEvent,
		AggregateType: "flash_sale_request", AggregateID: value.RequestID,
	}, func(tx *gorm.DB) error {
		if value.RequestStatus == "compensated" {
			return nil
		}
		result := tx.WithContext(ctx).Model(&FlashSaleRequest{}).
			Where("request_id=? AND status IN ?", value.RequestID, []string{"failed", "compensated"}).
			Updates(map[string]any{"status": "compensated"})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("flash sale request is not compensatable")
		}
		return nil
	})
	return err
}
