package promotion

import "time"

type CampaignStatus string

const (
	CampaignStatusDraft    CampaignStatus = "draft"
	CampaignStatusEnabled  CampaignStatus = "enabled"
	CampaignStatusDisabled CampaignStatus = "disabled"
	CampaignStatusEnded    CampaignStatus = "ended"
)

type FlashSaleCampaign struct {
	ID              uint64         `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CampaignNo      string         `gorm:"column:campaign_no" json:"campaign_no"`
	SKUID           uint64         `gorm:"column:sku_id" json:"sku_id"`
	Title           string         `gorm:"column:title" json:"title"`
	CampaignVersion uint64         `gorm:"column:campaign_version" json:"campaign_version"`
	Status          CampaignStatus `gorm:"column:status" json:"status"`
	PriceCent       int64          `gorm:"column:price_cent" json:"price_cent"`
	TotalStock      int64          `gorm:"column:total_stock" json:"total_stock"`
	PerUserLimit    uint           `gorm:"column:per_user_limit" json:"per_user_limit"`
	StartsAt        time.Time      `gorm:"column:starts_at" json:"starts_at"`
	EndsAt          time.Time      `gorm:"column:ends_at" json:"ends_at"`
	CreatedAt       time.Time      `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at" json:"updated_at"`
}

func (FlashSaleCampaign) TableName() string { return "flash_sale_campaigns" }

type FlashSaleRequest struct {
	ID               uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	RequestID        string    `gorm:"column:request_id"`
	CampaignID       uint64    `gorm:"column:campaign_id"`
	BuyerID          uint64    `gorm:"column:buyer_id"`
	IdempotencyKey   string    `gorm:"column:idempotency_key"`
	ReservationToken string    `gorm:"column:reservation_token"`
	Quantity         uint      `gorm:"column:quantity"`
	Status           string    `gorm:"column:status"`
	OrderID          *uint64   `gorm:"column:order_id"`
	FailureCode      *string   `gorm:"column:failure_code"`
	FailureReason    *string   `gorm:"column:failure_reason"`
	AcceptedAt       time.Time `gorm:"column:accepted_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (FlashSaleRequest) TableName() string { return "flash_sale_requests" }

type ReconciliationAnomaly struct {
	ID            uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	CampaignID    uint64     `gorm:"column:campaign_id"`
	AnomalyKey    string     `gorm:"column:anomaly_key"`
	AnomalyType   string     `gorm:"column:anomaly_type"`
	RequestID     *string    `gorm:"column:request_id"`
	OrderID       *uint64    `gorm:"column:order_id"`
	ExpectedValue *int64     `gorm:"column:expected_value"`
	ActualValue   *int64     `gorm:"column:actual_value"`
	Detail        string     `gorm:"column:detail"`
	EvidenceJSON  string     `gorm:"column:evidence_json"`
	Status        string     `gorm:"column:status"`
	Resolution    *string    `gorm:"column:resolution"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	ResolvedAt    *time.Time `gorm:"column:resolved_at"`
}

func (ReconciliationAnomaly) TableName() string { return "flash_sale_reconciliation_anomalies" }

type CreateCampaignInput struct {
	OwnerID      uint64
	SKUID        uint64
	Title        string
	PriceCent    int64
	TotalStock   int64
	PerUserLimit uint
	StartsAt     time.Time
	EndsAt       time.Time
}

type PreheatResult struct {
	CampaignID       uint64 `json:"campaign_id"`
	CampaignVersion  uint64 `json:"campaign_version"`
	MetadataKey      string `json:"metadata_key"`
	StockKey         string `json:"stock_key"`
	BuyersKey        string `json:"buyers_key"`
	RequestsKey      string `json:"requests_key"`
	StockInitialized bool   `json:"stock_initialized"`
}
