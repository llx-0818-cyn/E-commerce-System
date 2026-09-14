package storefront

import (
	"context"
	"errors"
	"fmt"

	"feedsystem_video_go/internal/content"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("storefront repository database is nil")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) createShop(ctx context.Context, shop *Shop) error {
	return r.db.WithContext(ctx).Create(shop).Error
}

func (r *Repository) findShopByID(ctx context.Context, shopID uint64) (*Shop, error) {
	var shop Shop
	if err := r.db.WithContext(ctx).Where("id = ?", shopID).First(&shop).Error; err != nil {
		return nil, err
	}
	return &shop, nil
}

func (r *Repository) findShopByOwner(ctx context.Context, ownerID uint64) (*Shop, error) {
	var shop Shop
	if err := r.db.WithContext(ctx).Where("owner_id = ?", ownerID).First(&shop).Error; err != nil {
		return nil, err
	}
	return &shop, nil
}

func (r *Repository) updateShopProfile(ctx context.Context, shopID, ownerID uint64, input ShopProfileInput) error {
	result := r.db.WithContext(ctx).Model(&Shop{}).
		Where("id = ? AND owner_id = ?", shopID, ownerID).
		Updates(map[string]any{
			"name":        input.Name,
			"description": input.Description,
			"avatar_url":  input.AvatarURL,
			"banner_url":  input.BannerURL,
		})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (r *Repository) updateShopStatus(ctx context.Context, shopID, ownerID uint64, status ShopStatus) error {
	result := r.db.WithContext(ctx).Model(&Shop{}).
		Where("id = ? AND owner_id = ?", shopID, ownerID).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (r *Repository) countPublicProducts(ctx context.Context, shopID uint64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&Product{}).
		Where("shop_id = ? AND status = ?", shopID, ProductStatusOnSale).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count public shop products: %w", err)
	}
	return count, nil
}

func (r *Repository) sellerDashboard(ctx context.Context, shopID uint64) (*SellerDashboard, error) {
	var dashboard SellerDashboard
	err := r.db.WithContext(ctx).Raw(`
SELECT ? AS shop_id,
       (SELECT COUNT(*) FROM products WHERE shop_id=? AND status<>'disabled') AS product_count,
       (SELECT COUNT(*) FROM product_skus AS sku JOIN products AS product ON product.id=sku.product_id WHERE product.shop_id=? AND sku.status<>'disabled') AS sku_count,
       COALESCE((SELECT SUM(sku.available_stock) FROM product_skus AS sku JOIN products AS product ON product.id=sku.product_id WHERE product.shop_id=? AND sku.status<>'disabled'),0) AS available_stock,
       COALESCE((SELECT SUM(sku.locked_stock) FROM product_skus AS sku JOIN products AS product ON product.id=sku.product_id WHERE product.shop_id=? AND sku.status<>'disabled'),0) AS locked_stock,
       COALESCE((SELECT SUM(sku.sold_stock) FROM product_skus AS sku JOIN products AS product ON product.id=sku.product_id WHERE product.shop_id=? AND sku.status<>'disabled'),0) AS sold_stock,
       (SELECT COUNT(*) FROM orders WHERE shop_id=? AND status='paid') AS pending_shipment_orders,
       (SELECT COUNT(*) FROM orders WHERE shop_id=? AND status='completed') AS completed_orders`,
		shopID, shopID, shopID, shopID, shopID, shopID, shopID, shopID).Scan(&dashboard).Error
	if err != nil {
		return nil, fmt.Errorf("load seller dashboard: %w", err)
	}
	return &dashboard, nil
}

func (r *Repository) listPublicProducts(ctx context.Context, shopID uint64, limit int, cursor *ProductCursor) ([]Product, error) {
	query := r.db.WithContext(ctx).Model(&Product{}).
		Where("shop_id = ? AND status = ?", shopID, ProductStatusOnSale)
	if cursor != nil {
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var products []Product
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&products).Error; err != nil {
		return nil, fmt.Errorf("list public shop products: %w", err)
	}
	if products == nil {
		products = []Product{}
	}
	return products, nil
}

