package payment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	orderdomain "feedsystem_video_go/internal/order"

	"gorm.io/gorm"
)

func (s *Service) ProcessCallback(ctx context.Context, payload CallbackPayload) (*CallbackOutcome, error) {
	if s == nil || s.events == nil || s.orders == nil {
		return nil, errors.New("payment callback service is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	verified, err := s.provider.VerifyCallback(ctx, payload)
	if err != nil {
		s.recordRejectedCallback(payload, "signature_invalid", err.Error())
		return nil, err
	}
	if existing, err := s.repository.findCallbackEvent(ctx, verified.CallbackEventID); err == nil {
		return s.duplicateCallbackOutcome(ctx, existing)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	payloadJSON := callbackJSON(payload)
	var outcome *CallbackOutcome
	var semanticErr error
	err = s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		paymentRecord, err := loadPaymentForUpdate(ctx, tx, verified.PaymentNo)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			anomaly := newPaymentAnomaly(nil, nil, verified.PaymentNo, verified.CallbackEventID, "payment_not_found", nil, &verified.AmountCent, "payment record not found", payloadJSON)
			if err := tx.WithContext(ctx).Create(anomaly).Error; err != nil {
				return err
			}
			outcome = &CallbackOutcome{Rejected: true}
			semanticErr = ErrPaymentNotFound
			return nil
		}
		if err != nil {
			return err
		}
		if existing, err := findCallbackEventInTransaction(ctx, tx, verified.CallbackEventID); err == nil {
			outcome = callbackOutcomeFromEvent(existing, paymentRecord)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		orderRow, err := loadPaymentOrderForUpdate(ctx, tx, paymentRecord.OrderID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		callback := &CallbackEvent{
			CallbackEventID: verified.CallbackEventID, PaymentID: paymentRecord.ID,
			Provider: s.provider.Name(), ProviderTradeNo: verified.ProviderTradeNo,
			Result: string(verified.Result), AmountCent: verified.AmountCent,
			PayloadJSON: payloadJSON, ProcessStatus: "received", ReceivedAt: now,
		}
		if err := tx.WithContext(ctx).Create(callback).Error; err != nil {
			return err
		}
		if err := s.injectCallbackFault("after_callback"); err != nil {
			return err
		}

		if verified.OrderNo != orderRow.OrderNo || verified.AmountCent != paymentRecord.AmountCent || verified.AmountCent != orderRow.TotalCent {
			detail := "callback order or amount does not match payment record"
			anomaly := newPaymentAnomaly(&paymentRecord.ID, &paymentRecord.OrderID, paymentRecord.PaymentNo, verified.CallbackEventID, "amount_mismatch", &paymentRecord.AmountCent, &verified.AmountCent, detail, payloadJSON)
			if err := tx.WithContext(ctx).Create(anomaly).Error; err != nil {
				return err
			}
			if err := rejectCallback(ctx, tx, callback.ID, detail, now); err != nil {
				return err
			}
			outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: paymentRecord.Status, Rejected: true}
			semanticErr = ErrPaymentAmountMismatch
			return nil
		}

		if paymentRecord.Status == StatusSucceeded {
			if paymentRecord.ProviderTradeNo != nil && *paymentRecord.ProviderTradeNo != verified.ProviderTradeNo {
				detail := "succeeded payment received a different provider trade number"
				anomaly := newPaymentAnomaly(&paymentRecord.ID, &paymentRecord.OrderID, paymentRecord.PaymentNo, verified.CallbackEventID, "provider_trade_conflict", nil, nil, detail, payloadJSON)
				if err := tx.WithContext(ctx).Create(anomaly).Error; err != nil {
					return err
				}
				if err := rejectCallback(ctx, tx, callback.ID, detail, now); err != nil {
					return err
				}
				outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: paymentRecord.Status, Rejected: true}
				semanticErr = ErrProviderConflict
				return nil
			}
			if err := completeCallback(ctx, tx, callback.ID, "duplicate", now); err != nil {
				return err
			}
			outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: paymentRecord.Status, Duplicate: true}
			return nil
		}

		if verified.Result == CallbackFailed {
			updates := map[string]any{
				"status": StatusFailed, "provider_trade_no": verified.ProviderTradeNo,
				"failure_code": "mock_failed", "failure_reason": "mock payment failed",
				"failed_at": verified.OccurredAt.UTC(),
			}
			if err := tx.WithContext(ctx).Model(&PaymentRecord{}).Where("id = ?", paymentRecord.ID).Updates(updates).Error; err != nil {
				return err
			}
			if err := s.injectCallbackFault("after_payment"); err != nil {
				return err
			}
			if err := completeCallback(ctx, tx, callback.ID, "applied", now); err != nil {
				return err
			}
			outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: StatusFailed, Applied: true}
			return s.injectCallbackFault("before_commit")
		}

		if paymentRecord.Status == StatusClosed || orderRow.Status == orderdomain.StatusCanceled {
			detail := "successful callback arrived after order cancellation"
			anomaly := newPaymentAnomaly(&paymentRecord.ID, &paymentRecord.OrderID, paymentRecord.PaymentNo, verified.CallbackEventID, "order_canceled", nil, nil, detail, payloadJSON)
			if err := tx.WithContext(ctx).Create(anomaly).Error; err != nil {
				return err
			}
			if err := rejectCallback(ctx, tx, callback.ID, detail, now); err != nil {
				return err
			}
			if paymentRecord.Status != StatusClosed {
				if err := tx.WithContext(ctx).Model(&PaymentRecord{}).Where("id = ?", paymentRecord.ID).Updates(map[string]any{
					"status": StatusClosed, "provider_trade_no": verified.ProviderTradeNo,
					"closed_at": now, "failure_code": "order_canceled", "failure_reason": detail,
				}).Error; err != nil {
					return err
				}
			}
			outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: StatusClosed, Rejected: true}
			semanticErr = orderdomain.ErrOrderStateConflict
			return nil
		}
		if orderRow.Status != orderdomain.StatusPendingPayment && orderRow.Status != orderdomain.StatusPaid {
			detail := "successful callback conflicts with current order state"
			anomaly := newPaymentAnomaly(&paymentRecord.ID, &paymentRecord.OrderID, paymentRecord.PaymentNo, verified.CallbackEventID, "order_state_conflict", nil, nil, detail, payloadJSON)
			if err := tx.WithContext(ctx).Create(anomaly).Error; err != nil {
				return err
			}
			if err := rejectCallback(ctx, tx, callback.ID, detail, now); err != nil {
				return err
			}
			outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: paymentRecord.Status, Rejected: true}
			semanticErr = orderdomain.ErrOrderStateConflict
			return nil
		}

		if err := tx.WithContext(ctx).Model(&PaymentRecord{}).Where("id = ?", paymentRecord.ID).Updates(map[string]any{
			"status": StatusSucceeded, "provider_trade_no": verified.ProviderTradeNo,
			"failure_code": nil, "failure_reason": nil, "succeeded_at": verified.OccurredAt.UTC(),
		}).Error; err != nil {
			return err
		}
		if err := s.injectCallbackFault("after_payment"); err != nil {
			return err
		}
		if _, err := s.orders.ApplyPaidTransition(ctx, tx, outbox, paymentRecord.OrderID, verified.OccurredAt); err != nil {
			return err
		}
		if err := s.injectCallbackFault("after_order"); err != nil {
			return err
		}
		if err := completeCallback(ctx, tx, callback.ID, "applied", now); err != nil {
			return err
		}
		outcome = &CallbackOutcome{PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: StatusSucceeded, Applied: true}
		return s.injectCallbackFault("before_commit")
	})
	if err != nil {
		return nil, err
	}
	return outcome, semanticErr
}

