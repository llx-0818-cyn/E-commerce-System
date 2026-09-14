package payment

import (
	"context"
	"errors"

	orderdomain "feedsystem_video_go/internal/order"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("payment repository database is nil")
	}
	return &Repository{db: db}, nil
}

type paymentOrderRow struct {
	ID        uint64             `gorm:"column:id"`
	OrderNo   string             `gorm:"column:order_no"`
	BuyerID   uint64             `gorm:"column:buyer_id"`
	Status    orderdomain.Status `gorm:"column:status"`
	TotalCent int64              `gorm:"column:total_cent"`
}

func loadPaymentOrderForUpdate(ctx context.Context, tx *gorm.DB, orderID uint64) (*paymentOrderRow, error) {
	var value paymentOrderRow
	if err := tx.WithContext(ctx).Table("orders").Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id,order_no,buyer_id,status,total_cent").Where("id = ?", orderID).Take(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func findOrderPaymentForUpdate(ctx context.Context, tx *gorm.DB, orderID uint64, provider string) (*PaymentRecord, error) {
	var value PaymentRecord
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_id = ? AND provider = ?", orderID, provider).First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *Repository) findOwnedPayment(ctx context.Context, buyerID uint64, paymentNo string) (*PaymentRecord, error) {
	var value PaymentRecord
	if err := r.db.WithContext(ctx).Where("buyer_id = ? AND payment_no = ?", buyerID, paymentNo).First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *Repository) findCallbackEvent(ctx context.Context, eventID string) (*CallbackEvent, error) {
	var value CallbackEvent
	if err := r.db.WithContext(ctx).Where("callback_event_id = ?", eventID).First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *Repository) findPaymentByID(ctx context.Context, paymentID uint64) (*PaymentRecord, error) {
	var value PaymentRecord
	if err := r.db.WithContext(ctx).First(&value, paymentID).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func findCallbackEventInTransaction(ctx context.Context, tx *gorm.DB, eventID string) (*CallbackEvent, error) {
	var value CallbackEvent
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("callback_event_id = ?", eventID).First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func loadPaymentForUpdate(ctx context.Context, tx *gorm.DB, paymentNo string) (*PaymentRecord, error) {
	var value PaymentRecord
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("payment_no = ?", paymentNo).First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}
