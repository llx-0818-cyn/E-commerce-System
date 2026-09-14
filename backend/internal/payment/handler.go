package payment

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"feedsystem_video_go/internal/middleware/jwt"
	orderdomain "feedsystem_video_go/internal/order"

	"github.com/gin-gonic/gin"
)

type PaymentApplication interface {
	CreatePayment(context.Context, CreatePaymentInput) (*PaymentSession, error)
	QueryPayment(context.Context, uint64, string) (*PaymentSession, error)
	ProcessCallback(context.Context, CallbackPayload) (*CallbackOutcome, error)
}

func (h *Handler) CreatePayment(c *gin.Context) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	var request struct {
		OrderID    uint64 `json:"order_id"`
		AmountCent int64  `json:"amount_cent"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON request body"})
		return
	}
	session, err := h.service.CreatePayment(c.Request.Context(), CreatePaymentInput{
		BuyerID: uint64(accountID), OrderID: request.OrderID, AmountCent: request.AmountCent,
	})
	if err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, session)
}

func (h *Handler) QueryPayment(c *gin.Context) {
	accountID, paymentNo, ok := mockPaymentActorAndNumber(c)
	if !ok {
		return
	}
	session, err := h.service.QueryPayment(c.Request.Context(), accountID, paymentNo)
	if err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

type MockSimulator interface {
	Simulate(context.Context, string, CallbackResult) (CallbackPayload, error)
	RepeatCallback(context.Context, string) (CallbackPayload, error)
}

type Handler struct {
	service           PaymentApplication
	mock              MockSimulator
	mockRoutesEnabled bool
}

func NewHandler(service PaymentApplication, mock MockSimulator, environment string, routesEnabled bool) (*Handler, error) {
	environment = strings.ToLower(strings.TrimSpace(environment))
	if service == nil || mock == nil {
		return nil, errors.New("payment handler dependencies are required")
	}
	if routesEnabled && environment != "development" && environment != "test" {
		return nil, errors.New("mock payment routes are only allowed in development or test")
	}
	return &Handler{service: service, mock: mock, mockRoutesEnabled: routesEnabled}, nil
}

func (h *Handler) RegisterMockRoutes(group *gin.RouterGroup) {
	if h == nil || group == nil || !h.mockRoutesEnabled {
		return
	}
	group.POST("/mock-payments/:payment_no/succeed", h.SimulateSuccess)
	group.POST("/mock-payments/:payment_no/fail", h.SimulateFailure)
	group.POST("/mock-payments/:payment_no/repeat", h.RepeatCallback)
}

func (h *Handler) SimulateSuccess(c *gin.Context) {
	h.simulate(c, CallbackSucceeded)
}

func (h *Handler) SimulateFailure(c *gin.Context) {
	h.simulate(c, CallbackFailed)
}

func (h *Handler) RepeatCallback(c *gin.Context) {
	accountID, paymentNo, ok := mockPaymentActorAndNumber(c)
	if !ok {
		return
	}
	if _, err := h.service.QueryPayment(c.Request.Context(), accountID, paymentNo); err != nil {
		writePaymentError(c, err)
		return
	}
	payload, err := h.mock.RepeatCallback(c.Request.Context(), paymentNo)
	if err != nil {
		writePaymentError(c, err)
		return
	}
	h.processSimulation(c, payload)
}

func (h *Handler) simulate(c *gin.Context, result CallbackResult) {
	accountID, paymentNo, ok := mockPaymentActorAndNumber(c)
	if !ok {
		return
	}
	if _, err := h.service.QueryPayment(c.Request.Context(), accountID, paymentNo); err != nil {
		writePaymentError(c, err)
		return
	}
	payload, err := h.mock.Simulate(c.Request.Context(), paymentNo, result)
	if err != nil {
		writePaymentError(c, err)
		return
	}
	h.processSimulation(c, payload)
}

func (h *Handler) processSimulation(c *gin.Context, payload CallbackPayload) {
	outcome, err := h.service.ProcessCallback(c.Request.Context(), payload)
	if err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"callback": payload, "outcome": outcome})
}

func mockPaymentActorAndNumber(c *gin.Context) (uint64, string, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, "", false
	}
	paymentNo := strings.TrimSpace(c.Param("payment_no"))
	if paymentNo == "" || len(paymentNo) > 40 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payment_no is invalid"})
		return 0, "", false
	}
	return uint64(accountID), paymentNo, true
}

func writePaymentError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, ErrPaymentValidation), errors.Is(err, ErrProviderValidation), errors.Is(err, ErrInvalidCallbackSignature):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrPaymentNotFound), errors.Is(err, ErrProviderNotFound), errors.Is(err, orderdomain.ErrOrderNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, ErrPaymentAmountMismatch), errors.Is(err, ErrPaymentOrderState), errors.Is(err, ErrProviderConflict),
		errors.Is(err, orderdomain.ErrOrderStateConflict):
		status, message = http.StatusConflict, err.Error()
	}
	c.JSON(status, gin.H{"error": message})
}
