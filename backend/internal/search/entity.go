package search

import "time"

type Scope string

type SortMode string

const (
	ScopeContent Scope = "content"
	ScopeProduct Scope = "product"
	ScopeShop    Scope = "shop"

	SortRelevance SortMode = "relevance"
	SortSales     SortMode = "sales"
	SortPriceAsc  SortMode = "price_asc"
	SortPriceDesc SortMode = "price_desc"
)

type Query struct {
	Keyword      string
	Scope        Scope
	Limit        int
	Offset       int
	CategoryID   *uint64
	MinPriceCent *int64
	MaxPriceCent *int64
	Sort         SortMode
}

type Hit struct {
	Type        Scope      `json:"type" gorm:"-"`
	ID          uint64     `json:"id" gorm:"column:id"`
	Title       string     `json:"title" gorm:"column:title"`
	Subtitle    string     `json:"subtitle,omitempty" gorm:"column:subtitle"`
	ImageURL    string     `json:"image_url,omitempty" gorm:"column:image_url"`
	PriceCent   *int64     `json:"price_cent,omitempty" gorm:"column:price_cent"`
	PublishedAt *time.Time `json:"published_at,omitempty" gorm:"column:published_at"`
}

type Page struct {
	Query      string `json:"query"`
	Scope      Scope  `json:"scope"`
	Items      []Hit  `json:"items"`
	HasMore    bool   `json:"has_more"`
	NextOffset *int   `json:"next_offset,omitempty"`
}
