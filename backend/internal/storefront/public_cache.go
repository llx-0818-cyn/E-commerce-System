package storefront

import (
	"context"
	"errors"
	"time"

	"feedsystem_video_go/internal/cacheinvalidation"
	"feedsystem_video_go/internal/entitycache"
	rediscache "feedsystem_video_go/internal/middleware/redis"
)

type cachedProductDetail struct {
	Product Product        `json:"product"`
	Images  []ProductImage `json:"images"`
	SKUIDs  []uint64       `json:"sku_ids"`
}

type cachedSKUSummary struct {
	ID                uint64    `json:"id"`
	ProductID         uint64    `json:"product_id"`
	SKUCode           string    `json:"sku_code"`
	Specs             SKUSpecs  `json:"specs"`
	PriceCent         int64     `json:"price_cent"`
	OriginalPriceCent *int64    `json:"original_price_cent,omitempty"`
	Status            SKUStatus `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type cachedSKUInventory struct {
	SKUID          uint64    `gorm:"column:sku_id" json:"sku_id"`
	AvailableStock int64     `json:"available_stock"`
	LockedStock    int64     `json:"locked_stock"`
	SoldStock      int64     `json:"sold_stock"`
	Version        uint64    `json:"version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PublicEntityCache struct {
	redis       *rediscache.Client
	shops       *entitycache.Reader[PublicShop]
	products    *entitycache.Reader[cachedProductDetail]
	skus        *entitycache.Reader[cachedSKUSummary]
	inventories *entitycache.Reader[cachedSKUInventory]
}

