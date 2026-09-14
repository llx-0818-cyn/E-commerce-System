package payment

import "time"

type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusClosed    Status = "closed"
)

type PaymentRecord struct {
	ID              uint64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	PaymentNo       string     `gorm:"column:payment_no" json:"payment_no"`
	OrderID         uint64     `gorm:"column:order_id" json:"order_id"`
	BuyerID         uint64     `gorm:"column:buyer_id" json:"buyer_id"`
	Provider        string     `gorm:"column:provider" json:"provider"`
	Status          Status     `gorm:"column:status" json:"status"`
	AmountCent      int64      `gorm:"column:amount_cent" json:"amount_cent"`
	SessionToken    string     `gorm:"column:session_token" json:"session_token"`
	CashierURL      string     `gorm:"column:cashier_url" json:"cashier_url"`
	ProviderTradeNo *string    `gorm:"column:provider_trade_no" json:"provider_trade_no,omitempty"`
	FailureCode     *string    `gorm:"column:failure_code" json:"failure_code,omitempty"`
	FailureReason   *string    `gorm:"column:failure_reason" json:"failure_reason,omitempty"`
	SucceededAt     *time.Time `gorm:"column:succeeded_at" json:"succeeded_at,omitempty"`
	FailedAt        *time.Time `gorm:"column:failed_at" json:"failed_at,omitempty"`
	ClosedAt        *time.Time `gorm:"column:closed_at" json:"closed_at,omitempty"`
	CreatedAt       time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (PaymentRecord) TableName() string { return "payment_records" }

type PaymentSession struct {
	Payment PaymentRecord `json:"payment"`
}

type CallbackEvent struct {
	ID              uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	CallbackEventID string     `gorm:"column:callback_event_id"`
	PaymentID       uint64     `gorm:"column:payment_id"`
	Provider        string     `gorm:"column:provider"`
	ProviderTradeNo string     `gorm:"column:provider_trade_no"`
	Result          string     `gorm:"column:result"`
	AmountCent      int64      `gorm:"column:amount_cent"`
	PayloadJSON     string     `gorm:"column:payload_json"`
	ProcessStatus   string     `gorm:"column:process_status"`
	FailureReason   *string    `gorm:"column:failure_reason"`
	ReceivedAt      time.Time  `gorm:"column:received_at"`
	ProcessedAt     *time.Time `gorm:"column:processed_at"`
}

func (CallbackEvent) TableName() string { return "payment_callback_events" }

type PaymentAnomaly struct {
	ID                 uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	PaymentID          *uint64    `gorm:"column:payment_id"`
	OrderID            *uint64    `gorm:"column:order_id"`
	PaymentNo          string     `gorm:"column:payment_no"`
	CallbackEventID    string     `gorm:"column:callback_event_id"`
	AnomalyType        string     `gorm:"column:anomaly_type"`
	ExpectedAmountCent *int64     `gorm:"column:expected_amount_cent"`
	ActualAmountCent   *int64     `gorm:"column:actual_amount_cent"`
	Detail             string     `gorm:"column:detail"`
	PayloadJSON        string     `gorm:"column:payload_json"`
	ResolvedAt         *time.Time `gorm:"column:resolved_at"`
	Resolution         *string    `gorm:"column:resolution"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
}

func (PaymentAnomaly) TableName() string { return "payment_anomalies" }

type CallbackOutcome struct {
	PaymentID uint64 `json:"payment_id,omitempty"`
	OrderID   uint64 `json:"order_id,omitempty"`
	Status    Status `json:"status,omitempty"`
	Applied   bool   `json:"applied"`
	Duplicate bool   `json:"duplicate"`
	Rejected  bool   `json:"rejected"`
}
