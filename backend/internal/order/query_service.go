package order

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

var (
	ErrOrderNotFound      = errors.New("order not found")
	ErrOrderForbidden     = errors.New("order operation forbidden")
	ErrOrderStateConflict = errors.New("order state conflict")
	ErrOrderNotExpired    = errors.New("order is not expired")
)

type OrderCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint64    `json:"id"`
}

type OrderPage struct {
	Orders     []Order      `json:"orders"`
	NextCursor *OrderCursor `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

func (s *Service) ListBuyerOrders(ctx context.Context, buyerID uint64, status Status, cursor *OrderCursor, limit int) (*OrderPage, error) {
	if err := validateOrderQuery(buyerID, status, cursor, limit); err != nil {
		return nil, err
	}
	orders, err := s.repository.listBuyerOrders(ctx, buyerID, status, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return buildOrderPage(orders, limit), nil
}

func (s *Service) ListShopOrders(ctx context.Context, ownerID, shopID uint64, status Status, cursor *OrderCursor, limit int) (*OrderPage, error) {
	if shopID == 0 || validateOrderQuery(ownerID, status, cursor, limit) != nil {
		return nil, ErrOrderValidation
	}
	actualOwner, err := s.repository.shopOwnerID(ctx, shopID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if actualOwner != ownerID {
		return nil, ErrOrderForbidden
	}
	orders, err := s.repository.listShopOrders(ctx, shopID, status, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	return buildOrderPage(orders, limit), nil
}

func (s *Service) GetOrderForActor(ctx context.Context, actorID, orderID uint64) (*Order, error) {
	if actorID == 0 || orderID == 0 {
		return nil, ErrOrderValidation
	}
	value, err := s.repository.getOrderWithItems(ctx, orderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if value.BuyerID == actorID {
		return value, nil
	}
	ownerID, err := s.repository.shopOwnerID(ctx, value.ShopID)
	if err != nil || ownerID != actorID {
		return nil, ErrOrderNotFound
	}
	return value, nil
}

func validateOrderQuery(actorID uint64, status Status, cursor *OrderCursor, limit int) error {
	if actorID == 0 || limit < 1 || limit > 100 || (status != "" && !status.Valid()) {
		return ErrOrderValidation
	}
	if cursor != nil && (cursor.ID == 0 || cursor.CreatedAt.IsZero()) {
		return ErrOrderValidation
	}
	return nil
}

func buildOrderPage(values []Order, limit int) *OrderPage {
	page := &OrderPage{Orders: values, HasMore: len(values) > limit}
	if page.HasMore {
		page.Orders = page.Orders[:limit]
	}
	if page.Orders == nil {
		page.Orders = []Order{}
	}
	if len(page.Orders) > 0 {
		last := page.Orders[len(page.Orders)-1]
		page.NextCursor = &OrderCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page
}
