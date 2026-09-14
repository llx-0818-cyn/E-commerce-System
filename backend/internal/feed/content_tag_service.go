package feed

import (
	"context"
	"errors"

	"feedsystem_video_go/internal/content"
)

type ContentTagService struct {
	repository *FeedRepository
}

func NewContentTagService(repository *FeedRepository) (*ContentTagService, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("content tag repository is nil")
	}
	return &ContentTagService{repository: repository}, nil
}

func (s *ContentTagService) ListByTag(ctx context.Context, rawTag string, limit int, cursor *PostCursor) (LatestPostPage, error) {
	tagName, err := content.NormalizeTagName(rawTag)
	if err != nil {
		return LatestPostPage{}, err
	}
	if limit <= 0 || limit > 100 {
		return LatestPostPage{}, errors.New("content tag limit must be in 1..100")
	}
	if cursor != nil && (cursor.PublishedAt.IsZero() || cursor.ID == 0) {
		return LatestPostPage{}, errors.New("content tag cursor is incomplete")
	}
	posts, err := s.repository.ListPublishedPostsByTag(ctx, tagName, limit+1, cursor)
	if err != nil {
		return LatestPostPage{}, err
	}
	return buildLatestPage(posts, limit), nil
}
