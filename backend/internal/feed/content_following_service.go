package feed

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"feedsystem_video_go/internal/content"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	"golang.org/x/sync/singleflight"
)

const contentFollowingCacheTTL = 30 * time.Second

type ContentFollowingService struct {
	repository *FeedRepository
	cache      *rediscache.Client
	loads      singleflight.Group
}

func NewContentFollowingService(repository *FeedRepository, cache *rediscache.Client) (*ContentFollowingService, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("content following repository is nil")
	}
	return &ContentFollowingService{repository: repository, cache: cache}, nil
}

func (s *ContentFollowingService) ListFollowing(ctx context.Context, viewerAccountID uint64, limit int, cursor *PostCursor) (LatestPostPage, error) {
	if viewerAccountID == 0 {
		return LatestPostPage{}, errors.New("content following viewer account is required")
	}
	if limit <= 0 || limit > 100 {
		return LatestPostPage{}, errors.New("content following limit must be in 1..100")
	}
	if cursor != nil && (cursor.PublishedAt.IsZero() || cursor.ID == 0) {
		return LatestPostPage{}, errors.New("content following cursor is incomplete")
	}
	if s.cache == nil {
		return s.listFromDB(ctx, viewerAccountID, limit, cursor)
	}

	cacheKey := ContentFollowingCacheKey(s.cache, viewerAccountID, limit, cursor)
	if page, ok := s.readCache(ctx, cacheKey); ok {
		return page, nil
	}
	loaded, err, _ := s.loads.Do(cacheKey, func() (any, error) {
		if page, ok := s.readCache(ctx, cacheKey); ok {
			return page, nil
		}
		page, err := s.listFromDB(ctx, viewerAccountID, limit, cursor)
		if err != nil {
			return LatestPostPage{}, err
		}
		s.writeCache(ctx, cacheKey, page)
		return page, nil
	})
	if err != nil {
		return LatestPostPage{}, err
	}
	return loaded.(LatestPostPage), nil
}

func (s *ContentFollowingService) listFromDB(ctx context.Context, viewerAccountID uint64, limit int, cursor *PostCursor) (LatestPostPage, error) {
	posts, err := s.repository.ListPublishedPostsByFollowing(ctx, viewerAccountID, limit+1, cursor)
	if err != nil {
		return LatestPostPage{}, err
	}
	return buildLatestPage(posts, limit), nil
}

func (s *ContentFollowingService) readCache(ctx context.Context, key string) (LatestPostPage, bool) {
	cacheCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	data, err := s.cache.GetBytes(cacheCtx, key)
	if err != nil {
		return LatestPostPage{}, false
	}
	var page LatestPostPage
	if err := json.Unmarshal(data, &page); err != nil {
		return LatestPostPage{}, false
	}
	if page.Posts == nil {
		page.Posts = []content.Post{}
	}
	return page, true
}

func (s *ContentFollowingService) writeCache(ctx context.Context, key string, page LatestPostPage) {
	data, err := json.Marshal(page)
	if err != nil {
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = s.cache.SetBytes(cacheCtx, key, data, contentFollowingCacheTTL)
}
