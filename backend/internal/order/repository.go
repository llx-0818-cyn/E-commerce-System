package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedsystem_video_go/internal/storefront"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("order repository database is nil")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) findSubmissionByKey(ctx context.Context, buyerID uint64, idempotencyKey string) (*OrderSubmission, error) {
	var submission OrderSubmission
	if err := r.db.WithContext(ctx).Where("buyer_id = ? AND idempotency_key = ?", buyerID, idempotencyKey).
		First(&submission).Error; err != nil {
		return nil, err
	}
	return &submission, nil
}

func (r *Repository) loadOrderGroup(ctx context.Context, submission *OrderSubmission) (*OrderGroup, error) {
	if submission == nil || submission.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var orders []Order
	err := r.db.WithContext(ctx).
		Preload("Items", func(query *gorm.DB) *gorm.DB { return query.Order("sku_id ASC, id ASC") }).
		Where("submission_id = ?", submission.ID).Order("shop_id ASC, id ASC").Find(&orders).Error
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &OrderGroup{Submission: *submission, Orders: orders}, nil
}

func (r *Repository) listBuyerOrders(ctx context.Context, buyerID uint64, status Status, cursor *OrderCursor, limit int) ([]Order, error) {
	query := r.db.WithContext(ctx).Model(&Order{}).Where("buyer_id = ?", buyerID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	query = applyOrderCursor(query, cursor)
	var orders []Order
	if err := query.Preload("Items", func(itemQuery *gorm.DB) *gorm.DB { return itemQuery.Order("sku_id ASC, id ASC") }).
		Order("created_at DESC, id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("list buyer orders: %w", err)
	}
	if orders == nil {
		orders = []Order{}
	}
	return orders, nil
}

func (r *Repository) shopOwnerID(ctx context.Context, shopID uint64) (uint64, error) {
	var ownerID uint64
	if err := r.db.WithContext(ctx).Table("shops").Select("owner_id").Where("id = ?", shopID).Scan(&ownerID).Error; err != nil {
		return 0, err
	}
	if ownerID == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return ownerID, nil
}

func (r *Repository) listShopOrders(ctx context.Context, shopID uint64, status Status, cursor *OrderCursor, limit int) ([]Order, error) {
	query := r.db.WithContext(ctx).Model(&Order{}).Where("shop_id = ?", shopID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	query = applyOrderCursor(query, cursor)
	var orders []Order
	if err := query.Preload("Items", func(itemQuery *gorm.DB) *gorm.DB { return itemQuery.Order("sku_id ASC, id ASC") }).
		Order("created_at DESC, id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("list shop orders: %w", err)
	}
	if orders == nil {
		orders = []Order{}
	}
	return orders, nil
}

func (r *Repository) getOrderWithItems(ctx context.Context, orderID uint64) (*Order, error) {
	var value Order
	if err := r.db.WithContext(ctx).
		Preload("Items", func(query *gorm.DB) *gorm.DB { return query.Order("sku_id ASC, id ASC") }).
		First(&value, orderID).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func loadOrderForUpdate(ctx context.Context, tx *gorm.DB, orderID uint64) (*Order, error) {
	var value Order
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&value, orderID).Error; err != nil {
		return nil, err
	}
	if err := tx.WithContext(ctx).Where("order_id = ?", orderID).Order("sku_id ASC, id ASC").Find(&value.Items).Error; err != nil {
		return nil, err
	}
	if value.Items == nil {
		value.Items = []OrderItem{}
	}
	return &value, nil
}

func shopOwnerIDInTransaction(ctx context.Context, tx *gorm.DB, shopID uint64) (uint64, error) {
	var ownerID uint64
	if err := tx.WithContext(ctx).Table("shops").Select("owner_id").Where("id = ?", shopID).Scan(&ownerID).Error; err != nil {
		return 0, err
	}
	if ownerID == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return ownerID, nil
}

func updateOrderStatusCAS(ctx context.Context, tx *gorm.DB, orderID uint64, source, target Status, at time.Time) error {
	updates := map[string]any{"status": target, "updated_at": at}
	switch target {
	case StatusCanceled:
		updates["canceled_at"] = at
	case StatusShipped:
		updates["shipped_at"] = at
	case StatusCompleted:
		updates["completed_at"] = at
	case StatusPaid:
		updates["paid_at"] = at
	}
	result := tx.WithContext(ctx).Model(&Order{}).Where("id = ? AND status = ?", orderID, source).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrOrderStateConflict
	}
	return nil
}

func releaseSKUStock(ctx context.Context, tx *gorm.DB, skuID uint64, quantity uint) (uint64, error) {
	result := tx.WithContext(ctx).Model(&storefront.ProductSKU{}).
		Where("id = ? AND locked_stock >= ?", skuID, quantity).
		Updates(map[string]any{
			"available_stock": gorm.Expr("available_stock + ?", quantity),
			"locked_stock":    gorm.Expr("locked_stock - ?", quantity),
			"version":         gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("release SKU %d stock: %w", skuID, result.Error)
	}
	if result.RowsAffected != 1 {
		return 0, ErrStockInsufficient
	}
	var version uint64
	if err := tx.WithContext(ctx).Model(&storefront.ProductSKU{}).Select("version").Where("id = ?", skuID).Scan(&version).Error; err != nil {
		return 0, err
	}
	return version, nil
}

func sellLockedSKUStock(ctx context.Context, tx *gorm.DB, skuID uint64, quantity uint) (uint64, error) {
	result := tx.WithContext(ctx).Model(&storefront.ProductSKU{}).
		Where("id = ? AND locked_stock >= ?", skuID, quantity).
		Updates(map[string]any{
			"locked_stock": gorm.Expr("locked_stock - ?", quantity),
			"sold_stock":   gorm.Expr("sold_stock + ?", quantity),
			"version":      gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("sell locked SKU %d stock: %w", skuID, result.Error)
	}
	if result.RowsAffected != 1 {
		return 0, ErrStockInsufficient
	}
	var version uint64
	if err := tx.WithContext(ctx).Model(&storefront.ProductSKU{}).Select("version").Where("id = ?", skuID).Scan(&version).Error; err != nil {
		return 0, err
	}
	return version, nil
}

func applyOrderCursor(query *gorm.DB, cursor *OrderCursor) *gorm.DB {
	if cursor == nil {
		return query
	}
	return query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
}

func (r *Repository) expiredOrderIDs(ctx context.Context, at time.Time, limit int) ([]uint64, error) {
	var ids []uint64
	if err := r.db.WithContext(ctx).Model(&Order{}).Select("id").
		Where("status = ? AND expire_at <= ?", StatusPendingPayment, at).
		Order("expire_at ASC, id ASC").Limit(limit).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("scan expired order ids: %w", err)
	}
	if ids == nil {
		ids = []uint64{}
	}
	return ids, nil
}

type addressRow struct {
	ID            uint64
	RecipientName string
	Phone         string
	Province      string
	City          string
	District      string
	Detail        string
	PostalCode    string
}

func loadOwnedAddressSnapshot(ctx context.Context, tx *gorm.DB, buyerID, addressID uint64) (AddressSnapshot, error) {
	var row addressRow
	err := tx.WithContext(ctx).Table("addresses").Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id,recipient_name,phone,province,city,district,detail,postal_code").
		Where("id = ? AND account_id = ?", addressID, buyerID).Take(&row).Error
	if err != nil {
		return AddressSnapshot{}, err
	}
	return AddressSnapshot{
		AddressID: row.ID, RecipientName: row.RecipientName, Phone: row.Phone,
		Province: row.Province, City: row.City, District: row.District,
		Detail: row.Detail, PostalCode: row.PostalCode,
	}, nil
}

type catalogOrderRow struct {
	SKUID          uint64                   `gorm:"column:sku_id"`
	SKUCode        string                   `gorm:"column:sku_code"`
	Specs          storefront.SKUSpecs      `gorm:"column:specs"`
	PriceCent      int64                    `gorm:"column:price_cent"`
	AvailableStock int64                    `gorm:"column:available_stock"`
	SKUVersion     uint64                   `gorm:"column:sku_version"`
	SKUStatus      storefront.SKUStatus     `gorm:"column:sku_status"`
	ProductID      uint64                   `gorm:"column:product_id"`
	ProductName    string                   `gorm:"column:product_name"`
	CoverURL       string                   `gorm:"column:cover_url"`
	ProductStatus  storefront.ProductStatus `gorm:"column:product_status"`
	ShopID         uint64                   `gorm:"column:shop_id"`
	ShopStatus     storefront.ShopStatus    `gorm:"column:shop_status"`
}

func loadCatalogOrderRowForUpdate(ctx context.Context, tx *gorm.DB, skuID uint64) (*catalogOrderRow, error) {
	var row catalogOrderRow
	err := tx.WithContext(ctx).Table("product_skus AS sku").Clauses(clause.Locking{Strength: "UPDATE"}).
		Select(`sku.id AS sku_id, sku.sku_code, sku.specs_json AS specs, sku.price_cent,
sku.available_stock, sku.version AS sku_version, sku.status AS sku_status,
product.id AS product_id, product.name AS product_name, product.cover_url, product.status AS product_status,
shop.id AS shop_id, shop.status AS shop_status`).
		Joins("JOIN products AS product ON product.id = sku.product_id").
		Joins("JOIN shops AS shop ON shop.id = product.shop_id").
		Where("sku.id = ?", skuID).Take(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func lockSKUStock(ctx context.Context, tx *gorm.DB, row *catalogOrderRow, quantity uint) (uint64, error) {
	result := tx.WithContext(ctx).Model(&storefront.ProductSKU{}).
		Where("id = ? AND version = ? AND status = ? AND available_stock >= ?", row.SKUID, row.SKUVersion, storefront.SKUStatusActive, quantity).
		Updates(map[string]any{
			"available_stock": gorm.Expr("available_stock - ?", quantity),
			"locked_stock":    gorm.Expr("locked_stock + ?", quantity),
			"version":         gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("lock SKU %d stock: %w", row.SKUID, result.Error)
	}
	if result.RowsAffected != 1 {
		return 0, ErrStockInsufficient
	}
	return row.SKUVersion + 1, nil
}
