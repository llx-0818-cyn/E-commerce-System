package search

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("search repository database is nil")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Search(ctx context.Context, query Query) ([]Hit, error) {
	switch query.Scope {
	case ScopeContent:
		return r.searchContent(ctx, query)
	case ScopeProduct:
		return r.searchProducts(ctx, query)
	case ScopeShop:
		return r.searchShops(ctx, query)
	default:
		return nil, ErrInvalidQuery
	}
}

func (r *Repository) searchContent(ctx context.Context, query Query) ([]Hit, error) {
	var hits []Hit
	err := r.db.WithContext(ctx).Raw(`
SELECT post.id,
       post.title,
       LEFT(post.body, 200) AS subtitle,
       COALESCE((
           SELECT image.image_url
           FROM post_images AS image
           WHERE image.post_id=post.id
           ORDER BY image.sort_order ASC,image.id ASC
           LIMIT 1
       ), '') AS image_url,
       post.published_at
FROM posts AS post
WHERE post.status='published'
  AND post.deleted_at IS NULL
  AND (
      LOCATE(?, post.title) > 0
      OR LOCATE(?, post.body) > 0
      OR EXISTS (
          SELECT 1
          FROM post_tags AS relation
          JOIN tags AS tag ON tag.id=relation.tag_id
          WHERE relation.post_id=post.id AND LOCATE(?, tag.name) > 0
      )
  )
ORDER BY post.published_at DESC,post.id DESC
LIMIT ? OFFSET ?`, query.Keyword, query.Keyword, query.Keyword, query.Limit, query.Offset).Scan(&hits).Error
	if err != nil {
		return nil, fmt.Errorf("search content: %w", err)
	}
	setHitType(hits, ScopeContent)
	return nonNilHits(hits), nil
}

func (r *Repository) searchProducts(ctx context.Context, query Query) ([]Hit, error) {
	var hits []Hit
	err := r.db.WithContext(ctx).Raw(`
SELECT product.id,
       product.name AS title,
       product.subtitle,
       product.cover_url AS image_url,
       (
           SELECT MIN(sku.price_cent)
           FROM product_skus AS sku
           WHERE sku.product_id=product.id
             AND sku.status='active'
             AND sku.available_stock > 0
       ) AS price_cent
FROM products AS product
JOIN shops AS shop ON shop.id=product.shop_id AND shop.status <> 'disabled'
WHERE product.status='on_sale'
  AND LOCATE(?, product.name) > 0
ORDER BY product.sales_count DESC,product.created_at DESC,product.id DESC
LIMIT ? OFFSET ?`, query.Keyword, query.Limit, query.Offset).Scan(&hits).Error
	if err != nil {
		return nil, fmt.Errorf("search products: %w", err)
	}
	setHitType(hits, ScopeProduct)
	return nonNilHits(hits), nil
}

func (r *Repository) searchShops(ctx context.Context, query Query) ([]Hit, error) {
	var hits []Hit
	err := r.db.WithContext(ctx).Raw(`
SELECT shop.id,
       shop.name AS title,
       shop.description AS subtitle,
       shop.avatar_url AS image_url
FROM shops AS shop
WHERE shop.status <> 'disabled'
  AND LOCATE(?, shop.name) > 0
ORDER BY (shop.status='open') DESC,shop.created_at DESC,shop.id DESC
LIMIT ? OFFSET ?`, query.Keyword, query.Limit, query.Offset).Scan(&hits).Error
	if err != nil {
		return nil, fmt.Errorf("search shops: %w", err)
	}
	setHitType(hits, ScopeShop)
	return nonNilHits(hits), nil
}

func setHitType(hits []Hit, scope Scope) {
	for index := range hits {
		hits[index].Type = scope
	}
}

func nonNilHits(hits []Hit) []Hit {
	if hits == nil {
		return []Hit{}
	}
	return hits
}
