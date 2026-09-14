package checkout

import (
	"context"
	"errors"
	"fmt"

	"feedsystem_video_go/internal/storefront"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("checkout repository database is nil")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

type purchasableSKU struct {
	SKUID          uint64
	ProductID      uint64
	PriceCent      int64
	AvailableStock int64
}

func findPurchasableSKU(ctx context.Context, tx *gorm.DB, skuID uint64) (*purchasableSKU, error) {
	var value purchasableSKU
	err := tx.WithContext(ctx).Table("product_skus AS sku").
		Select("sku.id AS sku_id, product.id AS product_id, sku.price_cent, sku.available_stock").
		Joins("JOIN products AS product ON product.id = sku.product_id").
		Joins("JOIN shops AS shop ON shop.id = product.shop_id").
		Where(`sku.id = ? AND sku.status = 'active' AND sku.available_stock > 0
AND product.status = 'on_sale' AND shop.status = 'open'`, skuID).
		Take(&value).Error
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func findCartItemForUpdate(ctx context.Context, tx *gorm.DB, accountID, skuID uint64) (*CartItem, error) {
	var item CartItem
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("account_id = ? AND sku_id = ?", accountID, skuID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func findOwnedCartItemForUpdate(ctx context.Context, tx *gorm.DB, accountID, itemID uint64) (*CartItem, error) {
	var item CartItem
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND account_id = ?", itemID, accountID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func createCartItem(ctx context.Context, tx *gorm.DB, item *CartItem) error {
	if err := tx.WithContext(ctx).Create(item).Error; err != nil {
		return fmt.Errorf("create cart item: %w", err)
	}
	return nil
}

func updateCartQuantity(ctx context.Context, tx *gorm.DB, itemID, accountID uint64, quantity uint, observedPriceCent *int64) error {
	updates := map[string]any{"quantity": quantity}
	if observedPriceCent != nil {
		updates["observed_price_cent"] = *observedPriceCent
	}
	result := tx.WithContext(ctx).Model(&CartItem{}).
		Where("id = ? AND account_id = ?", itemID, accountID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (r *Repository) listCart(ctx context.Context, accountID uint64) ([]CartItem, error) {
	var items []CartItem
	if err := r.db.WithContext(ctx).Where("account_id = ?", accountID).
		Order("updated_at DESC, id DESC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list cart items: %w", err)
	}
	if items == nil {
		items = []CartItem{}
	}
	return items, nil
}

func (r *Repository) deleteCartItem(ctx context.Context, accountID, itemID uint64) error {
	result := r.db.WithContext(ctx).Where("id = ? AND account_id = ?", itemID, accountID).Delete(&CartItem{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func lockAddressesByAccount(ctx context.Context, tx *gorm.DB, accountID uint64) ([]Address, error) {
	var addresses []Address
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("account_id = ?", accountID).Order("id ASC").Find(&addresses).Error; err != nil {
		return nil, err
	}
	return addresses, nil
}

func createAddress(ctx context.Context, tx *gorm.DB, address *Address) error {
	return tx.WithContext(ctx).Create(address).Error
}

func updateAddressProfile(ctx context.Context, tx *gorm.DB, accountID, addressID uint64, input AddressInput) error {
	result := tx.WithContext(ctx).Model(&Address{}).Where("id = ? AND account_id = ?", addressID, accountID).Updates(map[string]any{
		"recipient_name": input.RecipientName,
		"phone":          input.Phone,
		"province":       input.Province,
		"city":           input.City,
		"district":       input.District,
		"detail":         input.Detail,
		"postal_code":    input.PostalCode,
	})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func clearDefaultAddress(ctx context.Context, tx *gorm.DB, accountID uint64) error {
	return tx.WithContext(ctx).Exec(`
UPDATE addresses
SET is_default = FALSE, updated_at = CURRENT_TIMESTAMP(6)
WHERE account_id = ? AND is_default = TRUE`, accountID).Error
}

func setDefaultAddress(ctx context.Context, tx *gorm.DB, accountID, addressID uint64) error {
	result := tx.WithContext(ctx).Exec(`
UPDATE addresses
SET is_default = TRUE, updated_at = CURRENT_TIMESTAMP(6)
WHERE id = ? AND account_id = ?`, addressID, accountID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func deleteOwnedAddress(ctx context.Context, tx *gorm.DB, accountID, addressID uint64) error {
	result := tx.WithContext(ctx).Where("id = ? AND account_id = ?", addressID, accountID).Delete(&Address{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repository) listAddresses(ctx context.Context, accountID uint64) ([]Address, error) {
	var addresses []Address
	if err := r.db.WithContext(ctx).Where("account_id = ?", accountID).
		Order("is_default DESC, updated_at DESC, id DESC").Find(&addresses).Error; err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	if addresses == nil {
		addresses = []Address{}
	}
	return addresses, nil
}

func (r *Repository) findOwnedAddress(ctx context.Context, accountID, addressID uint64) (*Address, error) {
	var address Address
	if err := r.db.WithContext(ctx).Where("id = ? AND account_id = ?", addressID, accountID).First(&address).Error; err != nil {
		return nil, err
	}
	return &address, nil
}

func (r *Repository) findDefaultAddress(ctx context.Context, accountID uint64) (*Address, error) {
	var address Address
	if err := r.db.WithContext(ctx).Where("account_id = ? AND is_default = TRUE", accountID).First(&address).Error; err != nil {
		return nil, err
	}
	return &address, nil
}

type checkoutRow struct {
	CartItemID        uint64              `gorm:"column:cart_item_id"`
	AccountID         uint64              `gorm:"column:account_id"`
	Quantity          uint                `gorm:"column:quantity"`
	ObservedPriceCent int64               `gorm:"column:observed_price_cent"`
	SKUID             uint64              `gorm:"column:sku_id"`
	SKUCode           string              `gorm:"column:sku_code"`
	Specs             storefront.SKUSpecs `gorm:"column:specs"`
	CurrentPriceCent  int64               `gorm:"column:current_price_cent"`
	AvailableStock    int64               `gorm:"column:available_stock"`
	SKUStatus         string              `gorm:"column:sku_status"`
	ProductID         uint64              `gorm:"column:product_id"`
	ProductName       string              `gorm:"column:product_name"`
	CoverURL          string              `gorm:"column:cover_url"`
	ProductStatus     string              `gorm:"column:product_status"`
	ShopID            uint64              `gorm:"column:shop_id"`
	ShopName          string              `gorm:"column:shop_name"`
	ShopStatus        string              `gorm:"column:shop_status"`
}

func (r *Repository) checkoutRows(ctx context.Context, accountID uint64, cartItemIDs []uint64) ([]checkoutRow, error) {
	query := r.db.WithContext(ctx).Table("cart_items AS cart").
		Select(`cart.id AS cart_item_id, cart.account_id, cart.quantity, cart.observed_price_cent,
sku.id AS sku_id, sku.sku_code, sku.specs_json AS specs, sku.price_cent AS current_price_cent,
sku.available_stock, sku.status AS sku_status,
product.id AS product_id, product.name AS product_name, product.cover_url, product.status AS product_status,
shop.id AS shop_id, shop.name AS shop_name, shop.status AS shop_status`).
		Joins("JOIN product_skus AS sku ON sku.id = cart.sku_id").
		Joins("JOIN products AS product ON product.id = sku.product_id").
		Joins("JOIN shops AS shop ON shop.id = product.shop_id").
		Where("cart.account_id = ?", accountID)
	if len(cartItemIDs) > 0 {
		query = query.Where("cart.id IN ?", cartItemIDs)
	}
	var rows []checkoutRow
	if err := query.Order("shop.id ASC, cart.id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load checkout rows: %w", err)
	}
	return rows, nil
}
