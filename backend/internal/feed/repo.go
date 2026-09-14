package feed

import (
	"context"
	"feedsystem_video_go/internal/content"

	"gorm.io/gorm"
)

type FeedRepository struct {
	db            *gorm.DB
	contentReader PublishedPostReader
}

type PublishedPostReader interface {
	GetMany(context.Context, []uint64) ([]content.Post, error)
}

func NewFeedRepository(db *gorm.DB, readers ...PublishedPostReader) *FeedRepository {
	repository := &FeedRepository{db: db}
	if len(readers) > 0 {
		repository.contentReader = readers[0]
	}
	return repository
}
