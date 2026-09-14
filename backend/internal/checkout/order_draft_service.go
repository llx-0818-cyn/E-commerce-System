package checkout

import (
	"context"
	"errors"

	"feedsystem_video_go/internal/storefront"
)

var ErrCheckoutNotReady = errors.New("checkout preview is not ready for order creation")

func (s *Service) BuildOrderDrafts(ctx context.Context, input CheckoutPreviewInput) (*OrderDraftPlan, error) {
	preview, err := s.PreviewCheckout(ctx, input)
	if err != nil {
		return nil, err
	}
	if !preview.CanSubmit {
		return nil, ErrCheckoutNotReady
	}
	address := AddressSnapshot{
		AddressID: preview.Address.ID, RecipientName: preview.Address.RecipientName,
		Phone: preview.Address.Phone, Province: preview.Address.Province, City: preview.Address.City,
		District: preview.Address.District, Detail: preview.Address.Detail, PostalCode: preview.Address.PostalCode,
	}
	plan := &OrderDraftPlan{Drafts: make([]StoreOrderDraft, 0, len(preview.Shops)), GrandTotal: preview.TotalCent}
	for _, group := range preview.Shops {
		draft := StoreOrderDraft{
			BuyerID: input.AccountID, ShopID: group.ShopID, AddressSnapshot: address,
			Items: make([]OrderDraftItem, 0, len(group.Items)), TotalCent: group.SubtotalCent,
		}
		for _, item := range group.Items {
			if !item.CanCheckout {
				return nil, ErrCheckoutNotReady
			}
			draft.Items = append(draft.Items, OrderDraftItem{
				CartItemID: item.CartItemID, ProductID: item.ProductID, ProductName: item.ProductName,
				CoverURL: item.CoverURL, SKUID: item.SKUID, SKUCode: item.SKUCode,
				Specs: cloneSpecs(item.Specs), UnitPriceCent: item.CurrentPriceCent,
				Quantity: item.Quantity, SubtotalCent: item.SubtotalCent,
			})
		}
		plan.Drafts = append(plan.Drafts, draft)
	}
	return plan, nil
}

func cloneSpecs(value storefront.SKUSpecs) storefront.SKUSpecs {
	result := make(storefront.SKUSpecs, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
