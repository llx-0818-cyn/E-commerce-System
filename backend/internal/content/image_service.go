package content

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ImageService struct {
	repository *Repository
	storage    Storage
}

func NewImageService(repository *Repository, storage Storage) (*ImageService, error) {
	if repository == nil || repository.db == nil || storage == nil {
		return nil, errors.New("image service repository and storage are required")
	}
	return &ImageService{repository: repository, storage: storage}, nil
}

func (s *ImageService) Upload(ctx context.Context, ownerID uint64, filename string, source io.Reader) (*PostImage, error) {
	if ownerID == 0 || source == nil {
		return nil, ErrPostValidation
	}
	stored, err := s.storage.Save(ctx, ownerID, filename, source)
	if err != nil {
		return nil, err
	}
	image := &PostImage{
		OwnerID:    ownerID,
		StorageKey: stored.StorageKey,
		ImageURL:   stored.ImageURL,
		MIMEType:   stored.MIMEType,
		ByteSize:   stored.ByteSize,
		Width:      stored.Width,
		Height:     stored.Height,
	}
	if err := s.repository.db.WithContext(ctx).Create(image).Error; err != nil {
		_ = s.storage.Delete(ctx, stored.StorageKey)
		return nil, fmt.Errorf("persist uploaded image metadata: %w", err)
	}
	return image, nil
}

func (s *ImageService) DeleteUnused(ctx context.Context, ownerID, imageID uint64) error {
	if ownerID == 0 || imageID == 0 {
		return ErrPostValidation
	}
	return s.repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var image PostImage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", imageID, ownerID).First(&image).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPostNotFound
			}
			return err
		}
		if image.PostID != nil {
			return ErrImageInUse
		}
		if err := s.storage.Delete(ctx, image.StorageKey); err != nil {
			return err
		}
		if err := tx.Delete(&PostImage{}, image.ID).Error; err != nil {
			return fmt.Errorf("delete unused image metadata: %w", err)
		}
		return nil
	})
}

func (s *ImageService) CleanupUnused(ctx context.Context, olderThan time.Time, limit int) (int, error) {
	if olderThan.IsZero() {
		return 0, errors.New("unused image cleanup cutoff is required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return 0, errors.New("unused image cleanup limit cannot exceed 1000")
	}
	var images []PostImage
	if err := s.repository.db.WithContext(ctx).
		Where("post_id IS NULL AND created_at < ?", olderThan).
		Order("created_at, id").
		Limit(limit).
		Find(&images).Error; err != nil {
		return 0, fmt.Errorf("query unused images: %w", err)
	}
	deleted := 0
	for _, image := range images {
		if err := s.DeleteUnused(ctx, image.OwnerID, image.ID); err != nil {
			if errors.Is(err, ErrPostNotFound) || errors.Is(err, ErrImageInUse) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
