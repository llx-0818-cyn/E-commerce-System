package feed

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"feedsystem_video_go/internal/content"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	redis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

type LatestPostPage struct {
	Posts      []content.Post `json:"posts"`
	NextCursor *PostCursor    `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

type ContentLatestService struct {
	repository *FeedRepository
	cache      *rediscache.Client
	rebuild    singleflight.Group
}

func NewContentLatestService(repository *FeedRepository, cache *rediscache.Client) (*ContentLatestService, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("content latest repository is nil")
	}
	return &ContentLatestService{repository: repository, cache: cache}, nil
}

func (s *ContentLatestService) ListLatest(ctx context.Context, limit int, cursor *PostCursor) (LatestPostPage, error) {
	if limit <= 0 || limit > 100 {
		return LatestPostPage{}, errors.New("content latest limit must be in 1..100")
	}
	if cursor != nil && (cursor.PublishedAt.IsZero() || cursor.ID == 0) {
		return LatestPostPage{}, errors.New("content latest cursor is incomplete")
	}
	if s.cache == nil {
		return s.listFromDB(ctx, limit, cursor)
	}

	hotPosts, err := s.hotPosts(ctx, cursor)
	if err != nil {
		return s.listFromDB(ctx, limit, cursor)
	}
	if len(hotPosts) == 0 {
		if _, rebuildErr, _ := s.rebuild.Do(ContentTimelineKey(s.cache), func() (any, error) {
			return nil, s.rebuildTimeline(ctx)
		}); rebuildErr != nil {
			return s.listFromDB(ctx, limit, cursor)
		}
		hotPosts, err = s.hotPosts(ctx, cursor)
		if err != nil {
			return s.listFromDB(ctx, limit, cursor)
		}
	}
	if len(hotPosts) > limit {
		return buildLatestPage(hotPosts, limit), nil
	}

	// 热区不足时使用原始请求游标查 DB，再按 ID 去重/排序；这样即使 Redis
	// 时间线局部缺项，也不会以热区最后一条作为游标而跳过缺失内容。
	dbPosts, err := s.repository.ListPublishedPosts(ctx, limit+1, cursor)
	if err != nil {
		return LatestPostPage{}, err
	}
	combined := mergePosts(hotPosts, dbPosts)
	return buildLatestPage(combined, limit), nil
}

func (s *ContentLatestService) hotPosts(ctx context.Context, cursor *PostCursor) ([]content.Post, error) {
	members, err := s.cache.ZRangeWithScores(ctx, ContentTimelineKey(s.cache), 0, -1)
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(members))
	for _, member := range members {
		id, err := strconv.ParseUint(fmt.Sprint(member.Member), 10, 64)
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	posts, err := s.repository.GetPublishedPostsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered := posts[:0]
	for _, post := range posts {
		if post.PublishedAt == nil {
			continue
		}
		if cursor == nil || post.PublishedAt.Before(cursor.PublishedAt) || (post.PublishedAt.Equal(cursor.PublishedAt) && post.ID < cursor.ID) {
			filtered = append(filtered, post)
		}
	}
	sortPosts(filtered)
	return filtered, nil
}

func (s *ContentLatestService) rebuildTimeline(ctx context.Context) error {
	posts, err := s.repository.ListPublishedPosts(ctx, 1000, nil)
	if err != nil {
		return err
	}
	if len(posts) == 0 {
		return nil
	}
	members := make([]redis.Z, 0, len(posts))
	for _, post := range posts {
		if post.PublishedAt == nil {
			continue
		}
		members = append(members, redis.Z{Score: float64(post.PublishedAt.UnixMilli()), Member: strconv.FormatUint(post.ID, 10)})
	}
	if len(members) == 0 {
		return nil
	}
	if err := s.cache.ZAdd(ctx, ContentTimelineKey(s.cache), members...); err != nil {
		return err
	}
	// ListPublishedPosts 已将重建集合限制为最新 1000 条，不需要再次按负索引裁剪。
	// 避免某些 Redis 兼容实现把超出集合长度的负索引错误解释为 0，误删整个集合。
	return nil
}

func (s *ContentLatestService) listFromDB(ctx context.Context, limit int, cursor *PostCursor) (LatestPostPage, error) {
	posts, err := s.repository.ListPublishedPosts(ctx, limit+1, cursor)
	if err != nil {
		return LatestPostPage{}, err
	}
	return buildLatestPage(posts, limit), nil
}

func mergePosts(groups ...[]content.Post) []content.Post {
	byID := make(map[uint64]content.Post)
	for _, group := range groups {
		for _, post := range group {
			byID[post.ID] = post
		}
	}
	posts := make([]content.Post, 0, len(byID))
	for _, post := range byID {
		posts = append(posts, post)
	}
	sortPosts(posts)
	return posts
}

func sortPosts(posts []content.Post) {
	sort.Slice(posts, func(i, j int) bool {
		left, right := posts[i].PublishedAt, posts[j].PublishedAt
		if left == nil || right == nil {
			return left != nil
		}
		if left.Equal(*right) {
			return posts[i].ID > posts[j].ID
		}
		return left.After(*right)
	})
}

func buildLatestPage(posts []content.Post, limit int) LatestPostPage {
	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	page := LatestPostPage{Posts: posts, HasMore: hasMore}
	if len(posts) > 0 {
		last := posts[len(posts)-1]
		if last.PublishedAt != nil {
			page.NextCursor = &PostCursor{PublishedAt: last.PublishedAt.UTC(), ID: last.ID}
		}
	}
	return page
}
