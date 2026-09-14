package content

import (
	"context"
	"errors"
	"time"

	"feedsystem_video_go/internal/cacheinvalidation"
	"feedsystem_video_go/internal/entitycache"
	rediscache "feedsystem_video_go/internal/middleware/redis"
)

type PublicEntityReader struct {
	posts *entitycache.Reader[Post]
}

func NewPublicEntityReader(repository *Repository, cache *rediscache.Client) (*PublicEntityReader, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("content public entity reader repository is nil")
	}
	reader, err := entitycache.NewReader(cache, func(id uint64) string {
		return entitycache.ContentEntityKey(cache, id)
	}, func(ctx context.Context, ids []uint64) (map[uint64]Post, error) {
		posts, err := repository.GetPublicByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		result := make(map[uint64]Post, len(posts))
		for _, post := range posts {
			result[post.ID] = post
		}
		return result, nil
	}, entitycache.Options{L1TTL: 5 * time.Second, L2TTL: time.Hour})
	if err != nil {
		return nil, err
	}
	return &PublicEntityReader{posts: reader}, nil
}

func (r *PublicEntityReader) Get(ctx context.Context, id uint64) (*Post, bool, error) {
	if r == nil || r.posts == nil {
		return nil, false, errors.New("content public entity reader is not initialized")
	}
	post, found, err := r.posts.Get(ctx, id)
	if err != nil || !found {
		return nil, found, err
	}
	return &post, true, nil
}

func (r *PublicEntityReader) GetMany(ctx context.Context, ids []uint64) ([]Post, error) {
	if r == nil || r.posts == nil {
		return nil, errors.New("content public entity reader is not initialized")
	}
	return r.posts.GetMany(ctx, ids)
}

func (r *PublicEntityReader) Invalidate(ctx context.Context, targets []cacheinvalidation.Target) error {
	if r == nil || r.posts == nil {
		return nil
	}
	ids := make([]uint64, 0, len(targets))
	for _, target := range targets {
		if target.Entity == cacheinvalidation.ContentEntity {
			ids = append(ids, target.ID)
		}
	}
	return r.posts.Delete(ctx, ids...)
}
