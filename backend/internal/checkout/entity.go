package checkout

import (
	"time"

	"feedsystem_video_go/internal/storefront"
)

type CartItem struct {
	ID                uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	AccountID         uint64    `gorm:"column:account_id" json:"account_id"`
	SKUID             uint64    `gorm:"column:sku_id" json:"sku_id"`
	Quantity          uint      `gorm:"column:quantity" json:"quantity"`
	ObservedPriceCent int64     `gorm:"column:observed_price_cent" json:"observed_price_cent"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (CartItem) TableName() string { return "cart_items" }

type CartQuantityMode string

const (
	CartQuantityMerge   CartQuantityMode = "merge"
	CartQuantityReplace CartQuantityMode = "replace"
)

type AddCartInput struct {
	AccountID    uint64
	SKUID        uint64
	Quantity     int64
	Mode         CartQuantityMode
	SourcePostID uint64
}

type Address struct {
	ID               uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	AccountID        uint64    `gorm:"column:account_id" json:"account_id"`
	RecipientName    string    `gorm:"column:recipient_name" json:"recipient_name"`
	Phone            string    `gorm:"column:phone" json:"phone"`
	Province         string    `gorm:"column:province" json:"province"`
	City             string    `gorm:"column:city" json:"city"`
	District         string    `gorm:"column:district" json:"district"`
	Detail           string    `gorm:"column:detail" json:"detail"`
	PostalCode       string    `gorm:"column:postal_code" json:"postal_code"`
	IsDefault        bool      `gorm:"column:is_default" json:"is_default"`
	DefaultAccountID *uint64   `gorm:"column:default_account_id;->" json:"-"`
	CreatedAt        time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Address) TableName() string { return "addresses" }

type AddressInput struct {
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	City          string `json:"city"`
	District      string `json:"district"`
	Detail        string `json:"detail"`
	PostalCode    string `json:"postal_code"`
}

type CreateAddressInput struct {
	AddressInput
	IsDefault bool `json:"is_default"`
}

type CheckoutIssue string

const (
	CheckoutIssueShopUnavailable    CheckoutIssue = "shop_unavailable"
	CheckoutIssueProductUnavailable CheckoutIssue = "product_unavailable"
	CheckoutIssueSKUUnavailable     CheckoutIssue = "sku_unavailable"
	CheckoutIssueStockInsufficient  CheckoutIssue = "stock_insufficient"
)

type CheckoutPreviewInput struct {
	AccountID          uint64
	CartItemIDs        []uint64
	AddressID          uint64
	AcceptPriceChanges bool
}

type CheckoutItem struct {
	CartItemID        uint64              `json:"cart_item_id"`
	ShopID            uint64              `json:"shop_id"`
	ProductID         uint64              `json:"product_id"`
	ProductName       string              `json:"product_name"`
	CoverURL          string              `json:"cover_url"`
	SKUID             uint64              `json:"sku_id"`
	SKUCode           string              `json:"sku_code"`
	Specs             storefront.SKUSpecs `json:"specs"`
	Quantity          uint                `json:"quantity"`
	ObservedPriceCent int64               `json:"observed_price_cent"`
	CurrentPriceCent  int64               `json:"current_price_cent"`
	SubtotalCent      int64               `json:"subtotal_cent"`
	PriceChanged      bool                `json:"price_changed"`
	CanCheckout       bool                `json:"can_checkout"`
	Issue             CheckoutIssue       `json:"issue,omitempty"`
}

type CheckoutShopGroup struct {
	ShopID       uint64         `json:"shop_id"`
	ShopName     string         `json:"shop_name"`
	Items        []CheckoutItem `json:"items"`
	SubtotalCent int64          `json:"subtotal_cent"`
}

type CheckoutPreview struct {
	Address             Address             `json:"address"`
	Shops               []CheckoutShopGroup `json:"shops"`
	TotalCent           int64               `json:"total_cent"`
	HasPriceChanges     bool                `json:"has_price_changes"`
	HasUnavailableItems bool                `json:"has_unavailable_items"`
	CanSubmit           bool                `json:"can_submit"`
}

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

type OrderDraftItem struct {
	CartItemID    uint64              `json:"cart_item_id"`
	ProductID     uint64              `json:"product_id"`
	ProductName   string              `json:"product_name"`
	CoverURL      string              `json:"cover_url"`
	SKUID         uint64              `json:"sku_id"`
	SKUCode       string              `json:"sku_code"`
	Specs         storefront.SKUSpecs `json:"specs"`
	UnitPriceCent int64               `json:"unit_price_cent"`
	Quantity      uint                `json:"quantity"`
	SubtotalCent  int64               `json:"subtotal_cent"`
}

type StoreOrderDraft struct {
	BuyerID         uint64           `json:"buyer_id"`
	ShopID          uint64           `json:"shop_id"`
	AddressSnapshot AddressSnapshot  `json:"address_snapshot"`
	Items           []OrderDraftItem `json:"items"`
	TotalCent       int64            `json:"total_cent"`
}

type OrderDraftPlan struct {
	Drafts     []StoreOrderDraft `json:"drafts"`
	GrandTotal int64             `json:"grand_total_cent"`
}