func (r *Repository) listSellerProducts(ctx context.Context, shopID uint64, limit int) ([]Product, error) {
	var products []Product
	if err := r.db.WithContext(ctx).Where("shop_id=?", shopID).Order("created_at DESC,id DESC").Limit(limit).Find(&products).Error; err != nil {
		return nil, fmt.Errorf("list seller products: %w", err)
	}
	if products == nil {
		products = []Product{}
	}
	return products, nil
}

func (r *Repository) sellerProductDetail(ctx context.Context, productID uint64) (*SellerProductDetail, error) {
	product, err := r.findProductByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	images, err := r.listProductImages(ctx, productID)
	if err != nil {
		return nil, err
	}
	var skus []ProductSKU
	if err := r.db.WithContext(ctx).Where("product_id=?", productID).Order("created_at,id").Find(&skus).Error; err != nil {
		return nil, err
	}
	if skus == nil {
		skus = []ProductSKU{}
	}
	return &SellerProductDetail{Product: *product, Images: images, SKUs: skus}, nil
}

func (r *Repository) findPublicProduct(ctx context.Context, productID uint64) (*Product, error) {
	var product Product
	if err := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", productID, ProductStatusOnSale).
		First(&product).Error; err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *Repository) productMinimumSellablePrices(ctx context.Context, productIDs []uint64) (map[uint64]int64, error) {
	prices := make(map[uint64]int64, len(productIDs))
	if len(productIDs) == 0 {
		return prices, nil
	}
	var rows []struct {
		ProductID    uint64
		MinPriceCent int64
	}
	if err := r.db.WithContext(ctx).Model(&ProductSKU{}).
		Select("product_id, MIN(price_cent) AS min_price_cent").
		Where("product_id IN ? AND status = ? AND available_stock > 0", productIDs, SKUStatusActive).
		Group("product_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query public product minimum prices: %w", err)
	}
	for _, row := range rows {
		prices[row.ProductID] = row.MinPriceCent
	}
	return prices, nil
}

func (r *Repository) listProductImages(ctx context.Context, productID uint64) ([]ProductImage, error) {
	var images []ProductImage
	if err := r.db.WithContext(ctx).Where("product_id = ?", productID).
		Order("sort_order ASC, id ASC").Find(&images).Error; err != nil {
		return nil, fmt.Errorf("list product images: %w", err)
	}
	if images == nil {
		images = []ProductImage{}
	}
	return images, nil
}

func (r *Repository) listPublicProductSKUs(ctx context.Context, productID uint64) ([]ProductSKU, error) {
	var skus []ProductSKU
	if err := r.db.WithContext(ctx).
		Where("product_id = ? AND status <> ?", productID, SKUStatusDisabled).
		Order("status ASC, price_cent ASC, id ASC").Find(&skus).Error; err != nil {
		return nil, fmt.Errorf("list public product SKUs: %w", err)
	}
	if skus == nil {
		skus = []ProductSKU{}
	}
	return skus, nil
}

func (r *Repository) categoryIsActive(ctx context.Context, categoryID uint64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&Category{}).
		Where("id = ? AND status = ?", categoryID, CategoryStatusActive).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

func (r *Repository) createProductWithImages(ctx context.Context, product *Product, inputs []ProductImageInput) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(product).Error; err != nil {
			return fmt.Errorf("create product: %w", err)
		}
		images := productImagesFromInputs(product.ID, inputs)
		if len(images) > 0 {
			if err := tx.Create(&images).Error; err != nil {
				return fmt.Errorf("create product images: %w", err)
			}
		}
		return nil
	})
}

func (r *Repository) findProductByID(ctx context.Context, productID uint64) (*Product, error) {
	var product Product
	if err := r.db.WithContext(ctx).Where("id = ?", productID).First(&product).Error; err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *Repository) updateProductProfile(ctx context.Context, productID uint64, input ProductProfileInput) error {
	return r.db.WithContext(ctx).Model(&Product{}).Where("id = ?", productID).Updates(map[string]any{
		"category_id": input.CategoryID,
		"name":        input.Name,
		"subtitle":    input.Subtitle,
		"description": input.Description,
		"cover_url":   input.CoverURL,
	}).Error
}

