package payment

import (
	"context"
	"errors"
	"time"
)

var (
	ErrProviderValidation       = errors.New("invalid payment provider request")
	ErrProviderNotFound         = errors.New("payment provider record not found")
	ErrProviderConflict         = errors.New("payment provider request conflict")
	ErrInvalidCallbackSignature = errors.New("invalid payment callback signature")
)

type ProviderStatus string

const (
	ProviderStatusPending   ProviderStatus = "pending"
	ProviderStatusSucceeded ProviderStatus = "succeeded"
	ProviderStatusFailed    ProviderStatus = "failed"
	ProviderStatusClosed    ProviderStatus = "closed"
)

type CreateRequest struct {
	PaymentNo  string
	OrderNo    string
	BuyerID    uint64
	AmountCent int64
}

type ProviderPayment struct {
	Provider        string         `json:"provider"`
	PaymentNo       string         `json:"payment_no"`
	OrderNo         string         `json:"order_no"`
	BuyerID         uint64         `json:"buyer_id"`
	AmountCent      int64          `json:"amount_cent"`
	Status          ProviderStatus `json:"status"`
	SessionToken    string         `json:"session_token"`
	CashierURL      string         `json:"cashier_url"`
	ProviderTradeNo string         `json:"provider_trade_no,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type CallbackResult string

const (
	CallbackSucceeded CallbackResult = "succeeded"
	CallbackFailed    CallbackResult = "failed"
)

type CallbackPayload struct {
	CallbackEventID string         `json:"callback_event_id"`
	PaymentNo       string         `json:"payment_no"`
	OrderNo         string         `json:"order_no"`
	ProviderTradeNo string         `json:"provider_trade_no"`
	AmountCent      int64          `json:"amount_cent"`
	Result          CallbackResult `json:"result"`
	OccurredAt      time.Time      `json:"occurred_at"`
	Signature       string         `json:"signature"`
}

type VerifiedCallback struct {
	CallbackEventID string
	PaymentNo       string
	OrderNo         string
	ProviderTradeNo string
	AmountCent      int64
	Result          CallbackResult
	OccurredAt      time.Time
}

type PaymentProvider interface {
	Name() string
	Create(context.Context, CreateRequest) (*ProviderPayment, error)
	Query(context.Context, string) (*ProviderPayment, error)
	Close(context.Context, string) (*ProviderPayment, error)
	VerifyCallback(context.Context, CallbackPayload) (*VerifiedCallback, error)
}
