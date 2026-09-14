package payment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const MockProviderName = "mock"

type MockPaymentProvider struct {
	mu          sync.RWMutex
	payments    map[string]ProviderPayment
	callbacks   map[string]CallbackPayload
	cashierBase string
	secret      []byte
	now         func() time.Time
	entropy     io.Reader
}

func NewMockPaymentProvider(cashierBase, secret string) (*MockPaymentProvider, error) {
	cashierBase = strings.TrimRight(strings.TrimSpace(cashierBase), "/")
	secret = strings.TrimSpace(secret)
	if cashierBase == "" || len(secret) < 16 {
		return nil, ErrProviderValidation
	}
	return &MockPaymentProvider{
		payments: make(map[string]ProviderPayment), callbacks: make(map[string]CallbackPayload),
		cashierBase: cashierBase, secret: []byte(secret),
		now: func() time.Time { return time.Now().UTC() }, entropy: rand.Reader,
	}, nil
}

func (p *MockPaymentProvider) Name() string {
	return MockProviderName
}

func (p *MockPaymentProvider) Create(_ context.Context, request CreateRequest) (*ProviderPayment, error) {
	request.PaymentNo = strings.TrimSpace(request.PaymentNo)
	request.OrderNo = strings.TrimSpace(request.OrderNo)
	if request.PaymentNo == "" || len(request.PaymentNo) > 40 || request.OrderNo == "" || request.BuyerID == 0 || request.AmountCent <= 0 {
		return nil, ErrProviderValidation
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.payments[request.PaymentNo]; ok {
		if existing.OrderNo != request.OrderNo || existing.BuyerID != request.BuyerID || existing.AmountCent != request.AmountCent {
			return nil, ErrProviderConflict
		}
		return cloneProviderPayment(existing), nil
	}
	token, err := randomHex(p.entropy, 32)
	if err != nil {
		return nil, fmt.Errorf("create mock cashier session: %w", err)
	}
	now := p.now().UTC()
	value := ProviderPayment{
		Provider: MockProviderName, PaymentNo: request.PaymentNo, OrderNo: request.OrderNo,
		BuyerID: request.BuyerID, AmountCent: request.AmountCent, Status: ProviderStatusPending,
		SessionToken: token, CashierURL: p.cashierBase + "/" + token, CreatedAt: now, UpdatedAt: now,
	}
	p.payments[value.PaymentNo] = value
	return cloneProviderPayment(value), nil
}

func (p *MockPaymentProvider) Query(_ context.Context, paymentNo string) (*ProviderPayment, error) {
	paymentNo = strings.TrimSpace(paymentNo)
	if paymentNo == "" {
		return nil, ErrProviderValidation
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	value, ok := p.payments[paymentNo]
	if !ok {
		return nil, ErrProviderNotFound
	}
	return cloneProviderPayment(value), nil
}

func (p *MockPaymentProvider) Close(_ context.Context, paymentNo string) (*ProviderPayment, error) {
	paymentNo = strings.TrimSpace(paymentNo)
	if paymentNo == "" {
		return nil, ErrProviderValidation
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	value, ok := p.payments[paymentNo]
	if !ok {
		return nil, ErrProviderNotFound
	}
	if value.Status == ProviderStatusSucceeded {
		return nil, ErrProviderConflict
	}
	if value.Status != ProviderStatusClosed {
		value.Status = ProviderStatusClosed
		value.UpdatedAt = p.now().UTC()
		p.payments[paymentNo] = value
	}
	return cloneProviderPayment(value), nil
}

func (p *MockPaymentProvider) VerifyCallback(_ context.Context, payload CallbackPayload) (*VerifiedCallback, error) {
	payload.CallbackEventID = strings.TrimSpace(payload.CallbackEventID)
	payload.PaymentNo = strings.TrimSpace(payload.PaymentNo)
	payload.OrderNo = strings.TrimSpace(payload.OrderNo)
	payload.ProviderTradeNo = strings.TrimSpace(payload.ProviderTradeNo)
	payload.Signature = strings.TrimSpace(payload.Signature)
	if payload.CallbackEventID == "" || len(payload.CallbackEventID) > 64 || payload.PaymentNo == "" ||
		payload.OrderNo == "" || payload.ProviderTradeNo == "" || payload.AmountCent <= 0 || payload.OccurredAt.IsZero() ||
		(payload.Result != CallbackSucceeded && payload.Result != CallbackFailed) {
		return nil, ErrProviderValidation
	}
	expected := p.signCallback(payload)
	provided, err := hex.DecodeString(payload.Signature)
	if err != nil || !hmac.Equal(provided, expected) {
		return nil, ErrInvalidCallbackSignature
	}
	return &VerifiedCallback{
		CallbackEventID: payload.CallbackEventID, PaymentNo: payload.PaymentNo,
		OrderNo: payload.OrderNo, ProviderTradeNo: payload.ProviderTradeNo,
		AmountCent: payload.AmountCent, Result: payload.Result, OccurredAt: payload.OccurredAt.UTC(),
	}, nil
}

func (p *MockPaymentProvider) Simulate(_ context.Context, paymentNo string, result CallbackResult) (CallbackPayload, error) {
	paymentNo = strings.TrimSpace(paymentNo)
	if paymentNo == "" || (result != CallbackSucceeded && result != CallbackFailed) {
		return CallbackPayload{}, ErrProviderValidation
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	value, ok := p.payments[paymentNo]
	if !ok {
		return CallbackPayload{}, ErrProviderNotFound
	}
	if value.Status == ProviderStatusClosed || value.Status == ProviderStatusSucceeded {
		return CallbackPayload{}, ErrProviderConflict
	}
	eventID, err := randomHex(p.entropy, 16)
	if err != nil {
		return CallbackPayload{}, err
	}
	tradeID, err := randomHex(p.entropy, 16)
	if err != nil {
		return CallbackPayload{}, err
	}
	now := p.now().UTC()
	payload := CallbackPayload{
		CallbackEventID: eventID, PaymentNo: value.PaymentNo, OrderNo: value.OrderNo,
		ProviderTradeNo: "MOCK-" + tradeID, AmountCent: value.AmountCent, Result: result, OccurredAt: now,
	}
	payload = p.signedCallback(payload)
	value.ProviderTradeNo = payload.ProviderTradeNo
	value.UpdatedAt = now
	if result == CallbackSucceeded {
		value.Status = ProviderStatusSucceeded
	} else {
		value.Status = ProviderStatusFailed
	}
	p.payments[paymentNo] = value
	p.callbacks[paymentNo] = payload
	return payload, nil
}

func (p *MockPaymentProvider) RepeatCallback(_ context.Context, paymentNo string) (CallbackPayload, error) {
	paymentNo = strings.TrimSpace(paymentNo)
	if paymentNo == "" {
		return CallbackPayload{}, ErrProviderValidation
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	payload, ok := p.callbacks[paymentNo]
	if !ok {
		return CallbackPayload{}, ErrProviderNotFound
	}
	return payload, nil
}

func (p *MockPaymentProvider) signCallback(payload CallbackPayload) []byte {
	canonical := strings.Join([]string{
		payload.CallbackEventID,
		payload.PaymentNo,
		payload.OrderNo,
		payload.ProviderTradeNo,
		fmt.Sprintf("%d", payload.AmountCent),
		string(payload.Result),
		payload.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, "\n")
	mac := hmac.New(sha256.New, p.secret)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func (p *MockPaymentProvider) signedCallback(payload CallbackPayload) CallbackPayload {
	payload.Signature = hex.EncodeToString(p.signCallback(payload))
	return payload
}

func randomHex(reader io.Reader, bytes int) (string, error) {
	if reader == nil || bytes <= 0 {
		return "", errors.New("random source and byte count are required")
	}
	buffer := make([]byte, bytes)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func cloneProviderPayment(value ProviderPayment) *ProviderPayment {
	copy := value
	return &copy
}

var _ PaymentProvider = (*MockPaymentProvider)(nil)
