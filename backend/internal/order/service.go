package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/storefront"

	mysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

const (
	OrderCreatedEvent         = "order.created"
	OrderPaidEvent            = "order.paid"
	OrderExpireRequestedEvent = "order.expire.requested"
)

var (
	ErrOrderValidation     = errors.New("invalid order request")
	ErrAddressNotFound     = errors.New("order address not found")
	ErrCatalogUnavailable  = errors.New("order catalog item is unavailable")
	ErrPriceChanged        = errors.New("order price changed")
	ErrStockInsufficient   = errors.New("order stock is insufficient")
	ErrIdempotencyConflict = errors.New("order idempotency key conflicts with another request")
)

var errSubmissionAlreadyExists = errors.New("order submission already exists")

type CreateOrderItemInput struct {
	SKUID             uint64 `json:"sku_id"`
	Quantity          uint   `json:"quantity"`
	ExpectedPriceCent int64  `json:"expected_price_cent"`
}

type CreateShopOrderInput struct {
	ShopID uint64                 `json:"shop_id"`
	Items  []CreateOrderItemInput `json:"items"`
}

type CreateOrderGroupInput struct {
	BuyerID        uint64                 `json:"-"`
	IdempotencyKey string                 `json:"-"`
	AddressID      uint64                 `json:"address_id"`
	Drafts         []CreateShopOrderInput `json:"drafts"`
}

type OrderGroup struct {
	Submission OrderSubmission `json:"submission"`
	Orders     []Order         `json:"orders"`
}

type Service struct {
	repository      *Repository
	events          *domainevent.Repository
	orderNumbers    OrderNumberGenerator
	paymentValidFor time.Duration
	now             func() time.Time
}

func NewService(repository *Repository, paymentValidFor time.Duration) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("order service repository is nil")
	}
	if paymentValidFor <= 0 {
		return nil, errors.New("order payment validity must be positive")
	}
	events, err := domainevent.NewRepository(repository.db)
	if err != nil {
		return nil, err
	}
	return &Service{
		repository: repository, events: events, orderNumbers: NewOrderNumberGenerator(),
		paymentValidFor: paymentValidFor, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) CreateOrderGroup(ctx context.Context, input CreateOrderGroupInput) (*OrderGroup, error) {
	return s.createOrderGroup(ctx, input, false)
}

type CreateFlashSaleOrderInput struct {
	RequestID string
	BuyerID   uint64
	AddressID uint64
	ShopID    uint64
	SKUID     uint64
	Quantity  uint
	PriceCent int64
}

func (s *Service) CreateFlashSaleOrder(ctx context.Context, input CreateFlashSaleOrderInput) (*OrderGroup, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.RequestID == "" || len(input.RequestID) > 64 {
		return nil, ErrOrderValidation
	}
	return s.createOrderGroup(ctx, CreateOrderGroupInput{
		BuyerID: input.BuyerID, IdempotencyKey: "seckill:" + input.RequestID, AddressID: input.AddressID,
		Drafts: []CreateShopOrderInput{{ShopID: input.ShopID, Items: []CreateOrderItemInput{{
			SKUID: input.SKUID, Quantity: input.Quantity, ExpectedPriceCent: input.PriceCent,
		}}}},
	}, true)
}

