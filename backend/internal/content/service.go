package content

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"feedsystem_video_go/internal/cacheinvalidation"
	"feedsystem_video_go/internal/config"
	domainevent "feedsystem_video_go/internal/event"
	searchdomain "feedsystem_video_go/internal/search"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPostNotFound       = errors.New("post not found")
	ErrPostForbidden      = errors.New("post operation forbidden")
	ErrPostValidation     = errors.New("invalid post")
	ErrImageUnavailable   = errors.New("post image is unavailable")
	ErrTagValidation      = errors.New("invalid post tag")
	ErrProductUnavailable = errors.New("post product is unavailable")
)

var (
	hashtagPattern = regexp.MustCompile(`#([\p{L}\p{N}_-]+)`)
	tagNamePattern = regexp.MustCompile(`^[\p{L}\p{N}_-]+$`)
)

type Service struct {
	repository         *Repository
	events             *domainevent.Repository
	publicReader       *PublicEntityReader
	searchIndexEnabled bool
	limits             config.ContentConfig
	now                func() time.Time
}

func NewService(repository *Repository, limits config.ContentConfig, readers ...*PublicEntityReader) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("content service repository is nil")
	}
	if len(readers) > 1 {
		return nil, errors.New("content service accepts at most one public entity reader")
	}
	if limits.MaxImages <= 0 || limits.MaxImageSizeBytes <= 0 || limits.MaxBodyLength <= 0 {
		return nil, errors.New("content service limits are invalid")
	}
	events, err := domainevent.NewRepository(repository.db)
	if err != nil {
		return nil, err
	}
	var publicReader *PublicEntityReader
	if len(readers) == 1 {
		publicReader = readers[0]
	}
	return &Service{repository: repository, events: events, publicReader: publicReader, limits: limits, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Publish(ctx context.Context, input PublishInput) (*Post, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if err := s.validatePostInput(input.AuthorID, input.Title, input.Body, input.ImageIDs); err != nil {
		return nil, err
	}
	if err := validatePostProductIDs(input.ProductIDs); err != nil {
		return nil, err
	}
	tagNames, err := normalizeTags(input.Title, input.Body, input.Tags)
	if err != nil {
		return nil, err
	}

	var postID uint64
	err = s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		now := s.now().UTC()
		post := &Post{
			AuthorID:    input.AuthorID,
			Title:       input.Title,
			Body:        input.Body,
			Status:      PostStatusPublished,
			PublishedAt: &now,
		}
		if err := tx.WithContext(ctx).Create(post).Error; err != nil {
			return fmt.Errorf("create post: %w", err)
		}
		postID = post.ID
		if err := s.replaceImages(ctx, tx, post, input.AuthorID, input.ImageIDs, true); err != nil {
			return err
		}
		if err := s.replaceTags(ctx, tx, post.ID, tagNames); err != nil {
			return err
		}
		if err := s.replaceProducts(ctx, tx, post.ID, input.AuthorID, input.ProductIDs); err != nil {
			return err
		}
		_, err := outbox.Append(ctx, domainevent.DomainEvent{
			AggregateType: "post",
			AggregateID:   fmt.Sprintf("%d", post.ID),
			EventType:     "content.published",
			Payload: map[string]any{
				"post_id":      post.ID,
				"author_id":    post.AuthorID,
				"published_at": now.Format(time.RFC3339Nano),
				"product_ids":  input.ProductIDs,
			},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidatePostAfterCommit(ctx, postID)
	s.recordSearchAfterCommit(postID, searchdomain.IndexUpsert)
	return s.repository.GetPublic(ctx, postID)
}

func (s *Service) Detail(ctx context.Context, id uint64) (*Post, error) {
	if id == 0 {
		return nil, ErrPostNotFound
	}
	var post *Post
	var err error
	if s.publicReader != nil {
		var found bool
		post, found, err = s.publicReader.Get(ctx, id)
		if err == nil && !found {
			err = gorm.ErrRecordNotFound
		}
	} else {
		post, err = s.repository.GetPublic(ctx, id)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, err
	}
	return post, nil
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (*Post, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.PostID == 0 {
		return nil, ErrPostNotFound
	}
	if err := s.validatePostInput(input.AuthorID, input.Title, input.Body, input.ImageIDs); err != nil {
		return nil, err
	}
	if err := validatePostProductIDs(input.ProductIDs); err != nil {
		return nil, err
	}
	tagNames, err := normalizeTags(input.Title, input.Body, input.Tags)
	if err != nil {
		return nil, err
	}

	err = s.repository.transaction(ctx, func(tx *gorm.DB) error {
		post, err := s.repository.getForUpdate(ctx, tx, input.PostID)
		if errors.Is(err, gorm.ErrRecordNotFound) || post.Status == PostStatusDeleted {
			return ErrPostNotFound
		}
		if err != nil {
			return err
		}
		if post.AuthorID != input.AuthorID {
			return ErrPostForbidden
		}
		if err := tx.WithContext(ctx).Model(&Post{}).Where("id = ?", post.ID).Updates(map[string]any{
			"title": input.Title,
			"body":  input.Body,
		}).Error; err != nil {
			return fmt.Errorf("update post: %w", err)
		}
		if err := s.replaceImages(ctx, tx, post, input.AuthorID, input.ImageIDs, false); err != nil {
			return err
		}
		if err := s.replaceTags(ctx, tx, post.ID, tagNames); err != nil {
			return err
		}
		return s.replaceProducts(ctx, tx, post.ID, input.AuthorID, input.ProductIDs)
	})
	if err != nil {
		return nil, err
	}
	s.invalidatePostAfterCommit(ctx, input.PostID)
	s.recordSearchAfterCommit(input.PostID, searchdomain.IndexUpsert)
	return s.repository.GetPublic(ctx, input.PostID)
}

func (s *Service) Delete(ctx context.Context, authorID, postID uint64) error {
	if authorID == 0 || postID == 0 {
		return ErrPostNotFound
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err := s.repository.transaction(ctx, func(tx *gorm.DB) error {
		post, err := s.repository.getForUpdate(ctx, tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) || post.Status == PostStatusDeleted {
			return ErrPostNotFound
		}
		if err != nil {
			return err
		}
		if post.AuthorID != authorID {
			return ErrPostForbidden
		}
		now := s.now().UTC()
		result := tx.WithContext(ctx).Model(&Post{}).
			Where("id = ? AND status <> ?", postID, PostStatusDeleted).
			Updates(map[string]any{"status": PostStatusDeleted, "deleted_at": now})
		if result.Error != nil {
			return fmt.Errorf("soft delete post: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrPostNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.invalidatePostAfterCommit(ctx, postID)
	s.recordSearchAfterCommit(postID, searchdomain.IndexDelete)
	return nil
}

func (s *Service) ReorderImages(ctx context.Context, authorID, postID uint64, imageIDs []uint64) (*Post, error) {
	if postID == 0 {
		return nil, ErrPostNotFound
	}
	if err := s.validateImageIDs(authorID, imageIDs); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err := s.repository.transaction(ctx, func(tx *gorm.DB) error {
		post, err := s.repository.getForUpdate(ctx, tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) || post.Status == PostStatusDeleted {
			return ErrPostNotFound
		}
		if err != nil {
			return err
		}
		if post.AuthorID != authorID {
			return ErrPostForbidden
		}
		return s.replaceImages(ctx, tx, post, authorID, imageIDs, false)
	})
	if err != nil {
		return nil, err
	}
	s.invalidatePostAfterCommit(ctx, postID)
	s.recordSearchAfterCommit(postID, searchdomain.IndexUpsert)
	return s.repository.GetPublic(ctx, postID)
}

func (s *Service) invalidatePostAfterCommit(ctx context.Context, postID uint64) {
	if s.publicReader == nil || postID == 0 {
		return
	}
	targets := []cacheinvalidation.Target{{Entity: cacheinvalidation.ContentEntity, ID: postID}}
	if err := s.publicReader.Invalidate(ctx, targets); err != nil {
		eventCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if appendErr := cacheinvalidation.AppendFallback(eventCtx, s.events, targets); appendErr != nil {
			log.Printf("append content cache invalidation fallback failed: post_id=%d delete_err=%v append_err=%v", postID, err, appendErr)
		}
	}
}

func (s *Service) recordSearchAfterCommit(postID uint64, action searchdomain.IndexAction) {
	if !s.searchIndexEnabled {
		return
	}
	eventCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := searchdomain.AppendIndexRequest(eventCtx, s.events, searchdomain.IndexRequest{
		Scope: searchdomain.ScopeContent, ID: postID, Action: action,
	}); err != nil {
		log.Printf("append content search index event failed: post_id=%d action=%s err=%v", postID, action, err)
	}
}

func (s *Service) EnableSearchIndexing() {
	s.searchIndexEnabled = true
}

func (s *Service) ListByAuthor(ctx context.Context, query AuthorListQuery) ([]Post, error) {
	if query.AuthorID == 0 {
		return nil, ErrPostValidation
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		return nil, fmt.Errorf("%w: limit 不能超过 100", ErrPostValidation)
	}
	if !query.BeforeTime.IsZero() && query.BeforeID == 0 {
		return nil, fmt.Errorf("%w: 复合游标缺少 id", ErrPostValidation)
	}
	return s.repository.ListPublicByAuthor(ctx, query)
}

func (s *Service) validatePostInput(authorID uint64, title, body string, imageIDs []uint64) error {
	if authorID == 0 {
		return fmt.Errorf("%w: author_id 必填", ErrPostValidation)
	}
	if title == "" || utf8.RuneCountInString(title) > 200 {
		return fmt.Errorf("%w: title 必填且不能超过 200 字符", ErrPostValidation)
	}
	if body == "" || utf8.RuneCountInString(body) > s.limits.MaxBodyLength {
		return fmt.Errorf("%w: body 必填且不能超过 %d 字符", ErrPostValidation, s.limits.MaxBodyLength)
	}
	return s.validateImageIDs(authorID, imageIDs)
}

func (s *Service) validateImageIDs(authorID uint64, imageIDs []uint64) error {
	if authorID == 0 {
		return fmt.Errorf("%w: author_id 必填", ErrPostValidation)
	}
	if len(imageIDs) > s.limits.MaxImages {
		return fmt.Errorf("%w: 图片不能超过 %d 张", ErrPostValidation, s.limits.MaxImages)
	}
	seen := make(map[uint64]struct{}, len(imageIDs))
	for _, id := range imageIDs {
		if id == 0 {
			return fmt.Errorf("%w: image id 必须大于 0", ErrPostValidation)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: image id 不得重复", ErrPostValidation)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *Service) replaceImages(
	ctx context.Context,
	tx *gorm.DB,
	post *Post,
	ownerID uint64,
	imageIDs []uint64,
	publish bool,
) error {
	if len(imageIDs) > 0 {
		var images []PostImage
		database := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND owner_id = ?", imageIDs, ownerID)
		if publish {
			database = database.Where("post_id IS NULL")
		} else {
			database = database.Where("post_id IS NULL OR post_id = ?", post.ID)
		}
		if err := database.Find(&images).Error; err != nil {
			return fmt.Errorf("query post images: %w", err)
		}
		if len(images) != len(imageIDs) {
			return ErrImageUnavailable
		}
	}

	if !publish {
		if err := tx.WithContext(ctx).Model(&PostImage{}).Where("post_id = ?", post.ID).Updates(map[string]any{
			"post_id":     nil,
			"sort_order":  nil,
			"attached_at": nil,
		}).Error; err != nil {
			return fmt.Errorf("detach post images: %w", err)
		}
	}
	attachedAt := s.now().UTC()
	for index, imageID := range imageIDs {
		result := tx.WithContext(ctx).Model(&PostImage{}).
			Where("id = ? AND owner_id = ? AND post_id IS NULL", imageID, ownerID).
			Updates(map[string]any{
				"post_id":     post.ID,
				"sort_order":  index,
				"attached_at": attachedAt,
			})
		if result.Error != nil {
			return fmt.Errorf("attach post image %d: %w", imageID, result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrImageUnavailable
		}
	}
	return nil
}

func (s *Service) replaceTags(ctx context.Context, tx *gorm.DB, postID uint64, tagNames []string) error {
	if err := tx.WithContext(ctx).Where("post_id = ?", postID).Delete(&PostTag{}).Error; err != nil {
		return fmt.Errorf("delete post tags: %w", err)
	}
	for _, name := range tagNames {
		candidate := &Tag{Name: name}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			DoNothing: true,
		}).Create(candidate).Error; err != nil {
			return fmt.Errorf("upsert tag %q: %w", name, err)
		}
		var persisted Tag
		if err := tx.WithContext(ctx).Where("name = ?", name).First(&persisted).Error; err != nil {
			return fmt.Errorf("load tag %q: %w", name, err)
		}
		if err := tx.WithContext(ctx).Create(&PostTag{PostID: postID, TagID: persisted.ID}).Error; err != nil {
			return fmt.Errorf("attach tag %q: %w", name, err)
		}
	}
	return nil
}

const maxPostProducts = 20

func validatePostProductIDs(productIDs []uint64) error {
	if len(productIDs) > maxPostProducts {
		return fmt.Errorf("%w: products cannot exceed %d", ErrPostValidation, maxPostProducts)
	}
	seen := make(map[uint64]struct{}, len(productIDs))
	for _, productID := range productIDs {
		if productID == 0 {
			return fmt.Errorf("%w: product id must be positive", ErrPostValidation)
		}
		if _, duplicate := seen[productID]; duplicate {
			return fmt.Errorf("%w: product id must not be duplicated", ErrPostValidation)
		}
		seen[productID] = struct{}{}
	}
	return nil
}

func (s *Service) replaceProducts(ctx context.Context, tx *gorm.DB, postID, authorID uint64, productIDs []uint64) error {
	if len(productIDs) > 0 {
		var availableIDs []uint64
		if err := tx.WithContext(ctx).Table("products AS product").
			Select("product.id").
			Joins("JOIN shops AS shop ON shop.id = product.shop_id").
			Where("product.id IN ?", productIDs).
			Where("shop.owner_id = ? AND shop.status = 'open' AND product.status = 'on_sale'", authorID).
			Where(`EXISTS (
SELECT 1 FROM product_skus AS sku
WHERE sku.product_id = product.id
  AND sku.status = 'active'
  AND sku.available_stock > 0
)`).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Scan(&availableIDs).Error; err != nil {
			return fmt.Errorf("validate post products: %w", err)
		}
		if len(availableIDs) != len(productIDs) {
			return ErrProductUnavailable
		}
	}
	if err := tx.WithContext(ctx).Where("post_id = ?", postID).Delete(&PostProduct{}).Error; err != nil {
		return fmt.Errorf("delete post products: %w", err)
	}
	if len(productIDs) == 0 {
		return nil
	}
	relations := make([]PostProduct, len(productIDs))
	for index, productID := range productIDs {
		relations[index] = PostProduct{PostID: postID, ProductID: productID, SortOrder: uint(index)}
	}
	if err := tx.WithContext(ctx).Create(&relations).Error; err != nil {
		return fmt.Errorf("attach post products: %w", err)
	}
	return nil
}

func normalizeTags(title, body string, explicit []string) ([]string, error) {
	candidates := make([]string, 0, len(explicit)+4)
	for _, match := range hashtagPattern.FindAllStringSubmatch(title+"\n"+body, -1) {
		candidates = append(candidates, match[1])
	}
	candidates = append(candidates, explicit...)

	seen := make(map[string]struct{}, len(candidates))
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "#"))
		name = strings.ToLower(name)
		if name == "" {
			continue
		}
		if utf8.RuneCountInString(name) > 50 || !tagNamePattern.MatchString(name) {
			return nil, fmt.Errorf("%w: %q", ErrTagValidation, candidate)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

func NormalizeTagName(raw string) (string, error) {
	names, err := normalizeTags("", "", []string{raw})
	if err != nil {
		return "", err
	}
	if len(names) != 1 {
		return "", ErrTagValidation
	}
	return names[0], nil
}