func (r *Repository) replaceProductImages(ctx context.Context, productID uint64, inputs []ProductImageInput) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("product_id = ?", productID).Delete(&ProductImage{}).Error; err != nil {
			return fmt.Errorf("delete old product images: %w", err)
		}
		images := productImagesFromInputs(productID, inputs)
		if len(images) > 0 {
			if err := tx.Create(&images).Error; err != nil {
				return fmt.Errorf("create replacement product images: %w", err)
			}
		}
		return nil
	})
}

func productImagesFromInputs(productID uint64, inputs []ProductImageInput) []ProductImage {
	images := make([]ProductImage, 0, len(inputs))
	for index, input := range inputs {
		images = append(images, ProductImage{
			ProductID: productID, StorageKey: input.StorageKey, ImageURL: input.ImageURL,
			MIMEType: input.MIMEType, ByteSize: input.ByteSize, Width: input.Width,
			Height: input.Height, SortOrder: uint(index),
		})
	}
	return images
}

func (r *Repository) createSKU(ctx context.Context, sku *ProductSKU) error {
	return r.db.WithContext(ctx).Create(sku).Error
}

func (r *Repository) findSKUByID(ctx context.Context, skuID uint64) (*ProductSKU, error) {
	var sku ProductSKU
	if err := r.db.WithContext(ctx).Where("id = ?", skuID).First(&sku).Error; err != nil {
		return nil, err
	}
	return &sku, nil
}

func (r *Repository) updateSKUProfile(ctx context.Context, skuID uint64, input UpdateSKUInput) error {
	return r.db.WithContext(ctx).Model(&ProductSKU{}).Where("id = ?", skuID).Updates(map[string]any{
		"sku_code":            input.SKUCode,
		"specs_json":          input.Specs,
		"price_cent":          input.PriceCent,
		"original_price_cent": input.OriginalPriceCent,
		"version":             gorm.Expr("version + 1"),
	}).Error
}

func (r *Repository) updateSKUStatus(ctx context.Context, skuID uint64, status SKUStatus) error {
	return r.db.WithContext(ctx).Model(&ProductSKU{}).Where("id = ?", skuID).Updates(map[string]any{
		"status": status, "version": gorm.Expr("version + 1"),
	}).Error
}

