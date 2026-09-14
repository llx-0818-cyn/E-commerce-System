package order

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"feedsystem_video_go/internal/storefront"
)

func TestMoneyExactArithmeticAndOverflow(t *testing.T) {
	if _, err := NewMoney(-1); !errors.Is(err, ErrMoneyNegative) {
		t.Fatalf("NewMoney(-1) error = %v, want negative", err)
	}
	unit, _ := NewMoney(1999)
	subtotal, err := unit.Multiply(3)
	if err != nil || subtotal.Cent() != 5997 {
		t.Fatalf("1999 * 3 = %d, %v; want 5997", subtotal.Cent(), err)
	}
	shipping, _ := NewMoney(300)
	total, err := SumMoney(subtotal, shipping)
	if err != nil || total.Cent() != 6297 {
		t.Fatalf("sum = %d, %v; want 6297", total.Cent(), err)
	}
	maximum, _ := NewMoney(math.MaxInt64)
	one, _ := NewMoney(1)
	if _, err := maximum.Add(one); !errors.Is(err, ErrMoneyOverflow) {
		t.Fatalf("max + 1 error = %v, want overflow", err)
	}
	if _, err := maximum.Multiply(2); !errors.Is(err, ErrMoneyOverflow) {
		t.Fatalf("max * 2 error = %v, want overflow", err)
	}
}

func TestOrderNumberGeneratorUsesUTCAndEntropy(t *testing.T) {
	generator := &defaultOrderNumberGenerator{
		now: func() time.Time {
			return time.Date(2026, 8, 24, 20, 34, 56, 789_000_000, time.FixedZone("CST", 8*60*60))
		},
		entropy: bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7}),
	}
	orderNo, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if orderNo != "ORD-20260824123456789-0001020304050607" {
		t.Fatalf("order number = %q", orderNo)
	}
}

func TestOrderItemSnapshotIsDeepCopyAndUsesCurrentPrice(t *testing.T) {
	specs := storefront.SKUSpecs{"颜色": "白色", "尺寸": "L"}
	input := OrderItemSnapshotInput{
		ProductID: 1, ProductName: "原商品名", CoverURL: "/old-cover.jpg",
		SKUID: 2, SKUCode: "SKU-WHITE-L", Specs: specs, UnitPriceCent: 1200, Quantity: 2,
	}
	snapshot, err := NewOrderItemSnapshot(input)
	if err != nil {
		t.Fatalf("NewOrderItemSnapshot() error = %v", err)
	}
	input.ProductName = "改名后商品"
	input.UnitPriceCent = 1800
	specs["颜色"] = "黑色"
	if snapshot.ProductName != "原商品名" || snapshot.UnitPriceCent != 1200 || snapshot.SubtotalCent != 2400 || snapshot.Specs["颜色"] != "白色" {
		t.Fatalf("snapshot changed with source: %#v", snapshot)
	}
	if _, err := NewOrderItemSnapshot(OrderItemSnapshotInput{ProductID: 1, SKUID: 2, ProductName: "商品", SKUCode: "SKU", UnitPriceCent: math.MaxInt64, Quantity: 2}); !errors.Is(err, ErrMoneyOverflow) {
		t.Fatalf("overflow snapshot error = %v, want money overflow", err)
	}
}

func TestOrderStateMachineAllowsOnlyDeclaredTransitions(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	order := &Order{Status: StatusPendingPayment}
	if err := order.Transition(StatusShipped, now); !errors.Is(err, ErrInvalidTransition) || order.Status != StatusPendingPayment {
		t.Fatalf("pending -> shipped = %s, %v", order.Status, err)
	}
	if err := order.Transition(StatusPaid, now); err != nil || order.PaidAt == nil {
		t.Fatalf("pending -> paid = %#v, %v", order, err)
	}
	if err := order.Transition(StatusShipped, now.Add(time.Minute)); err != nil || order.ShippedAt == nil {
		t.Fatalf("paid -> shipped = %#v, %v", order, err)
	}
	if err := order.Transition(StatusCompleted, now.Add(2*time.Minute)); err != nil || order.CompletedAt == nil {
		t.Fatalf("shipped -> completed = %#v, %v", order, err)
	}
	if err := order.Transition(StatusCanceled, now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidTransition) || order.Status != StatusCompleted {
		t.Fatalf("completed -> canceled = %s, %v", order.Status, err)
	}
	canceled := &Order{Status: StatusPendingPayment}
	if err := canceled.Transition(StatusCanceled, now); err != nil || canceled.CanceledAt == nil {
		t.Fatalf("pending -> canceled = %#v, %v", canceled, err)
	}
}
