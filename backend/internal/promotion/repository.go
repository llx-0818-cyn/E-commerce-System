package promotion

import (
	"context"
	"errors"

	"feedsystem_video_go/internal/storefront"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("promotion repository database is nil")
	}
	return &Repository{db: db}, nil
}

type campaignSKURow struct {
	SKUID          uint64                   `gorm:"column:sku_id"`
	PriceCent      int64                    `gorm:"column:price_cent"`
	AvailableStock int64                    `gorm:"column:available_stock"`
	SKUStatus      storefront.SKUStatus     `gorm:"column:sku_status"`
	ProductStatus  storefront.ProductStatus `gorm:"column:product_status"`
	ShopStatus     storefront.ShopStatus    `gorm:"column:shop_status"`
	OwnerID        uint64                   `gorm:"column:owner_id"`
}

func loadOwnedCampaignSKU(ctx context.Context, tx *gorm.DB, ownerID, skuID uint64) (*campaignSKURow, error) {
	var row campaignSKURow
	err := tx.WithContext(ctx).Table("product_skus AS sku").Clauses(clause.Locking{Strength: "UPDATE"}).
		Select(`sku.id AS sku_id,sku.price_cent,sku.available_stock,sku.status AS sku_status,
product.status AS product_status,shop.status AS shop_status,shop.owner_id`).
		Joins("JOIN products AS product ON product.id=sku.product_id").
		Joins("JOIN shops AS shop ON shop.id=product.shop_id").
		Where("sku.id=? AND shop.owner_id=?", skuID, ownerID).Take(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) getCampaign(ctx context.Context, campaignID uint64) (*FlashSaleCampaign, error) {
	var value FlashSaleCampaign
	if err := r.db.WithContext(ctx).First(&value, campaignID).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func loadOwnedCampaignForUpdate(ctx context.Context, tx *gorm.DB, ownerID, campaignID uint64) (*FlashSaleCampaign, error) {
	var value FlashSaleCampaign
	err := tx.WithContext(ctx).Table("flash_sale_campaigns AS campaign").Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("campaign.*").
		Joins("JOIN product_skus AS sku ON sku.id=campaign.sku_id").
		Joins("JOIN products AS product ON product.id=sku.product_id").
		Joins("JOIN shops AS shop ON shop.id=product.shop_id").
		Where("campaign.id=? AND shop.owner_id=?", campaignID, ownerID).Take(&value).Error
	if err != nil {
		return nil, err
	}
	return &value, nil
}