func (r *Repository) adjustAvailableStock(ctx context.Context, skuID uint64, delta int64) error {
	query := r.db.WithContext(ctx).Model(&ProductSKU{}).Where("id = ?", skuID)
	if delta < 0 {
		query = query.Where("available_stock >= ?", -delta)
	}
	result := query.Updates(map[string]any{
		"available_stock": gorm.Expr("available_stock + ?", delta),
		"version":         gorm.Expr("version + 1"),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repository) countSellableSKUs(ctx context.Context, productID uint64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&ProductSKU{}).
		Where("product_id = ? AND status = ? AND available_stock > 0", productID, SKUStatusActive).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Repository) updateProductStatus(ctx context.Context, productID uint64, status ProductStatus) error {
	return r.db.WithContext(ctx).Model(&Product{}).Where("id = ?", productID).Update("status", status).Error
}

func (r *Repository) feedCommerceBatch(ctx context.Context, postIDs, authorIDs []uint64, productLimit int) (FeedCommerceBatch, error) {
	batch := FeedCommerceBatch{
		ShopsByOwner:   make(map[uint64]*FeedShopSummary, len(authorIDs)),
		ProductsByPost: make(map[uint64][]FeedProductCard, len(postIDs)),
	}
	for _, postID := range postIDs {
		batch.ProductsByPost[postID] = []FeedProductCard{}
	}
	if len(authorIDs) > 0 {
		var rows []struct {
			OwnerID   uint64
			ID        uint64
			Name      string
			AvatarURL string
		}
		if err := r.db.WithContext(ctx).Table("shops").
			Select("owner_id,id,name,avatar_url").
			Where("owner_id IN ? AND status = ?", authorIDs, ShopStatusOpen).
			Find(&rows).Error; err != nil {
			return FeedCommerceBatch{}, fmt.Errorf("batch feed shops: %w", err)
		}
		for _, row := range rows {
			batch.ShopsByOwner[row.OwnerID] = &FeedShopSummary{ID: row.ID, Name: row.Name, AvatarURL: row.AvatarURL}
		}
	}
	if len(postIDs) == 0 {
		return batch, nil
	}
	var cards []struct {
		PostID       uint64
		ID           uint64
		Name         string
		CoverURL     string
		MinPriceCent int64
		CardRank     int
	}
	if err := r.db.WithContext(ctx).Raw(`
SELECT ranked.post_id, ranked.id, ranked.name, ranked.cover_url,
       ranked.min_price_cent, ranked.card_rank
FROM (
    SELECT relation.post_id, product.id, product.name, product.cover_url,
           MIN(sku.price_cent) AS min_price_cent,
           ROW_NUMBER() OVER (
               PARTITION BY relation.post_id
               ORDER BY relation.sort_order ASC, product.id ASC
           ) AS card_rank
    FROM post_products AS relation
    JOIN posts AS post
      ON post.id = relation.post_id
     AND post.status = 'published'
     AND post.deleted_at IS NULL
    JOIN products AS product
      ON product.id = relation.product_id
     AND product.status = 'on_sale'
    JOIN shops AS shop
      ON shop.id = product.shop_id
     AND shop.owner_id = post.author_id
     AND shop.status = 'open'
    JOIN product_skus AS sku
      ON sku.product_id = product.id
     AND sku.status = 'active'
     AND sku.available_stock > 0
    WHERE relation.post_id IN ?
    GROUP BY relation.post_id, relation.sort_order,
             product.id, product.name, product.cover_url
) AS ranked
WHERE ranked.card_rank <= ?
ORDER BY ranked.post_id ASC, ranked.card_rank ASC`, postIDs, productLimit).Scan(&cards).Error; err != nil {
		return FeedCommerceBatch{}, fmt.Errorf("batch feed product cards: %w", err)
	}
	for _, card := range cards {
		batch.ProductsByPost[card.PostID] = append(batch.ProductsByPost[card.PostID], FeedProductCard{
			ID: card.ID, Name: card.Name, CoverURL: card.CoverURL, MinPriceCent: card.MinPriceCent,
		})
	}
	return batch, nil
}

func (r *Repository) listRelatedPublicPosts(ctx context.Context, productID uint64, limit int, cursor *RelatedPostCursor) ([]content.Post, error) {
	query := r.db.WithContext(ctx).Model(&content.Post{}).
		Preload("Images", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).
		Preload("Tags", func(db *gorm.DB) *gorm.DB { return db.Order("name ASC, id ASC") }).
		Joins("JOIN post_products AS relation ON relation.post_id = posts.id").
		Where("relation.product_id = ? AND posts.status = ? AND posts.deleted_at IS NULL", productID, content.PostStatusPublished)
	if cursor != nil {
		query = query.Where(
			"posts.published_at < ? OR (posts.published_at = ? AND posts.id < ?)",
			cursor.PublishedAt, cursor.PublishedAt, cursor.ID,
		)
	}
	var posts []content.Post
	if err := query.Order("posts.published_at DESC, posts.id DESC").Limit(limit).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list product related posts: %w", err)
	}
	if posts == nil {
		posts = []content.Post{}
	}
	for index := range posts {
		if posts[index].Images == nil {
			posts[index].Images = []content.PostImage{}
		}
		if posts[index].Tags == nil {
			posts[index].Tags = []content.Tag{}
		}
		posts[index].ProductIDs = []uint64{}
	}
	if len(posts) > 0 {
		postIDs := make([]uint64, len(posts))
		byID := make(map[uint64]*content.Post, len(posts))
		for index := range posts {
			postIDs[index] = posts[index].ID
			byID[posts[index].ID] = &posts[index]
		}
		var relations []content.PostProduct
		if err := r.db.WithContext(ctx).Where("post_id IN ?", postIDs).
			Order("post_id ASC, sort_order ASC").Find(&relations).Error; err != nil {
			return nil, fmt.Errorf("load related post product ids: %w", err)
		}
		for _, relation := range relations {
			if post := byID[relation.PostID]; post != nil {
				post.ProductIDs = append(post.ProductIDs, relation.ProductID)
			}
		}
	}
	return posts, nil
}
