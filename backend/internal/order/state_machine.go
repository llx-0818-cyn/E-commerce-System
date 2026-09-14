package order

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidTransition = errors.New("invalid order state transition")

func (s Status) Valid() bool {
	switch s {
	case StatusPendingPayment, StatusPaid, StatusShipped, StatusCompleted, StatusCanceled:
		return true
	default:
		return false
	}
}

func (s Status) CanTransitionTo(target Status) bool {
	switch s {
	case StatusPendingPayment:
		return target == StatusPaid || target == StatusCanceled
	case StatusPaid:
		return target == StatusShipped
	case StatusShipped:
		return target == StatusCompleted
	default:
		return false
	}
}

func (o *Order) Transition(target Status, at time.Time) error {
	if o == nil || !o.Status.CanTransitionTo(target) || at.IsZero() {
		current := Status("")
		if o != nil {
			current = o.Status
		}
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, target)
	}
	at = at.UTC()
	switch target {
	case StatusPaid:
		o.PaidAt = &at
	case StatusShipped:
		o.ShippedAt = &at
	case StatusCompleted:
		o.CompletedAt = &at
	case StatusCanceled:
		o.CanceledAt = &at
	}
	o.Status = target
	o.UpdatedAt = at
	return nil
}