func (s *Service) createOrderGroup(ctx context.Context, input CreateOrderGroupInput, trustedPriceOverride bool) (*OrderGroup, error) {
	normalized, fingerprint, grandTotal, err := normalizeCreateOrderGroup(input)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, err := s.repository.findSubmissionByKey(ctx, normalized.BuyerID, normalized.IdempotencyKey); err == nil {
		return s.replayOrderGroup(ctx, existing, fingerprint)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var group *OrderGroup
	err = s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		submission := OrderSubmission{
			BuyerID: normalized.BuyerID, IdempotencyKey: normalized.IdempotencyKey,
			RequestFingerprint: fingerprint, GrandTotalCent: grandTotal.Cent(),
		}
		if err := tx.WithContext(ctx).Create(&submission).Error; isMySQLDuplicateKey(err) {
			return errSubmissionAlreadyExists
		} else if err != nil {
			return fmt.Errorf("create order submission: %w", err)
		}
		address, err := loadOwnedAddressSnapshot(ctx, tx, normalized.BuyerID, normalized.AddressID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAddressNotFound
		}
		if err != nil {
			return err
		}

		catalogBySKU := make(map[uint64]*catalogOrderRow)
		versionBySKU := make(map[uint64]uint64)
		for _, reference := range sortedOrderItemReferences(normalized.Drafts) {
			row, err := loadCatalogOrderRowForUpdate(ctx, tx, reference.item.SKUID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCatalogUnavailable
			}
			if err != nil {
				return err
			}
			if row.ShopID != reference.shopID || row.ShopStatus != storefront.ShopStatusOpen ||
				row.ProductStatus != storefront.ProductStatusOnSale || row.SKUStatus != storefront.SKUStatusActive {
				return ErrCatalogUnavailable
			}
			if !trustedPriceOverride && row.PriceCent != reference.item.ExpectedPriceCent {
				return ErrPriceChanged
			}
			if row.AvailableStock < int64(reference.item.Quantity) {
				return ErrStockInsufficient
			}
			version, err := lockSKUStock(ctx, tx, row, reference.item.Quantity)
			if err != nil {
				return err
			}
			catalogBySKU[row.SKUID] = row
			versionBySKU[row.SKUID] = version
		}

		now := s.now().UTC()
		expireAt := now.Add(s.paymentValidFor)
		orders := make([]Order, 0, len(normalized.Drafts))
		for _, draft := range normalized.Drafts {
			orderNo, err := s.orderNumbers.Generate()
			if err != nil {
				return err
			}
			total, err := draftTotal(draft.Items)
			if err != nil {
				return err
			}
			created := Order{
				SubmissionID: submission.ID, OrderNo: orderNo, BuyerID: normalized.BuyerID,
				ShopID: draft.ShopID, Status: StatusPendingPayment, TotalCent: total.Cent(),
				AddressSnapshot: address, ExpireAt: expireAt,
			}
			if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
				return fmt.Errorf("create order: %w", err)
			}
			created.Items = make([]OrderItem, 0, len(draft.Items))
			ledger := make([]InventoryLedgerEntry, 0, len(draft.Items))
			for _, item := range draft.Items {
				catalog := catalogBySKU[item.SKUID]
				unitPrice := catalog.PriceCent
				if trustedPriceOverride {
					unitPrice = item.ExpectedPriceCent
				}
				snapshot, err := NewOrderItemSnapshot(OrderItemSnapshotInput{
					ProductID: catalog.ProductID, ProductName: catalog.ProductName, CoverURL: catalog.CoverURL,
					SKUID: catalog.SKUID, SKUCode: catalog.SKUCode, Specs: catalog.Specs,
					UnitPriceCent: unitPrice, Quantity: item.Quantity,
				})
				if err != nil {
					return err
				}
				snapshot.OrderID = created.ID
				created.Items = append(created.Items, *snapshot)
				ledger = append(ledger, InventoryLedgerEntry{
					EventID: inventoryLedgerEventID(orderNo, item.SKUID, InventoryMovementLock),
					OrderID: created.ID, SKUID: item.SKUID, Movement: InventoryMovementLock,
					Quantity: int64(item.Quantity), AvailableDelta: -int64(item.Quantity),
					LockedDelta: int64(item.Quantity), SKUVersion: versionBySKU[item.SKUID],
				})
			}
			if err := tx.WithContext(ctx).Create(&created.Items).Error; err != nil {
				return fmt.Errorf("create order items: %w", err)
			}
			if err := tx.WithContext(ctx).Create(&ledger).Error; err != nil {
				return fmt.Errorf("create inventory ledger: %w", err)
			}
			payload := map[string]any{
				"submission_id": submission.ID, "order_id": created.ID, "order_no": created.OrderNo,
				"buyer_id": created.BuyerID, "shop_id": created.ShopID, "total_cent": created.TotalCent,
				"expire_at": created.ExpireAt.Format(time.RFC3339Nano),
			}
			if _, err := outbox.Append(ctx, domainevent.DomainEvent{
				AggregateType: "order", AggregateID: strconv.FormatUint(created.ID, 10),
				EventType: OrderCreatedEvent, Payload: payload,
			}); err != nil {
				return err
			}
			if _, err := outbox.Append(ctx, domainevent.DomainEvent{
				AggregateType: "order", AggregateID: strconv.FormatUint(created.ID, 10),
				EventType: OrderExpireRequestedEvent, Payload: payload,
			}); err != nil {
				return err
			}
			orders = append(orders, created)
		}
		group = &OrderGroup{Submission: submission, Orders: orders}
		return nil
	})
	if errors.Is(err, errSubmissionAlreadyExists) {
		existing, loadErr := s.repository.findSubmissionByKey(ctx, normalized.BuyerID, normalized.IdempotencyKey)
		if loadErr != nil {
			return nil, loadErr
		}
		return s.replayOrderGroup(ctx, existing, fingerprint)
	}
	if err != nil {
		return nil, err
	}
	return group, nil
}

