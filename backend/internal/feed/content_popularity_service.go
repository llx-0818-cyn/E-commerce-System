package feed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	rediscache "feedsystem_video_go/internal/middleware/redis"
)

const (
	contentPopularityWindowCount = 60
	contentPopularityWindowTTL   = 2 * time.Hour
	contentPopularitySnapshotTTL = 2 * time.Minute
)

var ErrContentPopularitySnapshotExpired = errors.New("content popularity snapshot expired; restart pagination")

type ContentPopularityPage struct {
	Posts              []content.Post    `json:"posts"`
	SnapshotAt         *time.Time        `json:"snapshot_at,omitempty"`
	NextOffset         int               `json:"next_offset"`
	NextFallbackCursor *PopularityCursor `json:"next_fallback_cursor,omitempty"`
	HasMore            bool              `json:"has_more"`
	Degraded           bool              `json:"degraded"`
}

type ContentPopularityRecorder struct {
	cache *rediscache.Client
}

func NewContentPopularityRecorder(cache *rediscache.Client) (*ContentPopularityRecorder, error) {
	if cache == nil {
		return nil, errors.New("content popularity cache is nil")
	}
	return &ContentPopularityRecorder{cache: cache}, nil
}

func (r *ContentPopularityRecorder) RecordSignal(ctx context.Context, postID uint64, score float64, occurredAt time.Time) error {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return r.recordWindowSignal(ctx, ContentPopularityWindowKey(r.cache, occurredAt), postID, score)
}

func (r *ContentPopularityRecorder) RecordPaidSignal(ctx context.Context, postID uint64, score float64, occurredAt time.Time) error {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return r.recordWindowSignal(ctx, ContentPopularityPaidWindowKey(r.cache, occurredAt), postID, score)
}

func (r *ContentPopularityRecorder) recordWindowSignal(ctx context.Context, key string, postID uint64, score float64) error {
	if postID == 0 || score == 0 {
		return errors.New("content popularity post and non-zero score are required")
	}
	opCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := r.cache.ZincrBy(opCtx, key, strconv.FormatUint(postID, 10), score); err != nil {
		return fmt.Errorf("increment content popularity window: %w", err)
	}
	if err := r.cache.Expire(opCtx, key, contentPopularityWindowTTL); err != nil {
		return fmt.Errorf("expire content popularity window: %w", err)
	}
	return nil
}

type ContentPopularityService struct {
	repository          *FeedRepository
	cache               *rediscache.Client
	now                 func() time.Time
	decayHalfLife       time.Duration
	paidContributionCap float64
}

func NewContentPopularityService(repository *FeedRepository, cache *rediscache.Client, popularity ...config.PopularityConfig) (*ContentPopularityService, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("content popularity repository is nil")
	}
	if len(popularity) > 1 {
		return nil, errors.New("content popularity service accepts at most one popularity config")
	}
	decayHalfLife, paidCap := 24*time.Hour, float64(24)
	if len(popularity) == 1 {
		decayHalfLife = popularity[0].DecayHalfLife.Value()
		paidCap = popularity[0].PaidOrderContributionCap
	}
	if decayHalfLife <= 0 || math.IsNaN(paidCap) || math.IsInf(paidCap, 0) || paidCap < 0 {
		return nil, errors.New("content popularity service config is invalid")
	}
	return &ContentPopularityService{
		repository: repository, cache: cache, now: time.Now,
		decayHalfLife: decayHalfLife, paidContributionCap: paidCap,
	}, nil
}

