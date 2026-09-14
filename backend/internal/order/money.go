package order

import (
	"errors"
	"math"
)

var (
	ErrMoneyNegative = errors.New("money cannot be negative")
	ErrMoneyOverflow = errors.New("money arithmetic overflow")
)

type Money struct {
	cent int64
}

func NewMoney(cent int64) (Money, error) {
	if cent < 0 {
		return Money{}, ErrMoneyNegative
	}
	return Money{cent: cent}, nil
}

func (m Money) Cent() int64 {
	return m.cent
}

func (m Money) Add(other Money) (Money, error) {
	if m.cent > math.MaxInt64-other.cent {
		return Money{}, ErrMoneyOverflow
	}
	return Money{cent: m.cent + other.cent}, nil
}

func (m Money) Multiply(quantity uint) (Money, error) {
	if quantity > math.MaxInt64 || (m.cent != 0 && int64(quantity) > math.MaxInt64/m.cent) {
		return Money{}, ErrMoneyOverflow
	}
	return Money{cent: m.cent * int64(quantity)}, nil
}

func SumMoney(values ...Money) (Money, error) {
	total := Money{}
	for _, value := range values {
		var err error
		total, err = total.Add(value)
		if err != nil {
			return Money{}, err
		}
	}
	return total, nil
}
