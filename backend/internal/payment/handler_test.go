package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMockPaymentRoutesEnvironmentAuthenticationResultsAndRepeat(t *testing.T) {
	application := &fakePaymentApplication{}
	simulator := &fakeMockSimulator{}
	handler, err := NewHandler(application, simulator, "test", true)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	router := newMockPaymentTestRouter(handler)
	if response := performMockPaymentRequest(router, "/api/mock-payments/PAY-1/succeed", 0); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous success status = %d body=%s", response.Code, response.Body.String())
	}
	for path, wantResult := range map[string]CallbackResult{
		"/api/mock-payments/PAY-1/succeed": CallbackSucceeded,
		"/api/mock-payments/PAY-1/fail":    CallbackFailed,
	} {
		response := performMockPaymentRequest(router, path, 7)
		if response.Code != http.StatusOK {
			t.Fatalf("POST %s status = %d body=%s", path, response.Code, response.Body.String())
		}
		if simulator.lastResult != wantResult || application.lastBuyerID != 7 || application.lastPaymentNo != "PAY-1" {
			t.Fatalf("POST %s simulator/application = %#v/%#v", path, simulator, application)
		}
	}
	repeat := performMockPaymentRequest(router, "/api/mock-payments/PAY-1/repeat", 7)
	if repeat.Code != http.StatusOK || simulator.repeatCount != 1 || application.callbackCount != 3 {
		t.Fatalf("repeat status/body/counts = %d/%s/%d/%d", repeat.Code, repeat.Body.String(), simulator.repeatCount, application.callbackCount)
	}

	disabled, _ := NewHandler(application, simulator, "production", false)
	disabledRouter := newMockPaymentTestRouter(disabled)
	if response := performMockPaymentRequest(disabledRouter, "/api/mock-payments/PAY-1/succeed", 7); response.Code != http.StatusNotFound {
		t.Fatalf("disabled production route status = %d, want 404", response.Code)
	}
	if _, err := NewHandler(application, simulator, "production", true); err == nil {
		t.Fatal("NewHandler(production enabled) error = nil")
	}
}

func newMockPaymentTestRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	protected := router.Group("/api")
	protected.Use(func(c *gin.Context) {
		accountID, err := strconv.ParseUint(c.GetHeader("X-Test-Account"), 10, 64)
		if err != nil || accountID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Set("accountID", uint(accountID))
		c.Next()
	})
	protected.POST("/payments", handler.CreatePayment)
	protected.GET("/payments/:payment_no", handler.QueryPayment)
	handler.RegisterMockRoutes(protected)
	return router
}

func performMockPaymentRequest(router http.Handler, path string, accountID uint64) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	if accountID != 0 {
		request.Header.Set("X-Test-Account", strconv.FormatUint(accountID, 10))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type fakePaymentApplication struct {
	lastBuyerID   uint64
	lastPaymentNo string
	callbackCount int
}

func (f *fakePaymentApplication) CreatePayment(_ context.Context, input CreatePaymentInput) (*PaymentSession, error) {
	f.lastBuyerID = input.BuyerID
	return &PaymentSession{Payment: PaymentRecord{ID: 1, PaymentNo: "PAY-1", BuyerID: input.BuyerID, OrderID: input.OrderID, AmountCent: input.AmountCent}}, nil
}

func (f *fakePaymentApplication) QueryPayment(_ context.Context, buyerID uint64, paymentNo string) (*PaymentSession, error) {
	f.lastBuyerID, f.lastPaymentNo = buyerID, paymentNo
	return &PaymentSession{Payment: PaymentRecord{ID: 1, PaymentNo: paymentNo, BuyerID: buyerID}}, nil
}

func (f *fakePaymentApplication) ProcessCallback(_ context.Context, payload CallbackPayload) (*CallbackOutcome, error) {
	f.callbackCount++
	return &CallbackOutcome{PaymentID: 1, OrderID: 2, Status: StatusSucceeded, Applied: true}, nil
}

type fakeMockSimulator struct {
	lastResult  CallbackResult
	repeatCount int
}

func (f *fakeMockSimulator) Simulate(_ context.Context, paymentNo string, result CallbackResult) (CallbackPayload, error) {
	f.lastResult = result
	return CallbackPayload{CallbackEventID: "event-" + string(result), PaymentNo: paymentNo, Result: result, OccurredAt: time.Now()}, nil
}

func (f *fakeMockSimulator) RepeatCallback(_ context.Context, paymentNo string) (CallbackPayload, error) {
	f.repeatCount++
	return CallbackPayload{CallbackEventID: "event-repeat", PaymentNo: paymentNo, Result: f.lastResult, OccurredAt: time.Now()}, nil
}
