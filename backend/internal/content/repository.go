package content

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("content repository database is nil")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) transaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

func (r *Repository) getForUpdate(ctx context.Context, tx *gorm.DB, id uint64) (*Post, error) {
	var post Post
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&post).Error
	if err != nil {
		return nil, err
	}
	return &post, nil
}

func (r *Repository) GetPublic(ctx context.Context, id uint64) (*Post, error) {
	posts, err := r.GetPublicByIDs(ctx, []uint64{id})
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &posts[0], nil
}

func (r *Repository) GetPublicByIDs(ctx context.Context, ids []uint64) ([]Post, error) {
	if len(ids) == 0 {
		return []Post{}, nil
	}
	var posts []Post
	err := r.db.WithContext(ctx).
		Preload("Images", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).
		Preload("Tags", func(db *gorm.DB) *gorm.DB { return db.Order("name ASC, id ASC") }).
		Where("id IN ? AND status = ? AND deleted_at IS NULL", ids, PostStatusPublished).
		Find(&posts).Error
	if err != nil {
		return nil, err
	}
	if posts == nil {
		posts = []Post{}
	}
	for index := range posts {
		if posts[index].Images == nil {
			posts[index].Images = []PostImage{}
		}
		if posts[index].Tags == nil {
			posts[index].Tags = []Tag{}
		}
	}
	if err := r.attachProductIDs(ctx, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (r *Repository) ListPublicByAuthor(ctx context.Context, query AuthorListQuery) ([]Post, error) {
	database := r.db.WithContext(ctx).
		Preload("Images", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).
		Preload("Tags", func(db *gorm.DB) *gorm.DB { return db.Order("name ASC, id ASC") }).
		Where("author_id = ? AND status = ? AND deleted_at IS NULL", query.AuthorID, PostStatusPublished)
	if !query.BeforeTime.IsZero() {
		database = database.Where(
			"published_at < ? OR (published_at = ? AND id < ?)",
			query.BeforeTime,
			query.BeforeTime,
			query.BeforeID,
		)
	}
	var posts []Post
	if err := database.Order("published_at DESC, id DESC").Limit(query.Limit).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list author posts: %w", err)
	}
	if posts == nil {
		posts = []Post{}
	}
	for index := range posts {
		if posts[index].Images == nil {
			posts[index].Images = []PostImage{}
		}
		if posts[index].Tags == nil {
			posts[index].Tags = []Tag{}
		}
	}
	if err := r.attachProductIDs(ctx, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (r *Repository) attachProductIDs(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	postIDs := make([]uint64, len(posts))
	byPostID := make(map[uint64]*Post, len(posts))
	for index := range posts {
		postIDs[index] = posts[index].ID
		posts[index].ProductIDs = []uint64{}
		byPostID[posts[index].ID] = &posts[index]
	}
	var relations []PostProduct
	if err := r.db.WithContext(ctx).Where("post_id IN ?", postIDs).
		Order("post_id ASC, sort_order ASC").Find(&relations).Error; err != nil {
		return fmt.Errorf("load post product relations: %w", err)
	}
	for _, relation := range relations {
		if post := byPostID[relation.PostID]; post != nil {
			post.ProductIDs = append(post.ProductIDs, relation.ProductID)
		}
	}
	return nil
}

func (r *Repository) CountPublicByAuthor(ctx context.Context, authorID uint64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&Post{}).
		Where("author_id = ? AND status = ? AND deleted_at IS NULL", authorID, PostStatusPublished).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count author posts: %w", err)
	}
	return count, nil
}

func (r *Repository) SumPublicLikesByAuthor(ctx context.Context, authorID uint64) (uint64, error) {
	var total uint64
	if err := r.db.WithContext(ctx).Model(&Post{}).
		Where("author_id = ? AND status = ? AND deleted_at IS NULL", authorID, PostStatusPublished).
		Select("COALESCE(SUM(likes_count), 0)").Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum author post likes: %w", err)
	}
	return total, nil
}

func (r *Repository) ImagesByPostIDs(ctx context.Context, postIDs []uint64) (map[uint64][]PostImage, error) {
	imagesByPost := make(map[uint64][]PostImage, len(postIDs))
	for _, postID := range postIDs {
		imagesByPost[postID] = []PostImage{}
	}
	if len(postIDs) == 0 {
		return imagesByPost, nil
	}
	var images []PostImage
	if err := r.db.WithContext(ctx).
		Where("post_id IN ?", postIDs).
		Order("post_id ASC, sort_order ASC, id ASC").
		Find(&images).Error; err != nil {
		return nil, fmt.Errorf("batch post images: %w", err)
	}
	for _, image := range images {
		if image.PostID != nil {
			imagesByPost[*image.PostID] = append(imagesByPost[*image.PostID], image)
		}
	}
	return imagesByPost, nil
}
