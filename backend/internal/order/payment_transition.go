package order

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	domainevent "feedsystem_video_go/internal/event"

	"gorm.io/gorm"
)

func (s *Service) MarkOrderPaid(ctx context.Context, orderID uint64, paidAt time.Time) (*Order, error) {
	if orderID == 0 || paidAt.IsZero() {
		return nil, ErrOrderValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	paidAt = paidAt.UTC()
	var result *Order
	err := s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		var err error
		result, err = s.ApplyPaidTransition(ctx, tx, outbox, orderID, paidAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) ApplyPaidTransition(ctx context.Context, tx *gorm.DB, outbox *domainevent.Writer, orderID uint64, paidAt time.Time) (*Order, error) {
	if s == nil || tx == nil || outbox == nil || orderID == 0 || paidAt.IsZero() {
		return nil, ErrOrderValidation
	}
	value, err := loadOrderForUpdate(ctx, tx, orderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if value.Status == StatusPaid {
		return value, nil
	}
	if value.Status != StatusPendingPayment {
		return nil, ErrOrderStateConflict
	}
	paidAt = paidAt.UTC()
	sort.Slice(value.Items, func(i, j int) bool { return value.Items[i].SKUID < value.Items[j].SKUID })
	ledger := make([]InventoryLedgerEntry, 0, len(value.Items))
	for _, item := range value.Items {
		version, err := sellLockedSKUStock(ctx, tx, item.SKUID, item.Quantity)
		if err != nil {
			return nil, err
		}
		ledger = append(ledger, InventoryLedgerEntry{
			EventID: inventoryLedgerEventID(value.OrderNo, item.SKUID, InventoryMovementSell),
			OrderID: value.ID, SKUID: item.SKUID, Movement: InventoryMovementSell,
			Quantity: int64(item.Quantity), LockedDelta: -int64(item.Quantity),
			SoldDelta: int64(item.Quantity), SKUVersion: version,
		})
	}
	if len(ledger) > 0 {
		if err := tx.WithContext(ctx).Create(&ledger).Error; err != nil {
			return nil, fmt.Errorf("create sell inventory ledger: %w", err)
		}
	}
	if err := updateOrderStatusCAS(ctx, tx, value.ID, StatusPendingPayment, StatusPaid, paidAt); err != nil {
		return nil, err
	}
	if err := value.Transition(StatusPaid, paidAt); err != nil {
		return nil, err
	}
	if _, err := outbox.Append(ctx, domainevent.DomainEvent{
		AggregateType: "order", AggregateID: strconv.FormatUint(value.ID, 10), EventType: OrderPaidEvent,
		Payload: map[string]any{
			"order_id": value.ID, "order_no": value.OrderNo, "buyer_id": value.BuyerID,
			"shop_id": value.ShopID, "total_cent": value.TotalCent,
			"paid_at": paidAt.Format(time.RFC3339Nano),
		},
	}); err != nil {
		return nil, err
	}
	return value, nil
}
