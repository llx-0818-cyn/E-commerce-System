package search

import (
	"context"
	"fmt"
	"time"
)

type Document struct {
	Type        Scope      `json:"type"`
	ID          uint64     `json:"id"`
	Title       string     `json:"title"`
	Subtitle    string     `json:"subtitle,omitempty"`
	Body        string     `json:"body,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	ImageURL    string     `json:"image_url,omitempty"`
	PriceCent   *int64     `json:"price_cent,omitempty"`
	CategoryID  *uint64    `json:"category_id,omitempty"`
	SalesCount  uint64     `json:"sales_count,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

type DocumentSource interface {
	LoadDocument(context.Context, Scope, uint64) (*Document, bool, error)
	ListDocuments(context.Context, Scope, uint64, int) ([]Document, error)
}

func (r *Repository) LoadDocument(ctx context.Context, scope Scope, id uint64) (*Document, bool, error) {
	if id == 0 {
		return nil, false, ErrInvalidQuery
	}
	documents, err := r.loadDocuments(ctx, scope, id, 1, true)
	if err != nil || len(documents) == 0 {
		return nil, false, err
	}
	return &documents[0], true, nil
}

func (r *Repository) ListDocuments(ctx context.Context, scope Scope, afterID uint64, limit int) ([]Document, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalidQuery
	}
	return r.loadDocuments(ctx, scope, afterID, limit, false)
}

func (r *Repository) loadDocuments(ctx context.Context, scope Scope, id uint64, limit int, exact bool) ([]Document, error) {
	switch scope {
	case ScopeContent:
		return r.loadContentDocuments(ctx, id, limit, exact)
	case ScopeProduct:
		return r.loadProductDocuments(ctx, id, limit, exact)
	case ScopeShop:
		return r.loadShopDocuments(ctx, id, limit, exact)
	default:
		return nil, ErrInvalidQuery
	}
}

func (r *Repository) loadContentDocuments(ctx context.Context, id uint64, limit int, exact bool) ([]Document, error) {
	operator := ">"
	if exact {
		operator = "="
	}
	var documents []Document
	query := fmt.Sprintf(`
SELECT 'content' AS type,post.id,post.title,post.body,
       COALESCE((SELECT image.image_url FROM post_images AS image WHERE image.post_id=post.id ORDER BY image.sort_order,image.id LIMIT 1),'') AS image_url,
       post.published_at
FROM posts AS post
WHERE post.id %s ? AND post.status='published' AND post.deleted_at IS NULL
ORDER BY post.id ASC LIMIT ?`, operator)
	if err := r.db.WithContext(ctx).Raw(query, id, limit).Scan(&documents).Error; err != nil {
		return nil, fmt.Errorf("load content search documents: %w", err)
	}
	if len(documents) == 0 {
		return []Document{}, nil
	}
	ids := make([]uint64, len(documents))
	byID := make(map[uint64]*Document, len(documents))
	for index := range documents {
		ids[index] = documents[index].ID
		documents[index].Tags = []string{}
		byID[documents[index].ID] = &documents[index]
	}
	var tags []struct {
		PostID uint64 `gorm:"column:post_id"`
		Name   string `gorm:"column:name"`
	}
	if err := r.db.WithContext(ctx).Table("post_tags AS relation").
		Select("relation.post_id,tag.name").Joins("JOIN tags AS tag ON tag.id=relation.tag_id").
		Where("relation.post_id IN ?", ids).Order("relation.post_id,tag.name,tag.id").Scan(&tags).Error; err != nil {
		return nil, fmt.Errorf("load content search tags: %w", err)
	}
	for _, tag := range tags {
		if document := byID[tag.PostID]; document != nil {
			document.Tags = append(document.Tags, tag.Name)
		}
	}
	return documents, nil
}

func (r *Repository) loadProductDocuments(ctx context.Context, id uint64, limit int, exact bool) ([]Document, error) {
	operator := ">"
	if exact {
		operator = "="
	}
	var documents []Document
	query := fmt.Sprintf(`
SELECT 'product' AS type,product.id,product.name AS title,product.subtitle,product.description AS body,
       product.cover_url AS image_url,product.category_id,product.sales_count,
       (SELECT MIN(sku.price_cent) FROM product_skus AS sku WHERE sku.product_id=product.id AND sku.status='active' AND sku.available_stock>0) AS price_cent
FROM products AS product
JOIN shops AS shop ON shop.id=product.shop_id AND shop.status<>'disabled'
WHERE product.id %s ? AND product.status='on_sale'
ORDER BY product.id ASC LIMIT ?`, operator)
	if err := r.db.WithContext(ctx).Raw(query, id, limit).Scan(&documents).Error; err != nil {
		return nil, fmt.Errorf("load product search documents: %w", err)
	}
	return nonNilDocuments(documents), nil
}

func (r *Repository) loadShopDocuments(ctx context.Context, id uint64, limit int, exact bool) ([]Document, error) {
	operator := ">"
	if exact {
		operator = "="
	}
	var documents []Document
	query := fmt.Sprintf(`
SELECT 'shop' AS type,shop.id,shop.name AS title,shop.description AS body,shop.avatar_url AS image_url
FROM shops AS shop
WHERE shop.id %s ? AND shop.status<>'disabled'
ORDER BY shop.id ASC LIMIT ?`, operator)
	if err := r.db.WithContext(ctx).Raw(query, id, limit).Scan(&documents).Error; err != nil {
		return nil, fmt.Errorf("load shop search documents: %w", err)
	}
	return nonNilDocuments(documents), nil
}

func nonNilDocuments(documents []Document) []Document {
	if documents == nil {
		return []Document{}
	}
	return documents
}

var _ DocumentSource = (*Repository)(nil)
