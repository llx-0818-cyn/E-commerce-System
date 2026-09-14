package order

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestOrderHTTPUsesAuthenticatedActorsIdempotencyHeaderAndCursorContract(t *testing.T) {
	application := &fakeOrderApplication{}
	handler, err := NewHandler(application)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	router := newOrderTestRouter(handler)

	unauthorized := performOrderRequest(router, http.MethodPost, "/api/orders", `{}`, "", 0)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized create status = %d", unauthorized.Code)
	}
	missingKey := performOrderRequest(router, http.MethodPost, "/api/orders", `{"address_id":1,"drafts":[]}`, "", 7)
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key status = %d", missingKey.Code)
	}
	created := performOrderRequest(router, http.MethodPost, "/api/orders", `{"address_id":9,"drafts":[{"shop_id":3,"items":[{"sku_id":5,"quantity":2,"expected_price_cent":9900}]}]}`, "create-key", 7)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	if application.created.BuyerID != 7 || application.created.IdempotencyKey != "create-key" || application.created.AddressID != 9 || application.created.Drafts[0].Items[0].SKUID != 5 {
		t.Fatalf("create input = %#v", application.created)
	}

	before := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	listed := performOrderRequest(router, http.MethodGet, "/api/orders?status=paid&limit=10&before_time="+before+"&before_id=22", "", "", 7)
	if listed.Code != http.StatusOK || application.buyerID != 7 || application.status != StatusPaid || application.limit != 10 || application.cursor == nil || application.cursor.ID != 22 {
		t.Fatalf("buyer list status/capture = %d / %#v", listed.Code, application)
	}
	invalidCursor := performOrderRequest(router, http.MethodGet, "/api/orders?before_id=22", "", "", 7)
	if invalidCursor.Code != http.StatusBadRequest {
		t.Fatalf("incomplete cursor status = %d", invalidCursor.Code)
	}

	shopOrders := performOrderRequest(router, http.MethodGet, "/api/seller/orders?shop_id=4", "", "", 88)
	if shopOrders.Code != http.StatusOK || application.ownerID != 88 || application.shopID != 4 {
		t.Fatalf("shop list status/capture = %d / %#v", shopOrders.Code, application)
	}
	for _, path := range []string{"/api/orders/11/cancel", "/api/orders/11/confirm", "/api/seller/orders/11/ship"} {
		response := performOrderRequest(router, http.MethodPost, path, "", "", 7)
		if response.Code != http.StatusOK {
			t.Fatalf("transition %s status = %d", path, response.Code)
		}
	}
	if application.cancelActor != 7 || application.confirmActor != 7 || application.shipActor != 7 {
		t.Fatalf("transition actors = %d/%d/%d", application.cancelActor, application.confirmActor, application.shipActor)
	}
}

func newOrderTestRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	api.Use(func(c *gin.Context) {
		if raw := c.GetHeader("X-Test-Account"); raw != "" {
			var id uint
			_, _ = fmt.Sscan(raw, &id)
			c.Set("accountID", id)
		}
		c.Next()
	})
	api.POST("/orders", handler.Create)
	api.GET("/orders", handler.ListBuyer)
	api.GET("/orders/:id", handler.Get)
	api.POST("/orders/:id/cancel", handler.Cancel)
	api.POST("/orders/:id/confirm", handler.Confirm)
	api.GET("/seller/orders", handler.ListShop)
	api.POST("/seller/orders/:id/ship", handler.Ship)
	return router
}

func performOrderRequest(router http.Handler, method, path, body, idempotencyKey string, accountID uint) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if accountID != 0 {
		request.Header.Set("X-Test-Account", strconv.FormatUint(uint64(accountID), 10))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type fakeOrderApplication struct {
	created      CreateOrderGroupInput
	buyerID      uint64
	ownerID      uint64
	shopID       uint64
	status       Status
	cursor       *OrderCursor
	limit        int
	cancelActor  uint64
	confirmActor uint64
	shipActor    uint64
}

func (f *fakeOrderApplication) CreateOrderGroup(_ context.Context, input CreateOrderGroupInput) (*OrderGroup, error) {
	f.created = input
	return &OrderGroup{Submission: OrderSubmission{ID: 1}, Orders: []Order{}}, nil
}

func (f *fakeOrderApplication) ListBuyerOrders(_ context.Context, buyerID uint64, status Status, cursor *OrderCursor, limit int) (*OrderPage, error) {
	f.buyerID, f.status, f.cursor, f.limit = buyerID, status, cursor, limit
	return &OrderPage{Orders: []Order{}}, nil
}

func (f *fakeOrderApplication) ListShopOrders(_ context.Context, ownerID, shopID uint64, status Status, cursor *OrderCursor, limit int) (*OrderPage, error) {
	f.ownerID, f.shopID, f.status, f.cursor, f.limit = ownerID, shopID, status, cursor, limit
	return &OrderPage{Orders: []Order{}}, nil
}

func (f *fakeOrderApplication) GetOrderForActor(_ context.Context, actorID, orderID uint64) (*Order, error) {
	return &Order{ID: orderID, BuyerID: actorID}, nil
}

func (f *fakeOrderApplication) CancelOrder(_ context.Context, actorID, orderID uint64) (*Order, error) {
	f.cancelActor = actorID
	return &Order{ID: orderID, Status: StatusCanceled}, nil
}

func (f *fakeOrderApplication) ConfirmReceipt(_ context.Context, actorID, orderID uint64) (*Order, error) {
	f.confirmActor = actorID
	return &Order{ID: orderID, Status: StatusCompleted}, nil
}

func (f *fakeOrderApplication) ShipOrder(_ context.Context, actorID, orderID uint64) (*Order, error) {
	f.shipActor = actorID
	return &Order{ID: orderID, Status: StatusShipped}, nil
}
