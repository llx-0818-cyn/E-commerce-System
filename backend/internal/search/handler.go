package search

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) (*Handler, error) {
	if service == nil || service.repository == nil {
		return nil, errors.New("search handler service is nil")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) Search(c *gin.Context) {
	limit, ok := searchIntQuery(c, "limit", 20)
	if !ok {
		return
	}
	offset, ok := searchIntQuery(c, "offset", 0)
	if !ok {
		return
	}
	categoryID, ok := searchOptionalUint64(c, "category_id")
	if !ok {
		return
	}
	minPrice, ok := searchOptionalInt64(c, "min_price_cent")
	if !ok {
		return
	}
	maxPrice, ok := searchOptionalInt64(c, "max_price_cent")
	if !ok {
		return
	}
	page, err := h.service.Search(c.Request.Context(), Query{
		Keyword: c.Query("q"), Scope: Scope(c.DefaultQuery("scope", string(ScopeContent))), Limit: limit, Offset: offset,
		CategoryID: categoryID, MinPriceCent: minPrice, MaxPriceCent: maxPrice,
		Sort: SortMode(c.DefaultQuery("sort", string(SortRelevance))),
	})
	if errors.Is(err, ErrInvalidQuery) || errors.Is(err, ErrEnhancedSearchRequired) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}
	c.JSON(http.StatusOK, page)
}

func searchOptionalUint64(c *gin.Context, name string) (*uint64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be a positive integer"})
		return nil, false
	}
	return &value, true
}

func searchOptionalInt64(c *gin.Context, name string) (*int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be an integer"})
		return nil, false
	}
	return &value, true
}

func searchIntQuery(c *gin.Context, name string, fallback int) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": name + " must be an integer"})
		return 0, false
	}
	return value, true
}