func (s *Service) duplicateCallbackOutcome(ctx context.Context, callback *CallbackEvent) (*CallbackOutcome, error) {
	paymentRecord, err := s.repository.findPaymentByID(ctx, callback.PaymentID)
	if err != nil {
		return nil, err
	}
	return callbackOutcomeFromEvent(callback, paymentRecord), nil
}

func callbackOutcomeFromEvent(callback *CallbackEvent, paymentRecord *PaymentRecord) *CallbackOutcome {
	return &CallbackOutcome{
		PaymentID: paymentRecord.ID, OrderID: paymentRecord.OrderID, Status: paymentRecord.Status,
		Applied: callback.ProcessStatus == "applied", Duplicate: true, Rejected: callback.ProcessStatus == "rejected",
	}
}

func (s *Service) injectCallbackFault(stage string) error {
	if s.callbackFault == nil {
		return nil
	}
	return s.callbackFault(stage)
}

func completeCallback(ctx context.Context, tx *gorm.DB, callbackID uint64, status string, at time.Time) error {
	return tx.WithContext(ctx).Model(&CallbackEvent{}).Where("id = ?", callbackID).
		Updates(map[string]any{"process_status": status, "processed_at": at, "failure_reason": nil}).Error
}

func rejectCallback(ctx context.Context, tx *gorm.DB, callbackID uint64, reason string, at time.Time) error {
	return tx.WithContext(ctx).Model(&CallbackEvent{}).Where("id = ?", callbackID).
		Updates(map[string]any{"process_status": "rejected", "processed_at": at, "failure_reason": reason}).Error
}

func newPaymentAnomaly(paymentID, orderID *uint64, paymentNo, callbackEventID, anomalyType string, expected, actual *int64, detail, payloadJSON string) *PaymentAnomaly {
	return &PaymentAnomaly{
		PaymentID: paymentID, OrderID: orderID, PaymentNo: paymentNo, CallbackEventID: callbackEventID,
		AnomalyType: anomalyType, ExpectedAmountCent: expected, ActualAmountCent: actual,
		Detail: detail, PayloadJSON: payloadJSON,
	}
}

func callbackJSON(payload CallbackPayload) string {
	data, _ := json.Marshal(payload)
	return string(data)
}

func (s *Service) recordRejectedCallback(payload CallbackPayload, anomalyType, detail string) {
	if s == nil || s.repository == nil || strings.TrimSpace(payload.CallbackEventID) == "" {
		return
	}
	anomaly := newPaymentAnomaly(nil, nil, strings.TrimSpace(payload.PaymentNo), strings.TrimSpace(payload.CallbackEventID), anomalyType, nil, nil, detail, callbackJSON(payload))
	_ = s.repository.db.Create(anomaly).Error
}
