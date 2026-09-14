package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEnhancedSearchHTTPContractPassesProductFiltersAndSort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapter := &capturingSearchAdapter{}
	service := &Service{repository: &Repository{}, adapter: adapter, enhanced: true}
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	router := gin.New()
	router.GET("/api/search", handler.Search)
	request := httptest.NewRequest(http.MethodGet, "/api/search?q=杯子&scope=product&category_id=7&min_price_cent=1000&max_price_cent=9000&sort=sales&limit=12&offset=24", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("enhanced search status = %d body=%s", response.Code, response.Body.String())
	}
	query := adapter.query
	if query.Keyword != "杯子" || query.Scope != ScopeProduct || query.CategoryID == nil || *query.CategoryID != 7 ||
		query.MinPriceCent == nil || *query.MinPriceCent != 1000 || query.MaxPriceCent == nil || *query.MaxPriceCent != 9000 ||
		query.Sort != SortSales || query.Limit != 13 || query.Offset != 24 {
		t.Fatalf("adapter query = %#v", query)
	}
}

type capturingSearchAdapter struct {
	query Query
}

func (a *capturingSearchAdapter) Search(_ context.Context, query Query) ([]Hit, error) {
	a.query = query
	return []Hit{}, nil
}
