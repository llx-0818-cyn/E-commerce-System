package checkout

import (
	"context"
	"errors"
	"math"
	"sort"
)

var (
	ErrCheckoutValidation       = errors.New("invalid checkout preview")
	ErrCheckoutEmptyCart        = errors.New("checkout cart is empty")
	ErrCheckoutCartItemNotFound = errors.New("checkout cart item not found")
	ErrCheckoutAmountOverflow   = errors.New("checkout amount overflow")
)

func (s *Service) PreviewCheckout(ctx context.Context, input CheckoutPreviewInput) (*CheckoutPreview, error) {
	if input.AccountID == 0 {
		return nil, ErrCheckoutValidation
	}
	itemIDs, err := uniqueCheckoutItemIDs(input.CartItemIDs)
	if err != nil {
		return nil, err
	}
	var address *Address
	if input.AddressID == 0 {
		address, err = s.GetDefaultAddress(ctx, input.AccountID)
	} else {
		address, err = s.GetAddress(ctx, input.AccountID, input.AddressID)
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.repository.checkoutRows(ctx, input.AccountID, itemIDs)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrCheckoutEmptyCart
	}
	if len(itemIDs) > 0 && len(rows) != len(itemIDs) {
		return nil, ErrCheckoutCartItemNotFound
	}
	preview := &CheckoutPreview{Address: *address, Shops: []CheckoutShopGroup{}, CanSubmit: true}
	groups := make(map[uint64]*CheckoutShopGroup)
	for _, row := range rows {
		subtotal, err := checkedMultiply(row.CurrentPriceCent, int64(row.Quantity))
		if err != nil {
			return nil, err
		}
		item := CheckoutItem{
			CartItemID: row.CartItemID, ShopID: row.ShopID, ProductID: row.ProductID,
			ProductName: row.ProductName, CoverURL: row.CoverURL, SKUID: row.SKUID,
			SKUCode: row.SKUCode, Specs: row.Specs, Quantity: row.Quantity,
			ObservedPriceCent: row.ObservedPriceCent, CurrentPriceCent: row.CurrentPriceCent,
			SubtotalCent: subtotal, PriceChanged: row.ObservedPriceCent != row.CurrentPriceCent,
			CanCheckout: true,
		}
		switch {
		case row.ShopStatus != "open":
			item.CanCheckout, item.Issue = false, CheckoutIssueShopUnavailable
		case row.ProductStatus != "on_sale":
			item.CanCheckout, item.Issue = false, CheckoutIssueProductUnavailable
		case row.SKUStatus != "active":
			item.CanCheckout, item.Issue = false, CheckoutIssueSKUUnavailable
		case int64(row.Quantity) > row.AvailableStock:
			item.CanCheckout, item.Issue = false, CheckoutIssueStockInsufficient
		}
		group := groups[row.ShopID]
		if group == nil {
			group = &CheckoutShopGroup{ShopID: row.ShopID, ShopName: row.ShopName, Items: []CheckoutItem{}}
			groups[row.ShopID] = group
		}
		group.Items = append(group.Items, item)
		if item.CanCheckout {
			group.SubtotalCent, err = checkedAdd(group.SubtotalCent, subtotal)
			if err != nil {
				return nil, err
			}
		}
		preview.HasPriceChanges = preview.HasPriceChanges || item.PriceChanged
		preview.HasUnavailableItems = preview.HasUnavailableItems || !item.CanCheckout
	}
	shopIDs := make([]uint64, 0, len(groups))
	for shopID := range groups {
		shopIDs = append(shopIDs, shopID)
	}
	sort.Slice(shopIDs, func(i, j int) bool { return shopIDs[i] < shopIDs[j] })
	for _, shopID := range shopIDs {
		group := groups[shopID]
		preview.Shops = append(preview.Shops, *group)
		preview.TotalCent, err = checkedAdd(preview.TotalCent, group.SubtotalCent)
		if err != nil {
			return nil, err
		}
	}
	preview.CanSubmit = !preview.HasUnavailableItems && (!preview.HasPriceChanges || input.AcceptPriceChanges)
	return preview, nil
}

func uniqueCheckoutItemIDs(ids []uint64) ([]uint64, error) {
	result := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, ErrCheckoutValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrCheckoutValidation
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func checkedMultiply(left, right int64) (int64, error) {
	if left < 0 || right < 0 || (left != 0 && right > math.MaxInt64/left) {
		return 0, ErrCheckoutAmountOverflow
	}
	return left * right, nil
}

func checkedAdd(left, right int64) (int64, error) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, ErrCheckoutAmountOverflow
	}
	return left + right, nil
}
