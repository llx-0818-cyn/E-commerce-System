package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCheckoutHTTPAuthenticationIdentityAndEmptyCollections(t *testing.T) {
	service := &fakeAPIService{}
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	router := newCheckoutTestRouter(handler)

	if response := performCheckoutRequest(router, http.MethodGet, "/api/cart", "", 0); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous cart status = %d body=%s, want 401", response.Code, response.Body.String())
	}
	if response := performCheckoutRequest(router, http.MethodGet, "/api/cart", "", 7); response.Code != http.StatusOK || response.Body.String() != `{"items":[]}` {
		t.Fatalf("empty cart status/body = %d/%s, want 200/{\"items\":[]}", response.Code, response.Body.String())
	}
	if response := performCheckoutRequest(router, http.MethodGet, "/api/addresses", "", 7); response.Code != http.StatusOK || response.Body.String() != `{"addresses":[]}` {
		t.Fatalf("empty addresses status/body = %d/%s, want 200/{\"addresses\":[]}", response.Code, response.Body.String())
	}

	addResponse := performCheckoutRequest(router, http.MethodPost, "/api/cart/items", `{
"account_id":999,"sku_id":11,"quantity":2,"source_post_id":23
}`, 7)
	if addResponse.Code != http.StatusCreated {
		t.Fatalf("add cart status = %d body=%s", addResponse.Code, addResponse.Body.String())
	}
	if service.lastAdd.AccountID != 7 || service.lastAdd.SKUID != 11 || service.lastAdd.Quantity != 2 ||
		service.lastAdd.Mode != CartQuantityMerge || service.lastAdd.SourcePostID != 23 {
		t.Fatalf("add input = %#v; authenticated account and default merge mode were not preserved", service.lastAdd)
	}

	previewResponse := performCheckoutRequest(router, http.MethodPost, "/api/checkout/preview", `{
"account_id":999,"cart_item_ids":[5],"address_id":9,"accept_price_changes":true,"total_cent":1
}`, 7)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	if service.lastPreview.AccountID != 7 || len(service.lastPreview.CartItemIDs) != 1 || service.lastPreview.CartItemIDs[0] != 5 ||
		service.lastPreview.AddressID != 9 || !service.lastPreview.AcceptPriceChanges {
		t.Fatalf("preview input = %#v; server identity or request fields are incorrect", service.lastPreview)
	}
}

func TestCheckoutHTTPErrorContract(t *testing.T) {
	service := &fakeAPIService{}
	handler, _ := NewHandler(service)
	router := newCheckoutTestRouter(handler)

	service.addErr = ErrCartSKUUnavailable
	assertCheckoutError(t, performCheckoutRequest(router, http.MethodPost, "/api/cart/items", `{"sku_id":11,"quantity":2}`, 7),
		http.StatusConflict, ErrCartSKUUnavailable.Error())

	service.updateCartErr = ErrCartItemNotFound
	assertCheckoutError(t, performCheckoutRequest(router, http.MethodPatch, "/api/cart/items/88", `{"quantity":1}`, 7),
		http.StatusNotFound, ErrCartItemNotFound.Error())

	service.getAddressErr = ErrAddressNotFound
	assertCheckoutError(t, performCheckoutRequest(router, http.MethodGet, "/api/addresses/99", "", 7),
		http.StatusNotFound, ErrAddressNotFound.Error())

	service.previewErr = ErrCheckoutEmptyCart
	assertCheckoutError(t, performCheckoutRequest(router, http.MethodPost, "/api/checkout/preview", `{}`, 7),
		http.StatusConflict, ErrCheckoutEmptyCart.Error())

	service.previewErr = errors.New("database password must not leak")
	assertCheckoutError(t, performCheckoutRequest(router, http.MethodPost, "/api/checkout/preview", `{}`, 7),
		http.StatusInternalServerError, "internal server error")

	assertCheckoutError(t, performCheckoutRequest(router, http.MethodPost, "/api/cart/items", `{`, 7),
		http.StatusBadRequest, "invalid JSON request body")
	assertCheckoutError(t, performCheckoutRequest(router, http.MethodDelete, "/api/cart/items/not-an-id", "", 7),
		http.StatusBadRequest, "id must be a positive integer")
}

func TestCheckoutOpenAPIContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.json"))
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	wantOperations := map[string][]string{
		"/api/cart": {"get"}, "/api/cart/items": {"post"}, "/api/cart/items/{id}": {"patch", "delete"},
		"/api/addresses": {"get", "post"}, "/api/addresses/{id}": {"get", "patch", "delete"},
		"/api/addresses/{id}/default": {"put"}, "/api/checkout/preview": {"post"},
	}
	for path, methods := range wantOperations {
		for _, method := range methods {
			rawOperation, ok := document.Paths[path][method]
			if !ok {
				t.Errorf("OpenAPI missing %s %s", strings.ToUpper(method), path)
				continue
			}
			var operation struct {
				Security  []map[string]json.RawMessage `json:"security"`
				Responses map[string]json.RawMessage   `json:"responses"`
			}
			if err := json.Unmarshal(rawOperation, &operation); err != nil {
				t.Errorf("decode OpenAPI %s %s: %v", strings.ToUpper(method), path, err)
				continue
			}
			if len(operation.Security) != 1 {
				t.Errorf("OpenAPI %s %s security = %#v, want explicit BearerAuth", strings.ToUpper(method), path, operation.Security)
			}
			if _, ok := operation.Responses["401"]; !ok {
				t.Errorf("OpenAPI %s %s does not document 401", strings.ToUpper(method), path)
			}
		}
	}
	for _, name := range []string{"CartItem", "CartItemsResponse", "Address", "AddressesResponse", "CheckoutPreviewRequest", "CheckoutPreview"} {
		if _, ok := document.Components.Schemas[name]; !ok {
			t.Errorf("OpenAPI missing schema %s", name)
		}
	}
}

func newCheckoutTestRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	protected := router.Group("/api")
	protected.Use(func(c *gin.Context) {
		raw := c.GetHeader("X-Test-Account")
		accountID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || accountID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Set("accountID", uint(accountID))
		c.Next()
	})
	protected.GET("/cart", handler.ListCart)
	protected.POST("/cart/items", handler.AddCartItem)
	protected.PATCH("/cart/items/:id", handler.UpdateCartItem)
	protected.DELETE("/cart/items/:id", handler.DeleteCartItem)
	protected.GET("/addresses", handler.ListAddresses)
	protected.POST("/addresses", handler.CreateAddress)
	protected.GET("/addresses/:id", handler.GetAddress)
	protected.PATCH("/addresses/:id", handler.UpdateAddress)
	protected.DELETE("/addresses/:id", handler.DeleteAddress)
	protected.PUT("/addresses/:id/default", handler.SetDefaultAddress)
	protected.POST("/checkout/preview", handler.PreviewCheckout)
	return router
}

func performCheckoutRequest(router http.Handler, method, path, body string, accountID uint64) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if accountID != 0 {
		request.Header.Set("X-Test-Account", strconv.FormatUint(accountID, 10))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertCheckoutError(t *testing.T, response *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if response.Code != status || response.Body.String() != `{"error":"`+message+`"}` {
		t.Fatalf("error response = %d/%s, want %d/{\"error\":%q}", response.Code, response.Body.String(), status, message)
	}
}

type fakeAPIService struct {
	lastAdd       AddCartInput
	lastPreview   CheckoutPreviewInput
	addErr        error
	updateCartErr error
	getAddressErr error
	previewErr    error
}

func (s *fakeAPIService) AddCartItem(_ context.Context, input AddCartInput) (*CartItem, error) {
	s.lastAdd = input
	return &CartItem{ID: 1, AccountID: input.AccountID, SKUID: input.SKUID, Quantity: uint(input.Quantity)}, s.addErr
}

func (s *fakeAPIService) UpdateCartQuantity(_ context.Context, accountID, itemID uint64, quantity int64) (*CartItem, error) {
	return &CartItem{ID: itemID, AccountID: accountID, Quantity: uint(quantity)}, s.updateCartErr
}

func (s *fakeAPIService) DeleteCartItem(context.Context, uint64, uint64) error { return nil }

func (s *fakeAPIService) ListCart(context.Context, uint64) ([]CartItem, error) { return nil, nil }

func (s *fakeAPIService) CreateAddress(_ context.Context, accountID uint64, input CreateAddressInput) (*Address, error) {
	return &Address{ID: 1, AccountID: accountID, RecipientName: input.RecipientName}, nil
}

func (s *fakeAPIService) UpdateAddress(_ context.Context, accountID, addressID uint64, input AddressInput) (*Address, error) {
	return &Address{ID: addressID, AccountID: accountID, RecipientName: input.RecipientName}, nil
}

func (s *fakeAPIService) SetDefaultAddress(_ context.Context, accountID, addressID uint64) (*Address, error) {
	return &Address{ID: addressID, AccountID: accountID, IsDefault: true}, nil
}

func (s *fakeAPIService) DeleteAddress(context.Context, uint64, uint64) error { return nil }

func (s *fakeAPIService) ListAddresses(context.Context, uint64) ([]Address, error) { return nil, nil }

func (s *fakeAPIService) GetAddress(_ context.Context, accountID, addressID uint64) (*Address, error) {
	return &Address{ID: addressID, AccountID: accountID}, s.getAddressErr
}

func (s *fakeAPIService) PreviewCheckout(_ context.Context, input CheckoutPreviewInput) (*CheckoutPreview, error) {
	s.lastPreview = input
	return &CheckoutPreview{Shops: []CheckoutShopGroup{}}, s.previewErr
}
