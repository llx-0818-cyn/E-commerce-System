package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	domainevent "feedsystem_video_go/internal/event"

	"gorm.io/gorm"
)

const (
	ContentExposedEvent = "behavior.content.exposed"
	ContentClickedEvent = "behavior.content.clicked"
	ShopVisitedEvent    = "behavior.shop.visited"
	ProductClickedEvent = "behavior.product.clicked"
	CartItemAddedEvent  = "behavior.cart.item.added"
	OrderCreatedEvent   = "behavior.order.created"
	OrderPaidEvent      = "behavior.order.paid"
)

var ErrBehaviorValidation = errors.New("invalid behavior event")

type BehaviorEvent struct {
	EventType  string         `json:"-"`
	ActorID    uint64         `json:"actor_id,omitempty"`
	PostID     uint64         `json:"post_id,omitempty"`
	ShopID     uint64         `json:"shop_id,omitempty"`
	ProductID  uint64         `json:"product_id,omitempty"`
	SKUID      uint64         `json:"sku_id,omitempty"`
	OrderID    uint64         `json:"order_id,omitempty"`
	PaymentID  uint64         `json:"payment_id,omitempty"`
	Quantity   int64          `json:"quantity,omitempty"`
	AmountCent int64          `json:"amount_cent,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type Recorder struct {
	events *domainevent.Repository
	now    func() time.Time
}

func NewRecorder(db *gorm.DB) (*Recorder, error) {
	events, err := domainevent.NewRepository(db)
	if err != nil {
		return nil, err
	}
	return &Recorder{events: events, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (r *Recorder) RecordContentExposures(ctx context.Context, actorID uint64, postIDs []uint64) error {
	postIDs = uniqueIDs(postIDs)
	if len(postIDs) == 0 {
		return nil
	}
	now := r.now().UTC()
	events := make([]domainevent.DomainEvent, len(postIDs))
	for index, postID := range postIDs {
		value := BehaviorEvent{EventType: ContentExposedEvent, ActorID: actorID, PostID: postID, OccurredAt: now}
		events[index] = domainEvent(value, "post", postID)
	}
	_, err := r.events.AppendStandaloneBatch(ctx, events)
	return err
}

func (r *Recorder) RecordContentClick(ctx context.Context, actorID, postID uint64) error {
	return r.record(ctx, BehaviorEvent{EventType: ContentClickedEvent, ActorID: actorID, PostID: postID}, "post", postID)
}

func (r *Recorder) RecordShopVisit(ctx context.Context, actorID, shopID uint64) error {
	return r.RecordShopVisitFromPost(ctx, actorID, shopID, 0)
}

func (r *Recorder) RecordShopVisitFromPost(ctx context.Context, actorID, shopID, sourcePostID uint64) error {
	return r.record(ctx, BehaviorEvent{EventType: ShopVisitedEvent, ActorID: actorID, ShopID: shopID, PostID: sourcePostID}, "shop", shopID)
}

func (r *Recorder) RecordProductClick(ctx context.Context, actorID, productID, shopID uint64) error {
	return r.RecordProductClickFromPost(ctx, actorID, productID, shopID, 0)
}

func (r *Recorder) RecordProductClickFromPost(ctx context.Context, actorID, productID, shopID, sourcePostID uint64) error {
	return r.record(ctx, BehaviorEvent{EventType: ProductClickedEvent, ActorID: actorID, ProductID: productID, ShopID: shopID, PostID: sourcePostID}, "product", productID)
}

func (r *Recorder) RecordCartItemAdded(ctx context.Context, actorID, skuID, productID uint64, quantity int64) error {
	return r.RecordCartItemAddedFromPost(ctx, actorID, skuID, productID, 0, quantity)
}

func (r *Recorder) RecordCartItemAddedFromPost(ctx context.Context, actorID, skuID, productID, sourcePostID uint64, quantity int64) error {
	if actorID == 0 || quantity <= 0 {
		return ErrBehaviorValidation
	}
	return r.record(ctx, BehaviorEvent{EventType: CartItemAddedEvent, ActorID: actorID, SKUID: skuID, ProductID: productID, PostID: sourcePostID, Quantity: quantity}, "sku", skuID)
}

func (r *Recorder) RecordOrderCreated(ctx context.Context, actorID, orderID, shopID uint64, amountCent int64) error {
	return r.RecordOrderCreatedFromPost(ctx, actorID, orderID, shopID, 0, amountCent)
}

func (r *Recorder) RecordOrderCreatedFromPost(ctx context.Context, actorID, orderID, shopID, sourcePostID uint64, amountCent int64) error {
	if actorID == 0 || amountCent <= 0 {
		return ErrBehaviorValidation
	}
	return r.record(ctx, BehaviorEvent{EventType: OrderCreatedEvent, ActorID: actorID, OrderID: orderID, ShopID: shopID, PostID: sourcePostID, AmountCent: amountCent}, "order", orderID)
}

func (r *Recorder) RecordOrderPaid(ctx context.Context, actorID, orderID, paymentID uint64, amountCent int64) error {
	return r.RecordOrderPaidFromPost(ctx, actorID, orderID, paymentID, 0, amountCent)
}

func (r *Recorder) RecordOrderPaidFromPost(ctx context.Context, actorID, orderID, paymentID, sourcePostID uint64, amountCent int64) error {
	if actorID == 0 || paymentID == 0 || amountCent <= 0 {
		return ErrBehaviorValidation
	}
	return r.record(ctx, BehaviorEvent{EventType: OrderPaidEvent, ActorID: actorID, OrderID: orderID, PaymentID: paymentID, PostID: sourcePostID, AmountCent: amountCent}, "order", orderID)
}

func (r *Recorder) record(ctx context.Context, value BehaviorEvent, aggregateType string, aggregateID uint64) error {
	if r == nil || r.events == nil || aggregateID == 0 || value.EventType == "" {
		return ErrBehaviorValidation
	}
	value.OccurredAt = r.now().UTC()
	_, err := r.events.AppendStandalone(ctx, domainEvent(value, aggregateType, aggregateID))
	return err
}

func domainEvent(value BehaviorEvent, aggregateType string, aggregateID uint64) domainevent.DomainEvent {
	return domainevent.DomainEvent{
		AggregateType: aggregateType,
		AggregateID:   strconv.FormatUint(aggregateID, 10),
		EventType:     value.EventType,
		Payload:       value,
	}
}

func uniqueIDs(ids []uint64) []uint64 {
	result := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

type StatisticsSink interface {
	Apply(context.Context, BehaviorEvent) error
}

type Consumer struct {
	sink StatisticsSink
}

func NewConsumer(sink StatisticsSink) (*Consumer, error) {
	if sink == nil {
		return nil, errors.New("statistics sink is nil")
	}
	return &Consumer{sink: sink}, nil
}

func (c *Consumer) Handle(ctx context.Context, eventType string, payload []byte) error {
	if !IsBehaviorEventType(eventType) || !json.Valid(payload) {
		return ErrBehaviorValidation
	}
	var value BehaviorEvent
	if err := json.Unmarshal(payload, &value); err != nil {
		return fmt.Errorf("decode behavior event: %w", err)
	}
	value.EventType = eventType
	if value.OccurredAt.IsZero() {
		return ErrBehaviorValidation
	}
	return c.sink.Apply(ctx, value)
}

func IsBehaviorEventType(eventType string) bool {
	switch eventType {
	case ContentExposedEvent, ContentClickedEvent, ShopVisitedEvent, ProductClickedEvent,
		CartItemAddedEvent, OrderCreatedEvent, OrderPaidEvent:
		return true
	default:
		return false
	}
}
