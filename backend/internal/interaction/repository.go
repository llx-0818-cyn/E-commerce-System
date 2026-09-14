package interaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedsystem_video_go/internal/content"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("interaction repository database is nil")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

func lockPublicPost(ctx context.Context, tx *gorm.DB, postID uint64) (*content.Post, error) {
	var post content.Post
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND status = ? AND deleted_at IS NULL", postID, content.PostStatusPublished).
		First(&post).Error; err != nil {
		return nil, err
	}
	return &post, nil
}

func (r *Repository) lockComment(ctx context.Context, tx *gorm.DB, commentID uint64) (*PostComment, error) {
	var comment PostComment
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", commentID).First(&comment).Error; err != nil {
		return nil, err
	}
	return &comment, nil
}

func (r *Repository) listComments(ctx context.Context, query CommentListQuery) ([]PostComment, error) {
	database := r.db.WithContext(ctx).
		Where("post_id = ? AND status = 'active' AND deleted_at IS NULL", query.PostID)
	if !query.BeforeTime.IsZero() {
		database = database.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			query.BeforeTime,
			query.BeforeTime,
			query.BeforeID,
		)
	}
	var comments []PostComment
	if err := database.Order("created_at DESC, id DESC").Limit(query.Limit).Find(&comments).Error; err != nil {
		return nil, fmt.Errorf("list post comments: %w", err)
	}
	if comments == nil {
		comments = []PostComment{}
	}
	for index := range comments {
		comments[index].MentionedUsernames = ParseMentions(comments[index].Content)
	}
	return comments, nil
}

type favoriteRow struct {
	PostID    uint64
	CreatedAt time.Time
}

func (r *Repository) listFavorites(ctx context.Context, query FavoriteListQuery) ([]content.Post, []favoriteRow, error) {
	database := r.db.WithContext(ctx).
		Table("post_favorites AS favorite").
		Select("favorite.post_id, favorite.created_at").
		Joins("JOIN posts AS post ON post.id = favorite.post_id AND post.status = ? AND post.deleted_at IS NULL", content.PostStatusPublished).
		Where("favorite.account_id = ?", query.AccountID)
	if !query.BeforeTime.IsZero() {
		database = database.Where(
			"favorite.created_at < ? OR (favorite.created_at = ? AND favorite.post_id < ?)",
			query.BeforeTime,
			query.BeforeTime,
			query.BeforeID,
		)
	}
	var rows []favoriteRow
	if err := database.Order("favorite.created_at DESC, favorite.post_id DESC").Limit(query.Limit).Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("list favorite relations: %w", err)
	}
	if len(rows) == 0 {
		return []content.Post{}, rows, nil
	}
	ids := make([]uint64, len(rows))
	for index := range rows {
		ids[index] = rows[index].PostID
	}
	var posts []content.Post
	if err := r.db.WithContext(ctx).
		Preload("Images", func(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, id ASC") }).
		Preload("Tags", func(db *gorm.DB) *gorm.DB { return db.Order("name ASC, id ASC") }).
		Where("id IN ?", ids).
		Find(&posts).Error; err != nil {
		return nil, nil, fmt.Errorf("load favorite posts: %w", err)
	}
	byID := make(map[uint64]content.Post, len(posts))
	for _, post := range posts {
		if post.Images == nil {
			post.Images = []content.PostImage{}
		}
		if post.Tags == nil {
			post.Tags = []content.Tag{}
		}
		byID[post.ID] = post
	}
	ordered := make([]content.Post, 0, len(rows))
	for _, row := range rows {
		if post, ok := byID[row.PostID]; ok {
			ordered = append(ordered, post)
		}
	}
	return ordered, rows, nil
}
