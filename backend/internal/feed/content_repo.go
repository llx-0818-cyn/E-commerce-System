package feed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedsystem_video_go/internal/content"
)

type PostCursor struct {
	PublishedAt time.Time `json:"published_at"`
	ID          uint64    `json:"id"`
}

// ListPublishedPosts 是图文 Feed 的唯一 MySQL 时间排序入口。
func (repo *FeedRepository) ListPublishedPosts(ctx context.Context, limit int, cursor *PostCursor) ([]content.Post, error) {
	if repo == nil || repo.db == nil {
		return nil, errors.New("feed repository is not initialized")
	}
	// 对外服务仍限制单页最多 100 条；仓储层额外允许热区重建一次读取 1000 条。
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("post feed query limit must be in 1..1000")
	}
	query := repo.db.WithContext(ctx).Model(&content.Post{}).
		Where("status = ? AND deleted_at IS NULL", content.PostStatusPublished)
	if cursor != nil {
		if cursor.PublishedAt.IsZero() || cursor.ID == 0 {
			return nil, errors.New("post cursor requires published_at and id")
		}
		query = query.Where(
			"published_at < ? OR (published_at = ? AND id < ?)",
			cursor.PublishedAt,
			cursor.PublishedAt,
			cursor.ID,
		)
	}
	var posts []content.Post
	if err := query.Order("published_at DESC, id DESC").Limit(limit).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list published posts: %w", err)
	}
	if posts == nil {
		posts = []content.Post{}
	}
	return posts, nil
}

func (repo *FeedRepository) GetPublishedPostsByIDs(ctx context.Context, ids []uint64) ([]content.Post, error) {
	if len(ids) == 0 {
		return []content.Post{}, nil
	}
	if repo.contentReader != nil {
		return repo.contentReader.GetMany(ctx, ids)
	}
	var posts []content.Post
	if err := repo.db.WithContext(ctx).Model(&content.Post{}).
		Where("id IN ? AND status = ? AND deleted_at IS NULL", ids, content.PostStatusPublished).
		Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("get published posts by ids: %w", err)
	}
	return posts, nil
}

// ListPublishedPostsByFollowing 按查询时的当前关注图筛选公开 Post。
// EXISTS 避免脏的重复关系放大结果，并保持与最新流一致的复合游标语义。
func (repo *FeedRepository) ListPublishedPostsByFollowing(ctx context.Context, viewerAccountID uint64, limit int, cursor *PostCursor) ([]content.Post, error) {
	if repo == nil || repo.db == nil {
		return nil, errors.New("feed repository is not initialized")
	}
	if viewerAccountID == 0 {
		return nil, errors.New("following feed viewer account is required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("following post feed query limit must be in 1..1000")
	}
	query := repo.db.WithContext(ctx).Model(&content.Post{}).
		Where("status = ? AND deleted_at IS NULL", content.PostStatusPublished).
		Where(`EXISTS (
SELECT 1 FROM socials
WHERE socials.follower_id = ? AND socials.vlogger_id = posts.author_id
)`, viewerAccountID)
	if cursor != nil {
		if cursor.PublishedAt.IsZero() || cursor.ID == 0 {
			return nil, errors.New("post cursor requires published_at and id")
		}
		query = query.Where(
			"published_at < ? OR (published_at = ? AND id < ?)",
			cursor.PublishedAt,
			cursor.PublishedAt,
			cursor.ID,
		)
	}
	var posts []content.Post
	if err := query.Order("published_at DESC, id DESC").Limit(limit).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list published posts by following: %w", err)
	}
	if posts == nil {
		posts = []content.Post{}
	}
	return posts, nil
}

type PopularityCursor struct {
	Popularity  float64   `json:"popularity"`
	PublishedAt time.Time `json:"published_at"`
	ID          uint64    `json:"id"`
}

func (repo *FeedRepository) ListPublishedPostsByPopularity(ctx context.Context, limit int, cursor *PopularityCursor) ([]content.Post, error) {
	if repo == nil || repo.db == nil {
		return nil, errors.New("feed repository is not initialized")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("popular post feed query limit must be in 1..1000")
	}
	query := repo.db.WithContext(ctx).Model(&content.Post{}).
		Where("status = ? AND deleted_at IS NULL", content.PostStatusPublished)
	if cursor != nil {
		if cursor.PublishedAt.IsZero() || cursor.ID == 0 {
			return nil, errors.New("popularity cursor requires published_at and id")
		}
		query = query.Where(
			`popularity < ?
OR (popularity = ? AND published_at < ?)
OR (popularity = ? AND published_at = ? AND id < ?)`,
			cursor.Popularity,
			cursor.Popularity,
			cursor.PublishedAt,
			cursor.Popularity,
			cursor.PublishedAt,
			cursor.ID,
		)
	}
	var posts []content.Post
	if err := query.Order("popularity DESC, published_at DESC, id DESC").Limit(limit).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list published posts by popularity: %w", err)
	}
	if posts == nil {
		posts = []content.Post{}
	}
	return posts, nil
}

func (repo *FeedRepository) ListPublishedPostsByTag(ctx context.Context, tagName string, limit int, cursor *PostCursor) ([]content.Post, error) {
	if repo == nil || repo.db == nil {
		return nil, errors.New("feed repository is not initialized")
	}
	if tagName == "" {
		return nil, errors.New("post feed tag is required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("tag post feed query limit must be in 1..1000")
	}
	query := repo.db.WithContext(ctx).Model(&content.Post{}).
		Joins("JOIN post_tags ON post_tags.post_id = posts.id").
		Joins("JOIN tags ON tags.id = post_tags.tag_id").
		Where("tags.name = ? AND posts.status = ? AND posts.deleted_at IS NULL", tagName, content.PostStatusPublished)
	if cursor != nil {
		if cursor.PublishedAt.IsZero() || cursor.ID == 0 {
			return nil, errors.New("post cursor requires published_at and id")
		}
		query = query.Where(
			"posts.published_at < ? OR (posts.published_at = ? AND posts.id < ?)",
			cursor.PublishedAt,
			cursor.PublishedAt,
			cursor.ID,
		)
	}
	var posts []content.Post
	if err := query.Order("posts.published_at DESC, posts.id DESC").Limit(limit).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list published posts by tag: %w", err)
	}
	if posts == nil {
		posts = []content.Post{}
	}
	return posts, nil
}
