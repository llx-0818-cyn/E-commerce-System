package promotion

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type FlashSaleApplication interface {
	ReserveCampaign(context.Context, uint64, uint64, string, uint) (*ReservationResult, error)
	GetReservationStatus(context.Context, uint64, string) (*ReservationResult, error)
}

type Handler struct {
	service FlashSaleApplication
}

func NewHandler(service FlashSaleApplication) (*Handler, error) {
	if service == nil {
		return nil, errors.New("promotion handler service is nil")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) Accept(c *gin.Context) {
	buyerID, ok := flashSaleActorID(c)
	if !ok {
		return
	}
	campaignID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || campaignID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive integer"})
		return
	}
	var request struct {
		IdempotencyKey string `json:"idempotency_key"`
		Quantity       uint   `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON request body"})
		return
	}
	result, err := h.service.ReserveCampaign(c.Request.Context(), campaignID, buyerID, request.IdempotencyKey, request.Quantity)
	if err != nil {
		writeFlashSaleError(c, err)
		return
	}
	switch result.Code {
	case ReservationAccepted:
		status := http.StatusAccepted
		if result.Duplicate {
			status = http.StatusOK
		}
		c.JSON(status, result)
	case ReservationNotPreheated:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": string(result.Code)})
	default:
		c.JSON(http.StatusConflict, result)
	}
}

func (h *Handler) Status(c *gin.Context) {
	buyerID, ok := flashSaleActorID(c)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(c.Param("request_id"))
	if requestID == "" || len(requestID) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "request_id is invalid"})
		return
	}
	result, err := h.service.GetReservationStatus(c.Request.Context(), buyerID, requestID)
	if err != nil {
		writeFlashSaleError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func flashSaleActorID(c *gin.Context) (uint64, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uint64(accountID), true
}

func writeFlashSaleError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, ErrCampaignValidation):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrCampaignNotFound), errors.Is(err, ErrReservationNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, ErrCampaignForbidden):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, ErrCampaignUnavailable), errors.Is(err, ErrCampaignExpired):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, ErrPreheatUnavailable):
		status, message = http.StatusServiceUnavailable, err.Error()
	}
	c.JSON(status, gin.H{"error": message})
}
