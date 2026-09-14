package promotion

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/middleware/ratelimit"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	redis "github.com/redis/go-redis/v9"
)

func TestFlashSaleHTTPAuthenticationAcceptanceDuplicateStatusAndDependencyFailure(t *testing.T) {
	application := &fakeFlashSaleApplication{reserve: &ReservationResult{Code: ReservationAccepted, RequestID: "c1-v1-request", BuyerID: 7}}
	handler, _ := NewHandler(application)
	router := newFlashSaleTestRouter(handler)
	if response := performFlashSaleRequest(router, http.MethodPost, "/api/flash-sales/1/requests", `{"idempotency_key":"key","quantity":1}`, 0); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous accept status = %d body=%s", response.Code, response.Body.String())
	}
	accepted := performFlashSaleRequest(router, http.MethodPost, "/api/flash-sales/1/requests", `{"idempotency_key":"key","quantity":1}`, 7)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("accepted status = %d body=%s", accepted.Code, accepted.Body.String())
	}
	application.reserve = &ReservationResult{Code: ReservationAccepted, RequestID: "c1-v1-request", BuyerID: 7, Duplicate: true}
	duplicate := performFlashSaleRequest(router, http.MethodPost, "/api/flash-sales/1/requests", `{"idempotency_key":"key","quantity":1}`, 7)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
	status := performFlashSaleRequest(router, http.MethodGet, "/api/flash-sale-requests/c1-v1-request", "", 7)
	if status.Code != http.StatusOK {
		t.Fatalf("status query = %d body=%s", status.Code, status.Body.String())
	}
	application.reserveErr = fmt.Errorf("%w: redis unavailable", ErrPreheatUnavailable)
	unavailable := performFlashSaleRequest(router, http.MethodPost, "/api/flash-sales/1/requests", `{"idempotency_key":"other","quantity":1}`, 7)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency failure = %d body=%s", unavailable.Code, unavailable.Body.String())
	}
}

func TestFlashSaleHTTPRateLimit(t *testing.T) {
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()
	raw := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer raw.Close()
	cache := rediscache.NewClient(raw, "test:")
	application := &fakeFlashSaleApplication{reserve: &ReservationResult{Code: ReservationAccepted, RequestID: "c1-v1-request", BuyerID: 7}}
	handler, _ := NewHandler(application)
	limiter := ratelimit.Limit(cache, "flash-sale-test", 1, time.Minute, ratelimit.KeyByAccount)
	router := newFlashSaleTestRouter(handler, limiter)
	first := performFlashSaleRequest(router, http.MethodPost, "/api/flash-sales/1/requests", `{"idempotency_key":"one","quantity":1}`, 7)
	second := performFlashSaleRequest(router, http.MethodPost, "/api/flash-sales/1/requests", `{"idempotency_key":"two","quantity":1}`, 7)
	if first.Code != http.StatusAccepted || second.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit statuses = %d/%d, want 202/429", first.Code, second.Code)
	}
}

func newFlashSaleTestRouter(handler *Handler, middleware ...gin.HandlerFunc) *gin.Engine {
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
	protected.POST("/flash-sales/:id/requests", append(middleware, handler.Accept)...)
	protected.GET("/flash-sale-requests/:request_id", handler.Status)
	return router
}

func performFlashSaleRequest(router http.Handler, method, path, body string, accountID uint64) *httptest.ResponseRecorder {
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

type fakeFlashSaleApplication struct {
	reserve    *ReservationResult
	reserveErr error
}

func (f *fakeFlashSaleApplication) ReserveCampaign(context.Context, uint64, uint64, string, uint) (*ReservationResult, error) {
	return f.reserve, f.reserveErr
}

func (f *fakeFlashSaleApplication) GetReservationStatus(_ context.Context, buyerID uint64, _ string) (*ReservationResult, error) {
	if f.reserve == nil {
		return nil, ErrReservationNotFound
	}
	value := *f.reserve
	value.BuyerID = buyerID
	return &value, nil
}
