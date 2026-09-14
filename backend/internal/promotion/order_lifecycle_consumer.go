package promotion

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	orderdomain "feedsystem_video_go/internal/order"

	"gorm.io/gorm"
)

type orderLifecyclePayload struct {
	OrderID uint64 `json:"order_id"`
	Reason  string `json:"reason"`
}

func (s *Service) ProcessOrderLifecycle(ctx context.Context, eventID, eventType string, payloadJSON []byte) error {
	if s == nil || s.lifecycle == nil || s.cache == nil || eventID == "" ||
		(eventType != orderdomain.OrderPaidEvent && eventType != orderdomain.OrderCanceledEvent) {
		return ErrCampaignValidation
	}
	var payload orderLifecyclePayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil || payload.OrderID == 0 {
		return ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var requestID string
	err := s.repository.db.WithContext(ctx).Model(&FlashSaleRequest{}).Select("request_id").
		Where("order_id=?", payload.OrderID).Scan(&requestID).Error
	if err != nil {
		return err
	}
	if requestID == "" {
		return nil
	}
	value, err := loadSeckillOrderContext(ctx, s.repository.db, requestID)
	if err != nil {
		return err
	}
	if eventType == orderdomain.OrderCanceledEvent {
		if err := s.releaseCanceledReservation(ctx, value, payload.Reason); err != nil {
			return err
		}
	}
	_, err = s.lifecycle.Process(ctx, domainevent.ConsumedEvent{
		EventID: eventID, EventType: eventType,
		AggregateType: "order", AggregateID: strconv.FormatUint(payload.OrderID, 10),
	}, func(tx *gorm.DB) error {
		target := "paid"
		allowed := []string{"order_created", "paid"}
		if eventType == orderdomain.OrderCanceledEvent {
			target = "canceled"
			allowed = []string{"order_created", "canceled"}
		}
		if value.RequestStatus == target {
			return nil
		}
		result := tx.WithContext(ctx).Model(&FlashSaleRequest{}).Where("request_id=? AND status IN ?", requestID, allowed).
			Updates(map[string]any{"status": target})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("flash sale order lifecycle state conflict")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if eventType == orderdomain.OrderPaidEvent {
		result := ReservationResult{
			Code: ReservationPaid, RequestID: value.RequestID,
			ReservationToken: value.ReservationToken, BuyerID: value.BuyerID,
			Quantity: value.Quantity, OrderID: payload.OrderID, OrderNo: value.OrderNo,
			PaymentExpiresAt: value.ExpireAt,
		}
		return s.writeReservationResult(ctx, value, result)
	}
	return nil
}

func (s *Service) releaseCanceledReservation(ctx context.Context, value *seckillOrderContext, reason string) error {
	store, _ := NewLuaReservationStore(s.cache)
	ttl := value.EndsAt.Sub(s.now().UTC()) + time.Hour
	if ttl <= 0 {
		ttl = time.Hour
	}
	if reason == "" {
		reason = "order canceled"
	}
	result, err := store.Compensate(ctx, ReservationKeys{
		Metadata:     CampaignMetadataKey(s.cache, value.CampaignID, value.CampaignVersion),
		Stock:        CampaignStockKey(s.cache, value.CampaignID, value.CampaignVersion),
		Buyers:       CampaignBuyersKey(s.cache, value.CampaignID, value.CampaignVersion),
		Requests:     CampaignRequestsKey(s.cache, value.CampaignID, value.CampaignVersion),
		ResultPrefix: CampaignRequestResultPrefix(s.cache, value.CampaignID, value.CampaignVersion),
	}, CompensationCommand{
		BuyerID: value.BuyerID, IdempotencyKey: value.IdempotencyKey,
		RequestID: value.RequestID, ReservationToken: value.ReservationToken,
		Quantity: value.Quantity, Reason: reason, ResultCode: ReservationCanceled, TTL: ttl,
	})
	if err != nil {
		return err
	}
	if result.Code != CompensationApplied && result.Code != CompensationDuplicate {
		return ErrCampaignUnavailable
	}
	return nil
}