func (s *ContentPopularityService) ListPopular(
	ctx context.Context,
	limit int,
	snapshotAt time.Time,
	offset int,
	fallbackCursor *PopularityCursor,
) (ContentPopularityPage, error) {
	if limit <= 0 || limit > 100 {
		return ContentPopularityPage{}, errors.New("content popularity limit must be in 1..100")
	}
	if offset < 0 {
		return ContentPopularityPage{}, errors.New("content popularity offset must not be negative")
	}
	if offset > 0 && snapshotAt.IsZero() {
		return ContentPopularityPage{}, errors.New("content popularity snapshot is required for non-zero offset")
	}
	if fallbackCursor != nil && (fallbackCursor.PublishedAt.IsZero() || fallbackCursor.ID == 0) {
		return ContentPopularityPage{}, errors.New("content popularity fallback cursor is incomplete")
	}
	if s.cache == nil {
		return s.listFromDB(ctx, limit, fallbackCursor)
	}
	if snapshotAt.IsZero() {
		snapshotAt = s.now().UTC().Truncate(time.Minute)
	} else {
		snapshotAt = snapshotAt.UTC().Truncate(time.Minute)
	}

	snapshotKey := ContentPopularitySnapshotKey(s.cache, snapshotAt)
	opCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	exists, err := s.cache.Exists(opCtx, snapshotKey)
	if err != nil {
		return s.listFromDB(ctx, limit, fallbackCursor)
	}
	if !exists {
		if offset > 0 {
			return ContentPopularityPage{}, ErrContentPopularitySnapshotExpired
		}
		keys := make([]string, 0, contentPopularityWindowCount)
		paidKeys := make([]string, 0, contentPopularityWindowCount)
		weights := make([]float64, 0, contentPopularityWindowCount)
		for index := 0; index < contentPopularityWindowCount; index++ {
			minute := snapshotAt.Add(-time.Duration(index) * time.Minute)
			keys = append(keys, ContentPopularityWindowKey(s.cache, minute))
			paidKeys = append(paidKeys, ContentPopularityPaidWindowKey(s.cache, minute))
			weights = append(weights, math.Pow(0.5, (time.Duration(index)*time.Minute).Seconds()/s.decayHalfLife.Seconds()))
		}
		if err := s.cache.ZUnionStoreWeighted(opCtx, snapshotKey, keys, weights, "SUM"); err != nil {
			return s.listFromDB(ctx, limit, fallbackCursor)
		}
		paidSnapshotKey := snapshotKey + ":paid"
		if s.paidContributionCap > 0 {
			if err := s.cache.ZUnionStoreWeighted(opCtx, paidSnapshotKey, paidKeys, weights, "SUM"); err != nil {
				_ = s.cache.Del(context.Background(), snapshotKey)
				return s.listFromDB(ctx, limit, fallbackCursor)
			}
			if err := s.cache.AddCappedZSetSupplement(opCtx, snapshotKey, paidSnapshotKey, s.paidContributionCap); err != nil {
				_ = s.cache.Del(context.Background(), snapshotKey)
				_ = s.cache.Del(context.Background(), paidSnapshotKey)
				return s.listFromDB(ctx, limit, fallbackCursor)
			}
			_ = s.cache.Del(context.Background(), paidSnapshotKey)
		}
		if err := s.cache.Expire(opCtx, snapshotKey, contentPopularitySnapshotTTL); err != nil {
			_ = s.cache.Del(context.Background(), snapshotKey)
			return s.listFromDB(ctx, limit, fallbackCursor)
		}
	}

	members, err := s.cache.ZRevRange(opCtx, snapshotKey, int64(offset), int64(offset+limit))
	if err != nil {
		return s.listFromDB(ctx, limit, fallbackCursor)
	}
	if len(members) == 0 {
		if offset == 0 {
			return s.listFromDB(ctx, limit, fallbackCursor)
		}
		return ContentPopularityPage{Posts: []content.Post{}, SnapshotAt: &snapshotAt, NextOffset: offset}, nil
	}
	hasMore := len(members) > limit
	if hasMore {
		members = members[:limit]
	}
	ids := make([]uint64, 0, len(members))
	for _, member := range members {
		id, err := strconv.ParseUint(member, 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	posts, err := s.repository.GetPublishedPostsByIDs(ctx, ids)
	if err != nil {
		return ContentPopularityPage{}, err
	}
	byID := make(map[uint64]content.Post, len(posts))
	for _, post := range posts {
		byID[post.ID] = post
	}
	ordered := make([]content.Post, 0, len(ids))
	for _, id := range ids {
		if post, ok := byID[id]; ok {
			ordered = append(ordered, post)
		}
	}
	page := ContentPopularityPage{
		Posts:      ordered,
		SnapshotAt: &snapshotAt,
		NextOffset: offset + len(members),
		HasMore:    hasMore,
	}
	page.NextFallbackCursor = popularityCursorForLastPost(ordered)
	return page, nil
}

func (s *ContentPopularityService) listFromDB(ctx context.Context, limit int, cursor *PopularityCursor) (ContentPopularityPage, error) {
	posts, err := s.repository.ListPublishedPostsByPopularity(ctx, limit+1, cursor)
	if err != nil {
		return ContentPopularityPage{}, err
	}
	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	return ContentPopularityPage{
		Posts:              posts,
		NextFallbackCursor: popularityCursorForLastPost(posts),
		HasMore:            hasMore,
		Degraded:           true,
	}, nil
}

func popularityCursorForLastPost(posts []content.Post) *PopularityCursor {
	if len(posts) == 0 {
		return nil
	}
	last := posts[len(posts)-1]
	if last.PublishedAt == nil {
		return nil
	}
	return &PopularityCursor{Popularity: last.Popularity, PublishedAt: last.PublishedAt.UTC(), ID: last.ID}
}
