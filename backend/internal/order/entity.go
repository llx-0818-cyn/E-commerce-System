package order

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"feedsystem_video_go/internal/storefront"
)

var ErrSnapshotValidation = errors.New("invalid order snapshot")

type OrderSubmission struct {
	ID                 uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	BuyerID            uint64    `gorm:"column:buyer_id" json:"buyer_id"`
	IdempotencyKey     string    `gorm:"column:idempotency_key" json:"-"`
	RequestFingerprint string    `gorm:"column:request_fingerprint" json:"-"`
	GrandTotalCent     int64     `gorm:"column:grand_total_cent" json:"grand_total_cent"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (OrderSubmission) TableName() string { return "order_submissions" }

type AddressSnapshot struct {
	AddressID     uint64 `json:"address_id"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	City          string `json:"city"`
	District      string `json:"district"`
	Detail        string `json:"detail"`
	PostalCode    string `json:"postal_code"`
}

func (a AddressSnapshot) Validate() error {
	if a.AddressID == 0 || strings.TrimSpace(a.RecipientName) == "" || strings.TrimSpace(a.Phone) == "" ||
		strings.TrimSpace(a.Province) == "" || strings.TrimSpace(a.City) == "" || strings.TrimSpace(a.Detail) == "" {
		return ErrSnapshotValidation
	}
	return nil
}

func (a *AddressSnapshot) Scan(value any) error {
	data, err := databaseJSONBytes(value)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, a); err != nil {
		return fmt.Errorf("decode address snapshot: %w", err)
	}
	return a.Validate()
}

func (a AddressSnapshot) Value() (driver.Value, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encode address snapshot: %w", err)
	}
	return string(data), nil
}

type Status string

const (
	StatusPendingPayment Status = "pending_payment"
	StatusPaid           Status = "paid"
	StatusShipped        Status = "shipped"
	StatusCompleted      Status = "completed"
	StatusCanceled       Status = "canceled"
)

type Order struct {
	ID              uint64          `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	SubmissionID    uint64          `gorm:"column:submission_id" json:"submission_id"`
	OrderNo         string          `gorm:"column:order_no" json:"order_no"`
	BuyerID         uint64          `gorm:"column:buyer_id" json:"buyer_id"`
	ShopID          uint64          `gorm:"column:shop_id" json:"shop_id"`
	Status          Status          `gorm:"column:status" json:"status"`
	TotalCent       int64           `gorm:"column:total_cent" json:"total_cent"`
	AddressSnapshot AddressSnapshot `gorm:"column:address_snapshot_json;type:json" json:"address_snapshot"`
	ExpireAt        time.Time       `gorm:"column:expire_at" json:"expire_at"`
	PaidAt          *time.Time      `gorm:"column:paid_at" json:"paid_at,omitempty"`
	ShippedAt       *time.Time      `gorm:"column:shipped_at" json:"shipped_at,omitempty"`
	CompletedAt     *time.Time      `gorm:"column:completed_at" json:"completed_at,omitempty"`
	CanceledAt      *time.Time      `gorm:"column:canceled_at" json:"canceled_at,omitempty"`
	CreatedAt       time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time       `gorm:"column:updated_at" json:"updated_at"`
	Items           []OrderItem     `gorm:"foreignKey:OrderID" json:"items,omitempty"`
}

func (Order) TableName() string { return "orders" }

type OrderItem struct {
	ID            uint64              `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	OrderID       uint64              `gorm:"column:order_id" json:"order_id"`
	ProductID     uint64              `gorm:"column:product_id" json:"product_id"`
	ProductName   string              `gorm:"column:product_name" json:"product_name"`
	CoverURL      string              `gorm:"column:cover_url" json:"cover_url"`
	SKUID         uint64              `gorm:"column:sku_id" json:"sku_id"`
	SKUCode       string              `gorm:"column:sku_code" json:"sku_code"`
	Specs         storefront.SKUSpecs `gorm:"column:specs_json;type:json" json:"specs"`
	UnitPriceCent int64               `gorm:"column:unit_price_cent" json:"unit_price_cent"`
	Quantity      uint                `gorm:"column:quantity" json:"quantity"`
	SubtotalCent  int64               `gorm:"column:subtotal_cent" json:"subtotal_cent"`
	CreatedAt     time.Time           `gorm:"column:created_at" json:"created_at"`
}

func (OrderItem) TableName() string { return "order_items" }

type OrderItemSnapshotInput struct {
	ProductID     uint64
	ProductName   string
	CoverURL      string
	SKUID         uint64
	SKUCode       string
	Specs         storefront.SKUSpecs
	UnitPriceCent int64
	Quantity      uint
}

func NewOrderItemSnapshot(input OrderItemSnapshotInput) (*OrderItem, error) {
	input.ProductName = strings.TrimSpace(input.ProductName)
	input.SKUCode = strings.TrimSpace(input.SKUCode)
	if input.ProductID == 0 || input.SKUID == 0 || input.ProductName == "" || input.SKUCode == "" ||
		input.UnitPriceCent <= 0 || input.Quantity == 0 || input.Quantity > 999 {
		return nil, ErrSnapshotValidation
	}
	price, err := NewMoney(input.UnitPriceCent)
	if err != nil {
		return nil, err
	}
	subtotal, err := price.Multiply(input.Quantity)
	if err != nil {
		return nil, err
	}
	return &OrderItem{
		ProductID: input.ProductID, ProductName: input.ProductName, CoverURL: input.CoverURL,
		SKUID: input.SKUID, SKUCode: input.SKUCode, Specs: cloneSKUSpecs(input.Specs),
		UnitPriceCent: input.UnitPriceCent, Quantity: input.Quantity, SubtotalCent: subtotal.Cent(),
	}, nil
}

type InventoryMovement string

const (
	InventoryMovementLock    InventoryMovement = "lock"
	InventoryMovementRelease InventoryMovement = "release"
	InventoryMovementSell    InventoryMovement = "sell"
)

type InventoryLedgerEntry struct {
	ID             uint64            `gorm:"column:id;primaryKey;autoIncrement"`
	EventID        string            `gorm:"column:event_id"`
	OrderID        uint64            `gorm:"column:order_id"`
	SKUID          uint64            `gorm:"column:sku_id"`
	Movement       InventoryMovement `gorm:"column:movement_type"`
	Quantity       int64             `gorm:"column:quantity"`
	AvailableDelta int64             `gorm:"column:available_delta"`
	LockedDelta    int64             `gorm:"column:locked_delta"`
	SoldDelta      int64             `gorm:"column:sold_delta"`
	SKUVersion     uint64            `gorm:"column:sku_version"`
	CreatedAt      time.Time         `gorm:"column:created_at"`
}

func (InventoryLedgerEntry) TableName() string { return "inventory_ledger" }

func cloneSKUSpecs(value storefront.SKUSpecs) storefront.SKUSpecs {
	result := make(storefront.SKUSpecs, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func databaseJSONBytes(value any) ([]byte, error) {
	switch typed := value.(type) {
	case []byte:
		return typed, nil
	case string:
		return []byte(typed), nil
	default:
		return nil, errors.New("unsupported JSON database value")
	}
}
