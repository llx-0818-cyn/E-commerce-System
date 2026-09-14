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

const (
	OrderCanceledEvent  = "order.canceled"
	OrderShippedEvent   = "order.shipped"
	OrderCompletedEvent = "order.completed"
)

func (s *Service) CancelOrder(ctx context.Context, buyerID, orderID uint64) (*Order, error) {
	return s.transitionOrder(ctx, buyerID, orderID, StatusPendingPayment, StatusCanceled, transitionBuyer, nil)
}

func (s *Service) ShipOrder(ctx context.Context, ownerID, orderID uint64) (*Order, error) {
	return s.transitionOrder(ctx, ownerID, orderID, StatusPaid, StatusShipped, transitionShopOwner, nil)
}

func (s *Service) ConfirmReceipt(ctx context.Context, buyerID, orderID uint64) (*Order, error) {
	return s.transitionOrder(ctx, buyerID, orderID, StatusShipped, StatusCompleted, transitionBuyer, nil)
}

func (s *Service) ExpireOrder(ctx context.Context, orderID uint64, at time.Time) (*Order, error) {
	if at.IsZero() {
		return nil, ErrOrderValidation
	}
	at = at.UTC()
	return s.transitionOrder(ctx, 0, orderID, StatusPendingPayment, StatusCanceled, transitionSystem, &at)
}

type ExpirationScanResult struct {
	Scanned  int `json:"scanned"`
	Canceled int `json:"canceled"`
}

func (s *Service) ScanExpiredOrders(ctx context.Context, at time.Time, limit int) (ExpirationScanResult, error) {
	if at.IsZero() || limit < 1 || limit > 1000 {
		return ExpirationScanResult{}, ErrOrderValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids, err := s.repository.expiredOrderIDs(ctx, at.UTC(), limit)
	if err != nil {
		return ExpirationScanResult{}, err
	}
	result := ExpirationScanResult{Scanned: len(ids)}
	for _, orderID := range ids {
		value, err := s.ExpireOrder(ctx, orderID, at)
		if errors.Is(err, ErrOrderNotExpired) {
			continue
		}
		if err != nil {
			return result, err
		}
		if value.Status == StatusCanceled {
			result.Canceled++
		}
	}
	return result, nil
}

type transitionActor int

const (
	transitionBuyer transitionActor = iota + 1
	transitionShopOwner
	transitionSystem
)

func (s *Service) transitionOrder(ctx context.Context, actorID, orderID uint64, source, target Status, actor transitionActor, transitionAt *time.Time) (*Order, error) {
	if orderID == 0 || !source.CanTransitionTo(target) || (actor != transitionSystem && actorID == 0) {
		return nil, ErrOrderValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var result *Order
	err := s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		value, err := loadOrderForUpdate(ctx, tx, orderID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		switch actor {
		case transitionBuyer:
			if value.BuyerID != actorID {
				return ErrOrderNotFound
			}
		case transitionShopOwner:
			ownerID, err := shopOwnerIDInTransaction(ctx, tx, value.ShopID)
			if err != nil || ownerID != actorID {
				return ErrOrderNotFound
			}
		case transitionSystem:
			if value.Status != source && value.Status != target {
				result = value
				return nil
			}
		default:
			return ErrOrderValidation
		}
		if value.Status == target {
			result = value
			return nil
		}
		if value.Status != source {
			return ErrOrderStateConflict
		}
		now := s.now().UTC()
		if transitionAt != nil {
			now = transitionAt.UTC()
		}
		if actor == transitionSystem && now.Before(value.ExpireAt) {
			return ErrOrderNotExpired
		}
		if target == StatusCanceled {
			sort.Slice(value.Items, func(i, j int) bool { return value.Items[i].SKUID < value.Items[j].SKUID })
			ledger := make([]InventoryLedgerEntry, 0, len(value.Items))
			for _, item := range value.Items {
				version, err := releaseSKUStock(ctx, tx, item.SKUID, item.Quantity)
				if err != nil {
					return err
				}
				ledger = append(ledger, InventoryLedgerEntry{
					EventID: inventoryLedgerEventID(value.OrderNo, item.SKUID, InventoryMovementRelease),
					OrderID: value.ID, SKUID: item.SKUID, Movement: InventoryMovementRelease,
					Quantity: int64(item.Quantity), AvailableDelta: int64(item.Quantity),
					LockedDelta: -int64(item.Quantity), SKUVersion: version,
				})
			}
			if len(ledger) > 0 {
				if err := tx.WithContext(ctx).Create(&ledger).Error; err != nil {
					return fmt.Errorf("create release inventory ledger: %w", err)
				}
			}
		}
		if err := updateOrderStatusCAS(ctx, tx, value.ID, source, target, now); err != nil {
			return err
		}
		if err := value.Transition(target, now); err != nil {
			return err
		}
		eventType := transitionEventType(target)
		if _, err := outbox.Append(ctx, domainevent.DomainEvent{
			AggregateType: "order", AggregateID: strconv.FormatUint(value.ID, 10), EventType: eventType,
			Payload: map[string]any{
				"order_id": value.ID, "order_no": value.OrderNo, "buyer_id": value.BuyerID,
				"shop_id": value.ShopID, "status": value.Status, "reason": transitionReason(actor, target),
				"occurred_at": now.Format(time.RFC3339Nano),
			},
		}); err != nil {
			return err
		}
		result = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func transitionReason(actor transitionActor, target Status) string {
	if target == StatusCanceled {
		if actor == transitionSystem {
			return "expired"
		}
		return "buyer"
	}
	return ""
}

func transitionEventType(target Status) string {
	switch target {
	case StatusCanceled:
		return OrderCanceledEvent
	case StatusShipped:
		return OrderShippedEvent
	case StatusCompleted:
		return OrderCompletedEvent
	default:
		return ""
	}
}
