package checkout

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type APIService interface {
	AddCartItem(context.Context, AddCartInput) (*CartItem, error)
	UpdateCartQuantity(context.Context, uint64, uint64, int64) (*CartItem, error)
	DeleteCartItem(context.Context, uint64, uint64) error
	ListCart(context.Context, uint64) ([]CartItem, error)
	CreateAddress(context.Context, uint64, CreateAddressInput) (*Address, error)
	UpdateAddress(context.Context, uint64, uint64, AddressInput) (*Address, error)
	SetDefaultAddress(context.Context, uint64, uint64) (*Address, error)
	DeleteAddress(context.Context, uint64, uint64) error
	ListAddresses(context.Context, uint64) ([]Address, error)
	GetAddress(context.Context, uint64, uint64) (*Address, error)
	PreviewCheckout(context.Context, CheckoutPreviewInput) (*CheckoutPreview, error)
}

type Handler struct {
	service APIService
}

func NewHandler(service APIService) (*Handler, error) {
	if service == nil {
		return nil, errors.New("checkout handler service is nil")
	}
	return &Handler{service: service}, nil
}

type addCartItemRequest struct {
	SKUID        uint64           `json:"sku_id"`
	Quantity     int64            `json:"quantity"`
	Mode         CartQuantityMode `json:"mode"`
	SourcePostID uint64           `json:"source_post_id"`
}

func (h *Handler) AddCartItem(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	var request addCartItemRequest
	if !bindCheckoutJSON(c, &request) {
		return
	}
	if request.Mode == "" {
		request.Mode = CartQuantityMerge
	}
	item, err := h.service.AddCartItem(c.Request.Context(), AddCartInput{
		AccountID: accountID, SKUID: request.SKUID, Quantity: request.Quantity,
		Mode: request.Mode, SourcePostID: request.SourcePostID,
	})
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *Handler) UpdateCartItem(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	itemID, ok := checkoutPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Quantity int64 `json:"quantity"`
	}
	if !bindCheckoutJSON(c, &request) {
		return
	}
	item, err := h.service.UpdateCartQuantity(c.Request.Context(), accountID, itemID, request.Quantity)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *Handler) DeleteCartItem(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	itemID, ok := checkoutPathID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteCartItem(c.Request.Context(), accountID, itemID); err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "cart item deleted"})
}

func (h *Handler) ListCart(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	items, err := h.service.ListCart(c.Request.Context(), accountID)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	if items == nil {
		items = []CartItem{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) CreateAddress(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	var input CreateAddressInput
	if !bindCheckoutJSON(c, &input) {
		return
	}
	address, err := h.service.CreateAddress(c.Request.Context(), accountID, input)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusCreated, address)
}

func (h *Handler) UpdateAddress(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	addressID, ok := checkoutPathID(c, "id")
	if !ok {
		return
	}
	var input AddressInput
	if !bindCheckoutJSON(c, &input) {
		return
	}
	address, err := h.service.UpdateAddress(c.Request.Context(), accountID, addressID, input)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, address)
}

func (h *Handler) SetDefaultAddress(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	addressID, ok := checkoutPathID(c, "id")
	if !ok {
		return
	}
	address, err := h.service.SetDefaultAddress(c.Request.Context(), accountID, addressID)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, address)
}

func (h *Handler) DeleteAddress(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	addressID, ok := checkoutPathID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteAddress(c.Request.Context(), accountID, addressID); err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "address deleted"})
}

func (h *Handler) ListAddresses(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	addresses, err := h.service.ListAddresses(c.Request.Context(), accountID)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	if addresses == nil {
		addresses = []Address{}
	}
	c.JSON(http.StatusOK, gin.H{"addresses": addresses})
}

func (h *Handler) GetAddress(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	addressID, ok := checkoutPathID(c, "id")
	if !ok {
		return
	}
	address, err := h.service.GetAddress(c.Request.Context(), accountID, addressID)
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, address)
}

func (h *Handler) PreviewCheckout(c *gin.Context) {
	accountID, ok := checkoutAccountID(c)
	if !ok {
		return
	}
	var request struct {
		CartItemIDs        []uint64 `json:"cart_item_ids"`
		AddressID          uint64   `json:"address_id"`
		AcceptPriceChanges bool     `json:"accept_price_changes"`
	}
	if !bindCheckoutJSON(c, &request) {
		return
	}
	preview, err := h.service.PreviewCheckout(c.Request.Context(), CheckoutPreviewInput{
		AccountID: accountID, CartItemIDs: request.CartItemIDs, AddressID: request.AddressID,
		AcceptPriceChanges: request.AcceptPriceChanges,
	})
	if err != nil {
		writeCheckoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

func checkoutAccountID(c *gin.Context) (uint64, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uint64(accountID), true
}

func checkoutPathID(c *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be a positive integer"})
		return 0, false
	}
	return id, true
}

func bindCheckoutJSON(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON request body"})
		return false
	}
	return true
}

func writeCheckoutError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, ErrCartValidation), errors.Is(err, ErrAddressValidation), errors.Is(err, ErrCheckoutValidation):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrCartItemNotFound), errors.Is(err, ErrAddressNotFound), errors.Is(err, ErrCheckoutCartItemNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, ErrCartSKUUnavailable), errors.Is(err, ErrCheckoutEmptyCart),
		errors.Is(err, ErrCheckoutAmountOverflow), errors.Is(err, ErrCheckoutNotReady):
		status, message = http.StatusConflict, err.Error()
	}
	c.JSON(status, gin.H{"error": message})
}