func NewPublicEntityCache(repository *Repository, cache *rediscache.Client) (*PublicEntityCache, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("storefront public entity cache repository is nil")
	}
	shops, err := entitycache.NewReader(cache, func(id uint64) string {
		return entitycache.ShopSummaryKey(cache, id)
	}, repository.loadPublicShopCache, entitycache.Options{L1TTL: 5 * time.Second, L2TTL: 30 * time.Minute})
	if err != nil {
		return nil, err
	}
	products, err := entitycache.NewReader(cache, func(id uint64) string {
		return entitycache.ProductDetailKey(cache, id)
	}, repository.loadPublicProductCache, entitycache.Options{L1TTL: 5 * time.Second, L2TTL: time.Hour})
	if err != nil {
		return nil, err
	}
	skus, err := entitycache.NewReader(cache, func(id uint64) string {
		return entitycache.ProductSKUKey(cache, id)
	}, repository.loadPublicSKUCache, entitycache.Options{L1TTL: 3 * time.Second, L2TTL: 10 * time.Minute})
	if err != nil {
		return nil, err
	}
	inventories, err := entitycache.NewReader(cache, func(id uint64) string {
		return entitycache.ProductInventoryKey(cache, id)
	}, repository.loadSKUInventoryCache, entitycache.Options{L1TTL: 500 * time.Millisecond, L2TTL: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	return &PublicEntityCache{redis: cache, shops: shops, products: products, skus: skus, inventories: inventories}, nil
}

func (c *PublicEntityCache) getShop(ctx context.Context, id uint64) (*PublicShop, bool, error) {
	value, found, err := c.shops.Get(ctx, id)
	if err != nil || !found {
		return nil, found, err
	}
	return &value, true, nil
}

func (c *PublicEntityCache) getProduct(ctx context.Context, id uint64) (*cachedProductDetail, bool, error) {
	value, found, err := c.products.Get(ctx, id)
	if err != nil || !found {
		return nil, found, err
	}
	return &value, true, nil
}

func (c *PublicEntityCache) getSKUs(ctx context.Context, ids []uint64) ([]ProductSKU, error) {
	summaries, err := c.skus.GetMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	inventoryIDs := make([]uint64, len(summaries))
	for index := range summaries {
		inventoryIDs[index] = summaries[index].ID
	}
	inventories, err := c.inventories.GetMany(ctx, inventoryIDs)
	if err != nil {
		return nil, err
	}
	inventoryBySKU := make(map[uint64]cachedSKUInventory, len(inventories))
	for _, inventory := range inventories {
		inventoryBySKU[inventory.SKUID] = inventory
	}
	result := make([]ProductSKU, 0, len(summaries))
	for _, summary := range summaries {
		inventory := inventoryBySKU[summary.ID]
		result = append(result, ProductSKU{
			ID: summary.ID, ProductID: summary.ProductID, SKUCode: summary.SKUCode,
			Specs: summary.Specs, PriceCent: summary.PriceCent, OriginalPriceCent: summary.OriginalPriceCent,
			AvailableStock: inventory.AvailableStock, LockedStock: inventory.LockedStock,
			SoldStock: inventory.SoldStock, Version: inventory.Version, Status: summary.Status,
			CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt,
		})
	}
	return result, nil
}

func (c *PublicEntityCache) Invalidate(ctx context.Context, targets []cacheinvalidation.Target) error {
	if c == nil {
		return nil
	}
	shops := make([]uint64, 0, len(targets))
	products := make([]uint64, 0, len(targets))
	skus := make([]uint64, 0, len(targets))
	inventories := make([]uint64, 0, len(targets))
	priceRanges := make([]uint64, 0, len(targets))
	for _, target := range targets {
		switch target.Entity {
		case cacheinvalidation.ShopSummary:
			shops = append(shops, target.ID)
		case cacheinvalidation.ProductDetail:
			products = append(products, target.ID)
		case cacheinvalidation.ProductSKU:
			skus = append(skus, target.ID)
		case cacheinvalidation.ProductInventory:
			inventories = append(inventories, target.ID)
		case cacheinvalidation.ProductPriceRange:
			priceRanges = append(priceRanges, target.ID)
		}
	}
	var result error
	result = errors.Join(result, c.shops.Delete(ctx, shops...))
	result = errors.Join(result, c.products.Delete(ctx, products...))
	result = errors.Join(result, c.skus.Delete(ctx, skus...))
	result = errors.Join(result, c.inventories.Delete(ctx, inventories...))
	if c.redis != nil {
		for _, productID := range priceRanges {
			result = errors.Join(result, c.redis.Del(ctx, entitycache.ProductPriceRangeKey(c.redis, productID)))
		}
	}
	return result
}

func (r *Repository) loadPublicShopCache(ctx context.Context, ids []uint64) (map[uint64]PublicShop, error) {
	result := make(map[uint64]PublicShop, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var shops []Shop
	if err := r.db.WithContext(ctx).Where("id IN ? AND status <> ?", ids, ShopStatusDisabled).Find(&shops).Error; err != nil {
		return nil, err
	}
	type productCountRow struct {
		ShopID uint64 `gorm:"column:shop_id"`
		Count  int64  `gorm:"column:product_count"`
	}
	var counts []productCountRow
	if err := r.db.WithContext(ctx).Model(&Product{}).
		Select("shop_id,COUNT(*) AS product_count").
		Where("shop_id IN ? AND status = ?", ids, ProductStatusOnSale).
		Group("shop_id").Find(&counts).Error; err != nil {
		return nil, err
	}
	countByShop := make(map[uint64]int64, len(counts))
	for _, row := range counts {
		countByShop[row.ShopID] = row.Count
	}
	for _, shop := range shops {
		result[shop.ID] = PublicShop{
			Shop: shop, ProductCount: countByShop[shop.ID], AcceptingOrders: shop.Status == ShopStatusOpen,
		}
	}
	return result, nil
}

func (r *Repository) loadPublicProductCache(ctx context.Context, ids []uint64) (map[uint64]cachedProductDetail, error) {
	result := make(map[uint64]cachedProductDetail, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var products []Product
	if err := r.db.WithContext(ctx).Where("id IN ? AND status = ?", ids, ProductStatusOnSale).Find(&products).Error; err != nil {
		return nil, err
	}
	productIDs := make([]uint64, 0, len(products))
	for _, product := range products {
		productIDs = append(productIDs, product.ID)
		result[product.ID] = cachedProductDetail{Product: product, Images: []ProductImage{}, SKUIDs: []uint64{}}
	}
	if len(productIDs) == 0 {
		return result, nil
	}
	var images []ProductImage
	if err := r.db.WithContext(ctx).Where("product_id IN ?", productIDs).
		Order("product_id ASC,sort_order ASC,id ASC").Find(&images).Error; err != nil {
		return nil, err
	}
	for _, image := range images {
		value := result[image.ProductID]
		value.Images = append(value.Images, image)
		result[image.ProductID] = value
	}
	var skuRows []struct {
		ID        uint64    `gorm:"column:id"`
		ProductID uint64    `gorm:"column:product_id"`
		Status    SKUStatus `gorm:"column:status"`
		PriceCent int64     `gorm:"column:price_cent"`
	}
	if err := r.db.WithContext(ctx).Model(&ProductSKU{}).
		Select("id,product_id,status,price_cent").
		Where("product_id IN ? AND status <> ?", productIDs, SKUStatusDisabled).
		Order("product_id ASC,status ASC,price_cent ASC,id ASC").Find(&skuRows).Error; err != nil {
		return nil, err
	}
	for _, row := range skuRows {
		value := result[row.ProductID]
		value.SKUIDs = append(value.SKUIDs, row.ID)
		result[row.ProductID] = value
	}
	return result, nil
}

func (r *Repository) loadPublicSKUCache(ctx context.Context, ids []uint64) (map[uint64]cachedSKUSummary, error) {
	result := make(map[uint64]cachedSKUSummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var skus []ProductSKU
	if err := r.db.WithContext(ctx).Where("id IN ? AND status <> ?", ids, SKUStatusDisabled).Find(&skus).Error; err != nil {
		return nil, err
	}
	for _, sku := range skus {
		result[sku.ID] = cachedSKUSummary{
			ID: sku.ID, ProductID: sku.ProductID, SKUCode: sku.SKUCode, Specs: sku.Specs,
			PriceCent: sku.PriceCent, OriginalPriceCent: sku.OriginalPriceCent,
			Status: sku.Status, CreatedAt: sku.CreatedAt, UpdatedAt: sku.UpdatedAt,
		}
	}
	return result, nil
}

func (r *Repository) loadSKUInventoryCache(ctx context.Context, ids []uint64) (map[uint64]cachedSKUInventory, error) {
	result := make(map[uint64]cachedSKUInventory, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []cachedSKUInventory
	if err := r.db.WithContext(ctx).Model(&ProductSKU{}).
		Select("id AS sku_id,available_stock,locked_stock,sold_stock,version,updated_at").
		Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.SKUID] = row
	}
	return result, nil
}
