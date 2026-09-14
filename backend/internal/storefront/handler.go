package storefront

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service  *Service
	behavior StorefrontBehaviorRecorder
}

type StorefrontBehaviorRecorder interface {
	RecordShopVisitFromPost(context.Context, uint64, uint64, uint64) error
	RecordProductClickFromPost(context.Context, uint64, uint64, uint64, uint64) error
}

func NewHandler(service *Service, behavior ...StorefrontBehaviorRecorder) (*Handler, error) {
	if service == nil || service.repository == nil {
		return nil, errors.New("storefront handler service is nil")
	}
	if len(behavior) > 1 {
		return nil, errors.New("storefront handler accepts at most one behavior recorder")
	}
	handler := &Handler{service: service}
	if len(behavior) == 1 {
		handler.behavior = behavior[0]
	}
	return handler, nil
}

func (h *Handler) GetShop(c *gin.Context) {
	shopID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	shop, err := h.service.GetPublicShop(c.Request.Context(), shopID)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	if h.behavior != nil {
		viewerID, _ := jwt.GetAccountID(c)
		if err := h.behavior.RecordShopVisitFromPost(c.Request.Context(), uint64(viewerID), shop.Shop.ID, optionalSourcePostID(c)); err != nil {
			log.Printf("record shop visit failed: shop_id=%d err=%v", shop.Shop.ID, err)
		}
	}
	c.JSON(http.StatusOK, shop)
}

func (h *Handler) GetSellerDashboard(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	dashboard, err := h.service.GetSellerDashboard(c.Request.Context(), actorID)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, dashboard)
}

func (h *Handler) GetOwnedShop(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	shop, err := h.service.GetOwnedShop(c.Request.Context(), actorID)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, shop)
}

func (h *Handler) ListSellerProducts(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be in 1..100"})
			return
		}
		limit = value
	}
	page, err := h.service.ListSellerProducts(c.Request.Context(), actorID, limit)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) GetSellerProduct(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	detail, err := h.service.GetSellerProduct(c.Request.Context(), actorID, productID)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) ListShopProducts(c *gin.Context) {
	shopID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	limit, cursor, ok := storefrontProductPageQuery(c)
	if !ok {
		return
	}
	page, err := h.service.ListPublicProducts(c.Request.Context(), shopID, limit, cursor)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) GetProduct(c *gin.Context) {
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	product, err := h.service.GetPublicProduct(c.Request.Context(), productID)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	if h.behavior != nil {
		viewerID, _ := jwt.GetAccountID(c)
		if err := h.behavior.RecordProductClickFromPost(c.Request.Context(), uint64(viewerID), product.Product.ID, product.Product.ShopID, optionalSourcePostID(c)); err != nil {
			log.Printf("record product click failed: product_id=%d err=%v", product.Product.ID, err)
		}
	}
	c.JSON(http.StatusOK, product)
}

func (h *Handler) ListProductPosts(c *gin.Context) {
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	limit, cursor, ok := relatedPostPageQuery(c)
	if !ok {
		return
	}
	page, err := h.service.ListRelatedPublicPosts(c.Request.Context(), productID, limit, cursor)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) CreateShop(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	var input ShopProfileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	shop, err := h.service.CreateShop(c.Request.Context(), actorID, input)
	if errors.Is(err, ErrShopAlreadyExists) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "shop_id": shop.ID})
		return
	}
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusCreated, shop)
}

func (h *Handler) UpdateShop(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	shopID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var input ShopProfileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	shop, err := h.service.UpdateShop(c.Request.Context(), actorID, shopID, input)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, shop)
}

func (h *Handler) SetShopStatus(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	shopID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Status ShopStatus `json:"status"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var shop *Shop
	var err error
	switch request.Status {
	case ShopStatusOpen:
		shop, err = h.service.OpenShop(c.Request.Context(), actorID, shopID)
	case ShopStatusClosed:
		shop, err = h.service.CloseShop(c.Request.Context(), actorID, shopID)
	default:
		err = ErrShopValidation
	}
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, shop)
}

type productWriteRequest struct {
	CategoryID  *uint64             `json:"category_id"`
	Name        string              `json:"name"`
	Subtitle    string              `json:"subtitle"`
	Description string              `json:"description"`
	CoverURL    string              `json:"cover_url"`
	Images      []ProductImageInput `json:"images"`
}

func (r productWriteRequest) profile() ProductProfileInput {
	return ProductProfileInput{CategoryID: r.CategoryID, Name: r.Name, Subtitle: r.Subtitle, Description: r.Description, CoverURL: r.CoverURL}
}

func (h *Handler) CreateProduct(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	var request productWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	product, err := h.service.CreateProduct(c.Request.Context(), actorID, request.profile(), request.Images)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusCreated, product)
}

func (h *Handler) UpdateProduct(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var request productWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	product, err := h.service.UpdateProduct(c.Request.Context(), actorID, productID, request.profile())
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *Handler) ReplaceProductImages(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Images []ProductImageInput `json:"images"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	images, err := h.service.ReplaceProductImages(c.Request.Context(), actorID, productID, request.Images)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": images})
}

