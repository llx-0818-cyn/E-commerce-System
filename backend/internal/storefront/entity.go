package storefront

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"feedsystem_video_go/internal/content"
)

type ShopStatus string

const (
	ShopStatusOpen     ShopStatus = "open"
	ShopStatusClosed   ShopStatus = "closed"
	ShopStatusDisabled ShopStatus = "disabled"
)

type Shop struct {
	ID          uint64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	OwnerID     uint64     `gorm:"column:owner_id" json:"owner_id"`
	Name        string     `gorm:"column:name" json:"name"`
	Description string     `gorm:"column:description" json:"description"`
	AvatarURL   string     `gorm:"column:avatar_url" json:"avatar_url"`
	BannerURL   string     `gorm:"column:banner_url" json:"banner_url"`
	Status      ShopStatus `gorm:"column:status" json:"status"`
	CreatedAt   time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (Shop) TableName() string { return "shops" }

type ShopProfileInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
	BannerURL   string `json:"banner_url"`
}

type CategoryStatus string

const (
	CategoryStatusActive   CategoryStatus = "active"
	CategoryStatusDisabled CategoryStatus = "disabled"
)

type Category struct {
	ID        uint64         `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ParentID  *uint64        `gorm:"column:parent_id" json:"parent_id,omitempty"`
	Name      string         `gorm:"column:name" json:"name"`
	Slug      string         `gorm:"column:slug" json:"slug"`
	Status    CategoryStatus `gorm:"column:status" json:"status"`
	SortOrder uint           `gorm:"column:sort_order" json:"sort_order"`
	CreatedAt time.Time      `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at" json:"updated_at"`
}

func (Category) TableName() string { return "categories" }

type ProductStatus string

const (
	ProductStatusDraft    ProductStatus = "draft"
	ProductStatusOnSale   ProductStatus = "on_sale"
	ProductStatusOffSale  ProductStatus = "off_sale"
	ProductStatusDisabled ProductStatus = "disabled"
)

type Product struct {
	ID          uint64        `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ShopID      uint64        `gorm:"column:shop_id" json:"shop_id"`
	CategoryID  *uint64       `gorm:"column:category_id" json:"category_id,omitempty"`
	Name        string        `gorm:"column:name" json:"name"`
	Subtitle    string        `gorm:"column:subtitle" json:"subtitle"`
	Description string        `gorm:"column:description" json:"description"`
	CoverURL    string        `gorm:"column:cover_url" json:"cover_url"`
	Status      ProductStatus `gorm:"column:status" json:"status"`
	SalesCount  uint64        `gorm:"column:sales_count" json:"sales_count"`
	CreatedAt   time.Time     `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time     `gorm:"column:updated_at" json:"updated_at"`
}

func (Product) TableName() string { return "products" }

type ProductImage struct {
	ID         uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ProductID  uint64    `gorm:"column:product_id" json:"product_id"`
	StorageKey string    `gorm:"column:storage_key" json:"-"`
	ImageURL   string    `gorm:"column:image_url" json:"image_url"`
	MIMEType   string    `gorm:"column:mime_type" json:"mime_type"`
	ByteSize   uint64    `gorm:"column:byte_size" json:"byte_size"`
	Width      uint      `gorm:"column:width" json:"width"`
	Height     uint      `gorm:"column:height" json:"height"`
	SortOrder  uint      `gorm:"column:sort_order" json:"sort_order"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ProductImage) TableName() string { return "product_images" }

type SKUSpecs map[string]string

func (s *SKUSpecs) Scan(value any) error {
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return errors.New("unsupported SKU specs database value")
	}
	if err := json.Unmarshal(data, s); err != nil {
		return err
	}
	if *s == nil {
		*s = SKUSpecs{}
	}
	return nil
}

func (s SKUSpecs) Value() (driver.Value, error) {
	if s == nil {
		s = SKUSpecs{}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

type SKUStatus string

const (
	SKUStatusActive   SKUStatus = "active"
	SKUStatusInactive SKUStatus = "inactive"
	SKUStatusDisabled SKUStatus = "disabled"
)

type ProductSKU struct {
	ID                uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ProductID         uint64    `gorm:"column:product_id" json:"product_id"`
	SKUCode           string    `gorm:"column:sku_code" json:"sku_code"`
	Specs             SKUSpecs  `gorm:"column:specs_json;type:json" json:"specs"`
	PriceCent         int64     `gorm:"column:price_cent" json:"price_cent"`
	OriginalPriceCent *int64    `gorm:"column:original_price_cent" json:"original_price_cent,omitempty"`
	AvailableStock    int64     `gorm:"column:available_stock" json:"available_stock"`
	LockedStock       int64     `gorm:"column:locked_stock" json:"locked_stock"`
	SoldStock         int64     `gorm:"column:sold_stock" json:"sold_stock"`
	Version           uint64    `gorm:"column:version" json:"version"`
	Status            SKUStatus `gorm:"column:status" json:"status"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ProductSKU) TableName() string { return "product_skus" }

type ProductCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint64    `json:"id"`
}

type PublicShop struct {
	Shop            Shop  `json:"shop"`
	ProductCount    int64 `json:"product_count"`
	AcceptingOrders bool  `json:"accepting_orders"`
}

type PublicProductItem struct {
	Product      Product `json:"product"`
	MinPriceCent *int64  `json:"min_price_cent,omitempty"`
	CanPurchase  bool    `json:"can_purchase"`
}

type PublicProductPage struct {
	Products   []PublicProductItem `json:"products"`
	NextCursor *ProductCursor      `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

type PublicSKU struct {
	ID                uint64    `json:"id"`
	SKUCode           string    `json:"sku_code"`
	Specs             SKUSpecs  `json:"specs"`
	PriceCent         int64     `json:"price_cent"`
	OriginalPriceCent *int64    `json:"original_price_cent,omitempty"`
	AvailableStock    int64     `json:"available_stock"`
	Status            SKUStatus `json:"status"`
	CanPurchase       bool      `json:"can_purchase"`
}

type PublicProductDetail struct {
	Product         Product        `json:"product"`
	Shop            PublicShop     `json:"shop"`
	Images          []ProductImage `json:"images"`
	SKUs            []PublicSKU    `json:"skus"`
	MinPriceCent    *int64         `json:"min_price_cent,omitempty"`
	CanPurchase     bool           `json:"can_purchase"`
	RelatedPostsURL string         `json:"related_posts_url"`
}

type SellerDashboard struct {
	ShopID                uint64 `json:"shop_id" gorm:"column:shop_id"`
	ProductCount          int64  `json:"product_count" gorm:"column:product_count"`
	SKUCount              int64  `json:"sku_count" gorm:"column:sku_count"`
	AvailableStock        int64  `json:"available_stock" gorm:"column:available_stock"`
	LockedStock           int64  `json:"locked_stock" gorm:"column:locked_stock"`
	SoldStock             int64  `json:"sold_stock" gorm:"column:sold_stock"`
	PendingShipmentOrders int64  `json:"pending_shipment_orders" gorm:"column:pending_shipment_orders"`
	CompletedOrders       int64  `json:"completed_orders" gorm:"column:completed_orders"`
}

type SellerProductDetail struct {
	Product Product        `json:"product"`
	Images  []ProductImage `json:"images"`
	SKUs    []ProductSKU   `json:"skus"`
}

type SellerProductPage struct {
	Products []Product `json:"products"`
}

type ProductProfileInput struct {
	CategoryID  *uint64 `json:"category_id"`
	Name        string  `json:"name"`
	Subtitle    string  `json:"subtitle"`
	Description string  `json:"description"`
	CoverURL    string  `json:"cover_url"`
}

type ProductImageInput struct {
	StorageKey string `json:"storage_key"`
	ImageURL   string `json:"image_url"`
	MIMEType   string `json:"mime_type"`
	ByteSize   uint64 `json:"byte_size"`
	Width      uint   `json:"width"`
	Height     uint   `json:"height"`
}

type CreateSKUInput struct {
	SKUCode           string   `json:"sku_code"`
	Specs             SKUSpecs `json:"specs"`
	PriceCent         int64    `json:"price_cent"`
	OriginalPriceCent *int64   `json:"original_price_cent"`
	AvailableStock    int64    `json:"available_stock"`
}

type UpdateSKUInput struct {
	SKUCode           string   `json:"sku_code"`
	Specs             SKUSpecs `json:"specs"`
	PriceCent         int64    `json:"price_cent"`
	OriginalPriceCent *int64   `json:"original_price_cent"`
}

type FeedShopSummary struct {
	ID        uint64 `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type FeedProductCard struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	CoverURL     string `json:"cover_url"`
	MinPriceCent int64  `json:"min_price_cent"`
}

type FeedCommerceBatch struct {
	ShopsByOwner   map[uint64]*FeedShopSummary
	ProductsByPost map[uint64][]FeedProductCard
}

type RelatedPostCursor struct {
	PublishedAt time.Time `json:"published_at"`
	ID          uint64    `json:"id"`
}

type RelatedPostPage struct {
	Posts      []content.Post     `json:"posts"`
	NextCursor *RelatedPostCursor `json:"next_cursor,omitempty"`
	HasMore    bool               `json:"has_more"`
}
