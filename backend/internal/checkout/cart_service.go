package checkout

import (
	"context"
	"errors"
	"log"

	"gorm.io/gorm"
)

var (
	ErrCartValidation     = errors.New("invalid cart request")
	ErrCartItemNotFound   = errors.New("cart item not found")
	ErrCartSKUUnavailable = errors.New("cart SKU is unavailable or quantity exceeds current stock")
)

type CartBehaviorRecorder interface {
	RecordCartItemAddedFromPost(context.Context, uint64, uint64, uint64, uint64, int64) error
}

type Service struct {
	repository *Repository
	behavior   CartBehaviorRecorder
}

func NewService(repository *Repository, behavior ...CartBehaviorRecorder) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("checkout service repository is nil")
	}
	if len(behavior) > 1 {
		return nil, errors.New("checkout service accepts at most one behavior recorder")
	}
	service := &Service{repository: repository}
	if len(behavior) == 1 {
		service.behavior = behavior[0]
	}
	return service, nil
}

func (s *Service) AddCartItem(ctx context.Context, input AddCartInput) (*CartItem, error) {
	if input.AccountID == 0 || input.SKUID == 0 || input.Quantity <= 0 || input.Quantity > 999 ||
		(input.Mode != CartQuantityMerge && input.Mode != CartQuantityReplace) {
		return nil, ErrCartValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var saved CartItem
	var productID uint64
	err := s.repository.transaction(ctx, func(tx *gorm.DB) error {
		sku, err := findPurchasableSKU(ctx, tx, input.SKUID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCartSKUUnavailable
		}
		if err != nil {
			return err
		}
		productID = sku.ProductID
		item, err := findCartItemForUpdate(ctx, tx, input.AccountID, input.SKUID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if input.Quantity > sku.AvailableStock {
				return ErrCartSKUUnavailable
			}
			item = &CartItem{AccountID: input.AccountID, SKUID: input.SKUID, Quantity: uint(input.Quantity), ObservedPriceCent: sku.PriceCent}
			if err := createCartItem(ctx, tx, item); err != nil {
				return err
			}
			saved = *item
			return nil
		}
		if err != nil {
			return err
		}
		target := input.Quantity
		if input.Mode == CartQuantityMerge {
			target = int64(item.Quantity) + input.Quantity
		}
		if target <= 0 || target > 999 || target > sku.AvailableStock {
			return ErrCartSKUUnavailable
		}
		if err := updateCartQuantity(ctx, tx, item.ID, input.AccountID, uint(target), &sku.PriceCent); err != nil {
			return err
		}
		item.Quantity = uint(target)
		item.ObservedPriceCent = sku.PriceCent
		saved = *item
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.behavior != nil {
		if err := s.behavior.RecordCartItemAddedFromPost(ctx, input.AccountID, input.SKUID, productID, input.SourcePostID, input.Quantity); err != nil {
			log.Printf("record cart item added failed: account_id=%d sku_id=%d err=%v", input.AccountID, input.SKUID, err)
		}
	}
	return &saved, nil
}

func (s *Service) UpdateCartQuantity(ctx context.Context, accountID, itemID uint64, quantity int64) (*CartItem, error) {
	if accountID == 0 || itemID == 0 || quantity <= 0 || quantity > 999 {
		return nil, ErrCartValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var saved CartItem
	err := s.repository.transaction(ctx, func(tx *gorm.DB) error {
		item, err := findOwnedCartItemForUpdate(ctx, tx, accountID, itemID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCartItemNotFound
		}
		if err != nil {
			return err
		}
		sku, err := findPurchasableSKU(ctx, tx, item.SKUID)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && quantity > sku.AvailableStock) {
			return ErrCartSKUUnavailable
		}
		if err != nil {
			return err
		}
		if err := updateCartQuantity(ctx, tx, item.ID, accountID, uint(quantity), nil); err != nil {
			return err
		}
		item.Quantity = uint(quantity)
		saved = *item
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func (s *Service) DeleteCartItem(ctx context.Context, accountID, itemID uint64) error {
	if accountID == 0 || itemID == 0 {
		return ErrCartValidation
	}
	if err := s.repository.deleteCartItem(ctx, accountID, itemID); errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrCartItemNotFound
	} else {
		return err
	}
}

func (s *Service) ListCart(ctx context.Context, accountID uint64) ([]CartItem, error) {
	if accountID == 0 {
		return nil, ErrCartValidation
	}
	return s.repository.listCart(ctx, accountID)
}