func (s *Service) replayOrderGroup(ctx context.Context, submission *OrderSubmission, fingerprint string) (*OrderGroup, error) {
	if submission.RequestFingerprint != fingerprint {
		return nil, ErrIdempotencyConflict
	}
	return s.repository.loadOrderGroup(ctx, submission)
}

func normalizeCreateOrderGroup(input CreateOrderGroupInput) (CreateOrderGroupInput, string, Money, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.BuyerID == 0 || input.AddressID == 0 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(input.Drafts) == 0 {
		return CreateOrderGroupInput{}, "", Money{}, ErrOrderValidation
	}
	result := CreateOrderGroupInput{
		BuyerID: input.BuyerID, IdempotencyKey: input.IdempotencyKey, AddressID: input.AddressID,
		Drafts: make([]CreateShopOrderInput, len(input.Drafts)),
	}
	seenShops := make(map[uint64]struct{}, len(input.Drafts))
	seenSKUs := make(map[uint64]struct{})
	for draftIndex, draft := range input.Drafts {
		if draft.ShopID == 0 || len(draft.Items) == 0 {
			return CreateOrderGroupInput{}, "", Money{}, ErrOrderValidation
		}
		if _, duplicate := seenShops[draft.ShopID]; duplicate {
			return CreateOrderGroupInput{}, "", Money{}, ErrOrderValidation
		}
		seenShops[draft.ShopID] = struct{}{}
		result.Drafts[draftIndex] = CreateShopOrderInput{ShopID: draft.ShopID, Items: append([]CreateOrderItemInput(nil), draft.Items...)}
		for _, item := range draft.Items {
			if item.SKUID == 0 || item.Quantity == 0 || item.Quantity > 999 || item.ExpectedPriceCent <= 0 {
				return CreateOrderGroupInput{}, "", Money{}, ErrOrderValidation
			}
			if _, duplicate := seenSKUs[item.SKUID]; duplicate {
				return CreateOrderGroupInput{}, "", Money{}, ErrOrderValidation
			}
			seenSKUs[item.SKUID] = struct{}{}
		}
		sort.Slice(result.Drafts[draftIndex].Items, func(i, j int) bool {
			return result.Drafts[draftIndex].Items[i].SKUID < result.Drafts[draftIndex].Items[j].SKUID
		})
	}
	sort.Slice(result.Drafts, func(i, j int) bool { return result.Drafts[i].ShopID < result.Drafts[j].ShopID })
	grandTotal := Money{}
	for _, draft := range result.Drafts {
		total, err := draftTotal(draft.Items)
		if err != nil {
			return CreateOrderGroupInput{}, "", Money{}, err
		}
		grandTotal, err = grandTotal.Add(total)
		if err != nil {
			return CreateOrderGroupInput{}, "", Money{}, err
		}
	}
	fingerprintPayload := struct {
		AddressID uint64                 `json:"address_id"`
		Drafts    []CreateShopOrderInput `json:"drafts"`
	}{AddressID: result.AddressID, Drafts: result.Drafts}
	payload, err := json.Marshal(fingerprintPayload)
	if err != nil {
		return CreateOrderGroupInput{}, "", Money{}, err
	}
	hash := sha256.Sum256(payload)
	return result, hex.EncodeToString(hash[:]), grandTotal, nil
}

func draftTotal(items []CreateOrderItemInput) (Money, error) {
	total := Money{}
	for _, item := range items {
		price, err := NewMoney(item.ExpectedPriceCent)
		if err != nil {
			return Money{}, err
		}
		subtotal, err := price.Multiply(item.Quantity)
		if err != nil {
			return Money{}, err
		}
		total, err = total.Add(subtotal)
		if err != nil {
			return Money{}, err
		}
	}
	return total, nil
}

type orderItemReference struct {
	shopID uint64
	item   CreateOrderItemInput
}

func sortedOrderItemReferences(drafts []CreateShopOrderInput) []orderItemReference {
	references := make([]orderItemReference, 0)
	for _, draft := range drafts {
		for _, item := range draft.Items {
			references = append(references, orderItemReference{shopID: draft.ShopID, item: item})
		}
	}
	sort.Slice(references, func(i, j int) bool { return references[i].item.SKUID < references[j].item.SKUID })
	return references
}

func inventoryLedgerEventID(orderNo string, skuID uint64, movement InventoryMovement) string {
	value := orderNo + ":" + strconv.FormatUint(skuID, 10) + ":" + string(movement)
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func isMySQLDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
