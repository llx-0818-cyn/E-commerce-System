package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	orderdomain "feedsystem_video_go/internal/order"

	"gorm.io/gorm"
)

var (
	ErrPaymentValidation     = errors.New("invalid payment request")
	ErrPaymentNotFound       = errors.New("payment not found")
	ErrPaymentAmountMismatch = errors.New("payment amount does not match order")
	ErrPaymentOrderState     = errors.New("order is not pending payment")
)

type CreatePaymentInput struct {
	BuyerID    uint64
	OrderID    uint64
	AmountCent int64
}

type Service struct {
	repository     *Repository
	provider       PaymentProvider
	paymentNumbers PaymentNumberGenerator
	events         *domainevent.Repository
	orders         PaymentOrderTransition
	callbackFault  func(string) error
}

type PaymentOrderTransition interface {
	ApplyPaidTransition(context.Context, *gorm.DB, *domainevent.Writer, uint64, time.Time) (*orderdomain.Order, error)
}

func NewService(repository *Repository, provider PaymentProvider, orderTransitions ...PaymentOrderTransition) (*Service, error) {
	if repository == nil || repository.db == nil || provider == nil {
		return nil, errors.New("payment service dependencies are required")
	}
	if len(orderTransitions) > 1 {
		return nil, errors.New("payment service accepts at most one order transition")
	}
	events, err := domainevent.NewRepository(repository.db)
	if err != nil {
		return nil, err
	}
	service := &Service{repository: repository, provider: provider, paymentNumbers: NewPaymentNumberGenerator(), events: events}
	if len(orderTransitions) == 1 {
		service.orders = orderTransitions[0]
	}
	return service, nil
}

func (s *Service) CreatePayment(ctx context.Context, input CreatePaymentInput) (*PaymentSession, error) {
	if input.BuyerID == 0 || input.OrderID == 0 || input.AmountCent <= 0 {
		return nil, ErrPaymentValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var output *PaymentSession
	createdProviderPaymentNo := ""
	err := s.repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		orderRow, err := loadPaymentOrderForUpdate(ctx, tx, input.OrderID)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && orderRow.BuyerID != input.BuyerID) {
			return ErrPaymentNotFound
		}
		if err != nil {
			return err
		}
		if orderRow.TotalCent != input.AmountCent {
			return ErrPaymentAmountMismatch
		}
		existing, err := findOrderPaymentForUpdate(ctx, tx, orderRow.ID, s.provider.Name())
		if err == nil {
			output = &PaymentSession{Payment: *existing}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if orderRow.Status != orderdomain.StatusPendingPayment {
			return ErrPaymentOrderState
		}
		paymentNo, err := s.paymentNumbers.Generate()
		if err != nil {
			return err
		}
		providerPayment, err := s.provider.Create(ctx, CreateRequest{
			PaymentNo: paymentNo, OrderNo: orderRow.OrderNo, BuyerID: orderRow.BuyerID, AmountCent: orderRow.TotalCent,
		})
		if err != nil {
			return err
		}
		createdProviderPaymentNo = paymentNo
		record := PaymentRecord{
			PaymentNo: paymentNo, OrderID: orderRow.ID, BuyerID: orderRow.BuyerID,
			Provider: s.provider.Name(), Status: StatusPending, AmountCent: orderRow.TotalCent,
			SessionToken: providerPayment.SessionToken, CashierURL: providerPayment.CashierURL,
		}
		if err := tx.WithContext(ctx).Create(&record).Error; err != nil {
			return fmt.Errorf("create payment record: %w", err)
		}
		output = &PaymentSession{Payment: record}
		return nil
	})
	if err != nil {
		if createdProviderPaymentNo != "" {
			_, _ = s.provider.Close(ctx, createdProviderPaymentNo)
		}
		return nil, err
	}
	return output, nil
}

func (s *Service) QueryPayment(ctx context.Context, buyerID uint64, paymentNo string) (*PaymentSession, error) {
	paymentNo = strings.TrimSpace(paymentNo)
	if buyerID == 0 || paymentNo == "" {
		return nil, ErrPaymentValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value, err := s.repository.findOwnedPayment(ctx, buyerID, paymentNo)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &PaymentSession{Payment: *value}, nil
}
