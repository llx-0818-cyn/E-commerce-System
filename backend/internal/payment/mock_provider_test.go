package payment

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMockPaymentProviderCreateQueryCloseAndConflicts(t *testing.T) {
	provider, err := NewMockPaymentProvider("/api/mock-payments/cashier", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewMockPaymentProvider() error = %v", err)
	}
	provider.now = func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }
	provider.entropy = bytes.NewReader(bytes.Repeat([]byte{0x11}, 32))
	request := CreateRequest{PaymentNo: "PAY-1", OrderNo: "ORD-1", BuyerID: 7, AmountCent: 9900}
	created, err := provider.Create(context.Background(), request)
	if err != nil || created.Status != ProviderStatusPending || len(created.SessionToken) != 64 ||
		created.CashierURL != "/api/mock-payments/cashier/"+created.SessionToken {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	replayed, err := provider.Create(context.Background(), request)
	if err != nil || replayed.SessionToken != created.SessionToken {
		t.Fatalf("Create(replay) = %#v, %v", replayed, err)
	}
	if _, err := provider.Create(context.Background(), CreateRequest{PaymentNo: "PAY-1", OrderNo: "ORD-1", BuyerID: 7, AmountCent: 1}); !errors.Is(err, ErrProviderConflict) {
		t.Fatalf("Create(conflict) error = %v", err)
	}
	queried, err := provider.Query(context.Background(), "PAY-1")
	if err != nil || queried.SessionToken != created.SessionToken {
		t.Fatalf("Query() = %#v, %v", queried, err)
	}
	closed, err := provider.Close(context.Background(), "PAY-1")
	if err != nil || closed.Status != ProviderStatusClosed {
		t.Fatalf("Close() = %#v, %v", closed, err)
	}
	repeatedClose, err := provider.Close(context.Background(), "PAY-1")
	if err != nil || repeatedClose.Status != ProviderStatusClosed {
		t.Fatalf("Close(replay) = %#v, %v", repeatedClose, err)
	}
	if _, err := provider.Query(context.Background(), "missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("Query(missing) error = %v", err)
	}
}

func TestMockPaymentProviderVerifyCallbackSignatureAndFields(t *testing.T) {
	provider, _ := NewMockPaymentProvider("/mock/cashier", "0123456789abcdef0123456789abcdef")
	payload := CallbackPayload{
		CallbackEventID: "callback-1", PaymentNo: "PAY-1", OrderNo: "ORD-1",
		ProviderTradeNo: "MOCK-TRADE-1", AmountCent: 9900, Result: CallbackSucceeded,
		OccurredAt: time.Date(2026, 8, 24, 12, 0, 0, 123, time.UTC),
	}
	payload = provider.signedCallback(payload)
	verified, err := provider.VerifyCallback(context.Background(), payload)
	if err != nil || verified.PaymentNo != payload.PaymentNo || verified.AmountCent != payload.AmountCent || verified.Result != CallbackSucceeded {
		t.Fatalf("VerifyCallback() = %#v, %v", verified, err)
	}
	tampered := payload
	tampered.AmountCent = 1
	if _, err := provider.VerifyCallback(context.Background(), tampered); !errors.Is(err, ErrInvalidCallbackSignature) {
		t.Fatalf("VerifyCallback(tampered) error = %v", err)
	}
	invalid := payload
	invalid.Signature = strings.Repeat("z", 64)
	if _, err := provider.VerifyCallback(context.Background(), invalid); !errors.Is(err, ErrInvalidCallbackSignature) {
		t.Fatalf("VerifyCallback(invalid signature) error = %v", err)
	}
}

func TestMockPaymentProviderSimulateAndRepeatCallback(t *testing.T) {
	provider, _ := NewMockPaymentProvider("/mock/cashier", "0123456789abcdef0123456789abcdef")
	provider.now = func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }
	provider.entropy = bytes.NewReader(append(bytes.Repeat([]byte{0x01}, 32), bytes.Repeat([]byte{0x02}, 32)...))
	if _, err := provider.Create(context.Background(), CreateRequest{PaymentNo: "PAY-SIM", OrderNo: "ORD-SIM", BuyerID: 7, AmountCent: 9900}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	failed, err := provider.Simulate(context.Background(), "PAY-SIM", CallbackFailed)
	if err != nil || failed.Result != CallbackFailed || failed.Signature == "" {
		t.Fatalf("Simulate(failed) = %#v, %v", failed, err)
	}
	repeated, err := provider.RepeatCallback(context.Background(), "PAY-SIM")
	if err != nil || repeated != failed {
		t.Fatalf("RepeatCallback() = %#v, %v; want %#v", repeated, err, failed)
	}
	if verified, err := provider.VerifyCallback(context.Background(), repeated); err != nil || verified.Result != CallbackFailed {
		t.Fatalf("VerifyCallback(repeated) = %#v, %v", verified, err)
	}
}