func (h *Handler) SetProductStatus(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Status ProductStatus `json:"status"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var product *Product
	var err error
	switch request.Status {
	case ProductStatusOnSale:
		product, err = h.service.PublishProduct(c.Request.Context(), actorID, productID)
	case ProductStatusOffSale:
		product, err = h.service.OffSaleProduct(c.Request.Context(), actorID, productID)
	default:
		err = ErrProductValidation
	}
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *Handler) CreateSKU(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	productID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var input CreateSKUInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sku, err := h.service.CreateSKU(c.Request.Context(), actorID, productID, input)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusCreated, sku)
}

func (h *Handler) UpdateSKU(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	skuID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var input UpdateSKUInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sku, err := h.service.UpdateSKU(c.Request.Context(), actorID, skuID, input)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, sku)
}

func (h *Handler) SetSKUStatus(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	skuID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Status SKUStatus `json:"status"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sku, err := h.service.SetSKUStatus(c.Request.Context(), actorID, skuID, request.Status)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, sku)
}

func (h *Handler) AdjustSKUStock(c *gin.Context) {
	actorID, ok := storefrontActorID(c)
	if !ok {
		return
	}
	skuID, ok := storefrontPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Delta int64 `json:"delta"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sku, err := h.service.AdjustSKUStock(c.Request.Context(), actorID, skuID, request.Delta)
	if err != nil {
		respondStorefrontError(c, err)
		return
	}
	c.JSON(http.StatusOK, sku)
}

func storefrontActorID(c *gin.Context) (uint64, bool) {
	actorID, err := jwt.GetAccountID(c)
	if err != nil || actorID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uint64(actorID), true
}

func storefrontPathID(c *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be a positive integer"})
		return 0, false
	}
	return id, true
}

func optionalSourcePostID(c *gin.Context) uint64 {
	raw := strings.TrimSpace(c.Query("source_post_id"))
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func storefrontProductPageQuery(c *gin.Context) (int, *ProductCursor, bool) {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be in 1..100"})
			return 0, nil, false
		}
		limit = value
	}
	timeRaw, idRaw := strings.TrimSpace(c.Query("before_time")), strings.TrimSpace(c.Query("before_id"))
	if timeRaw == "" && idRaw == "" {
		return limit, nil, true
	}
	if timeRaw == "" || idRaw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time and before_id must be provided together"})
		return 0, nil, false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, timeRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time must be RFC3339"})
		return 0, nil, false
	}
	id, err := strconv.ParseUint(idRaw, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_id must be a positive integer"})
		return 0, nil, false
	}
	return limit, &ProductCursor{CreatedAt: createdAt, ID: id}, true
}

func relatedPostPageQuery(c *gin.Context) (int, *RelatedPostCursor, bool) {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be in 1..100"})
			return 0, nil, false
		}
		limit = value
	}
	timeRaw, idRaw := strings.TrimSpace(c.Query("before_time")), strings.TrimSpace(c.Query("before_id"))
	if timeRaw == "" && idRaw == "" {
		return limit, nil, true
	}
	if timeRaw == "" || idRaw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time and before_id must be provided together"})
		return 0, nil, false
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, timeRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time must be RFC3339"})
		return 0, nil, false
	}
	id, err := strconv.ParseUint(idRaw, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_id must be a positive integer"})
		return 0, nil, false
	}
	return limit, &RelatedPostCursor{PublishedAt: publishedAt, ID: id}, true
}

func respondStorefrontError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrShopValidation), errors.Is(err, ErrProductValidation), errors.Is(err, ErrSKUValidation):
		status = http.StatusBadRequest
	case errors.Is(err, ErrShopForbidden), errors.Is(err, ErrProductForbidden):
		status = http.StatusForbidden
	case errors.Is(err, ErrShopNotFound), errors.Is(err, ErrProductNotFound), errors.Is(err, ErrSKUNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrShopAlreadyExists), errors.Is(err, ErrShopDisabled), errors.Is(err, ErrProductDisabled),
		errors.Is(err, ErrShopNotOpen), errors.Is(err, ErrProductNoSellableSKU), errors.Is(err, ErrSKUDisabled),
		errors.Is(err, ErrSKUCodeConflict), errors.Is(err, ErrStockInsufficient):
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
