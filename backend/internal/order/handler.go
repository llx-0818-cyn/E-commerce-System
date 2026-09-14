package order

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type APIService interface {
	CreateOrderGroup(context.Context, CreateOrderGroupInput) (*OrderGroup, error)
	ListBuyerOrders(context.Context, uint64, Status, *OrderCursor, int) (*OrderPage, error)
	ListShopOrders(context.Context, uint64, uint64, Status, *OrderCursor, int) (*OrderPage, error)
	GetOrderForActor(context.Context, uint64, uint64) (*Order, error)
	CancelOrder(context.Context, uint64, uint64) (*Order, error)
	ShipOrder(context.Context, uint64, uint64) (*Order, error)
	ConfirmReceipt(context.Context, uint64, uint64) (*Order, error)
}

type Handler struct {
	service APIService
}

func NewHandler(service APIService) (*Handler, error) {
	if service == nil {
		return nil, errors.New("order handler service is nil")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) Create(c *gin.Context) {
	buyerID, ok := orderActorID(c)
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Idempotency-Key header is required and must not exceed 128 characters"})
		return
	}
	var request struct {
		AddressID uint64                 `json:"address_id"`
		Drafts    []CreateShopOrderInput `json:"drafts"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group, err := h.service.CreateOrderGroup(c.Request.Context(), CreateOrderGroupInput{
		BuyerID: buyerID, IdempotencyKey: idempotencyKey, AddressID: request.AddressID, Drafts: request.Drafts,
	})
	if err != nil {
		writeOrderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, group)
}

func (h *Handler) ListBuyer(c *gin.Context) {
	buyerID, ok := orderActorID(c)
	if !ok {
		return
	}
	status, cursor, limit, ok := orderPageQuery(c)
	if !ok {
		return
	}
	page, err := h.service.ListBuyerOrders(c.Request.Context(), buyerID, status, cursor, limit)
	if err != nil {
		writeOrderError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) Get(c *gin.Context) {
	actorID, ok := orderActorID(c)
	if !ok {
		return
	}
	orderID, ok := orderPathID(c)
	if !ok {
		return
	}
	value, err := h.service.GetOrderForActor(c.Request.Context(), actorID, orderID)
	if err != nil {
		writeOrderError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}

func (h *Handler) Cancel(c *gin.Context) {
	h.transitionBuyer(c, h.service.CancelOrder)
}

func (h *Handler) Confirm(c *gin.Context) {
	h.transitionBuyer(c, h.service.ConfirmReceipt)
}

func (h *Handler) transitionBuyer(c *gin.Context, transition func(context.Context, uint64, uint64) (*Order, error)) {
	buyerID, ok := orderActorID(c)
	if !ok {
		return
	}
	orderID, ok := orderPathID(c)
	if !ok {
		return
	}
	value, err := transition(c.Request.Context(), buyerID, orderID)
	if err != nil {
		writeOrderError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}

func (h *Handler) ListShop(c *gin.Context) {
	ownerID, ok := orderActorID(c)
	if !ok {
		return
	}
	shopID, err := strconv.ParseUint(c.Query("shop_id"), 10, 64)
	if err != nil || shopID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shop_id is required"})
		return
	}
	status, cursor, limit, ok := orderPageQuery(c)
	if !ok {
		return
	}
	page, err := h.service.ListShopOrders(c.Request.Context(), ownerID, shopID, status, cursor, limit)
	if err != nil {
		writeOrderError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) Ship(c *gin.Context) {
	ownerID, ok := orderActorID(c)
	if !ok {
		return
	}
	orderID, ok := orderPathID(c)
	if !ok {
		return
	}
	value, err := h.service.ShipOrder(c.Request.Context(), ownerID, orderID)
	if err != nil {
		writeOrderError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}

func orderPageQuery(c *gin.Context) (Status, *OrderCursor, int, bool) {
	status := Status(strings.TrimSpace(c.Query("status")))
	if status != "" && !status.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order status"})
		return "", nil, 0, false
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be in 1..100"})
			return "", nil, 0, false
		}
		limit = value
	}
	beforeTime, beforeID := strings.TrimSpace(c.Query("before_time")), strings.TrimSpace(c.Query("before_id"))
	if (beforeTime == "") != (beforeID == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time and before_id must be provided together"})
		return "", nil, 0, false
	}
	var cursor *OrderCursor
	if beforeTime != "" {
		parsedTime, timeErr := time.Parse(time.RFC3339Nano, beforeTime)
		parsedID, idErr := strconv.ParseUint(beforeID, 10, 64)
		if timeErr != nil || idErr != nil || parsedID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order cursor"})
			return "", nil, 0, false
		}
		cursor = &OrderCursor{CreatedAt: parsedTime.UTC(), ID: parsedID}
	}
	return status, cursor, limit, true
}

func orderActorID(c *gin.Context) (uint64, bool) {
	value, err := jwt.GetAccountID(c)
	if err != nil || value == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uint64(value), true
}

func orderPathID(c *gin.Context) (uint64, bool) {
	value, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || value == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return 0, false
	}
	return value, true
}

func writeOrderError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrOrderValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAddressNotFound), errors.Is(err, ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrOrderForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, ErrCatalogUnavailable), errors.Is(err, ErrPriceChanged), errors.Is(err, ErrStockInsufficient),
		errors.Is(err, ErrIdempotencyConflict), errors.Is(err, ErrOrderStateConflict), errors.Is(err, ErrOrderNotExpired):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "order operation failed"})
	}
}
