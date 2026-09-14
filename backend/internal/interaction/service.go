package interaction

import (
	"context"
	"errors"
	"fmt"

	"feedsystem_video_go/internal/content"
	domainevent "feedsystem_video_go/internal/event"

	"gorm.io/gorm"
)

var ErrInteractionValidation = errors.New("invalid interaction request")

type Service struct {
	repository *Repository
	events     *domainevent.Repository
}

func NewService(repository *Repository) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("interaction service repository is nil")
	}
	events, err := domainevent.NewRepository(repository.db)
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository, events: events}, nil
}

func (s *Service) SetLike(ctx context.Context, accountID, postID uint64, target bool) (StateResult, error) {
	return s.setRelation(ctx, accountID, postID, target, "post_likes", "likes_count")
}

func (s *Service) SetFavorite(ctx context.Context, accountID, postID uint64, target bool) (StateResult, error) {
	return s.setRelation(ctx, accountID, postID, target, "post_favorites", "favorites_count")
}

func (s *Service) setRelation(ctx context.Context, accountID, postID uint64, target bool, table, countColumn string) (StateResult, error) {
	if accountID == 0 || postID == 0 {
		return StateResult{}, ErrInteractionValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := StateResult{Active: target}
	err := s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		post, err := lockPublicPost(ctx, tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return content.ErrPostNotFound
		}
		if err != nil {
			return err
		}
		var relationCount int64
		if err := tx.WithContext(ctx).Table(table).Where("post_id = ? AND account_id = ?", postID, accountID).Count(&relationCount).Error; err != nil {
			return fmt.Errorf("query %s state: %w", table, err)
		}
		active := relationCount == 1
		if active != target {
			if target {
				if err := tx.WithContext(ctx).Table(table).Create(map[string]any{"post_id": postID, "account_id": accountID}).Error; err != nil {
					return fmt.Errorf("create %s relation: %w", table, err)
				}
				if err := tx.WithContext(ctx).Model(&content.Post{}).Where("id = ?", postID).
					UpdateColumn(countColumn, gorm.Expr(countColumn+" + 1")).Error; err != nil {
					return fmt.Errorf("increment %s: %w", countColumn, err)
				}
				if table == "post_likes" {
					if _, err := outbox.Append(ctx, domainevent.DomainEvent{
						AggregateType: "post",
						AggregateID:   fmt.Sprintf("%d", postID),
						EventType:     "content.liked",
						Payload: map[string]any{
							"post_id":        postID,
							"post_author_id": post.AuthorID,
							"actor_id":       accountID,
						},
					}); err != nil {
						return err
					}
				} else if table == "post_favorites" {
					if _, err := outbox.Append(ctx, domainevent.DomainEvent{
						AggregateType: "post",
						AggregateID:   fmt.Sprintf("%d", postID),
						EventType:     "content.favorited",
						Payload: map[string]any{
							"post_id":        postID,
							"post_author_id": post.AuthorID,
							"actor_id":       accountID,
						},
					}); err != nil {
						return err
					}
				}
			} else {
				if err := tx.WithContext(ctx).Exec("DELETE FROM "+table+" WHERE post_id = ? AND account_id = ?", postID, accountID).Error; err != nil {
					return fmt.Errorf("delete %s relation: %w", table, err)
				}
				expression := fmt.Sprintf("IF(%s > 0, %s - 1, 0)", countColumn, countColumn)
				if err := tx.WithContext(ctx).Model(&content.Post{}).Where("id = ?", postID).
					UpdateColumn(countColumn, gorm.Expr(expression)).Error; err != nil {
					return fmt.Errorf("decrement %s: %w", countColumn, err)
				}
			}
		}
		if err := tx.WithContext(ctx).Model(&content.Post{}).Where("id = ?", post.ID).Select(countColumn).Scan(&result.Count).Error; err != nil {
			return fmt.Errorf("load %s: %w", countColumn, err)
		}
		return nil
	})
	if err != nil {
		return StateResult{}, err
	}
	return result, nil
}

func (s *Service) ListFavorites(ctx context.Context, query FavoriteListQuery) (FavoriteListResult, error) {
	if query.AccountID == 0 {
		return FavoriteListResult{}, ErrInteractionValidation
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		return FavoriteListResult{}, fmt.Errorf("%w: limit 不能超过 100", ErrInteractionValidation)
	}
	if !query.BeforeTime.IsZero() && query.BeforeID == 0 {
		return FavoriteListResult{}, fmt.Errorf("%w: 复合游标缺少 post_id", ErrInteractionValidation)
	}
	requested := query.Limit
	query.Limit++
	posts, rows, err := s.repository.listFavorites(ctx, query)
	if err != nil {
		return FavoriteListResult{}, err
	}
	result := FavoriteListResult{Posts: posts, HasMore: len(rows) > requested}
	if result.HasMore {
		result.Posts = result.Posts[:requested]
		rows = rows[:requested]
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		result.NextBeforeTime = &last.CreatedAt
		result.NextBeforeID = last.PostID
	}
	return result, nil
}

func (s *Service) FavoriteStates(ctx context.Context, accountID uint64, postIDs []uint64) (map[uint64]bool, error) {
	states := make(map[uint64]bool, len(postIDs))
	for _, postID := range postIDs {
		states[postID] = false
	}
	if accountID == 0 || len(postIDs) == 0 {
		return states, nil
	}
	var favorites []PostFavorite
	if err := s.repository.db.WithContext(ctx).
		Where("account_id = ? AND post_id IN ?", accountID, postIDs).
		Find(&favorites).Error; err != nil {
		return nil, fmt.Errorf("batch favorite states: %w", err)
	}
	for _, favorite := range favorites {
		states[favorite.PostID] = true
	}
	return states, nil
}

func (s *Service) LikeStates(ctx context.Context, accountID uint64, postIDs []uint64) (map[uint64]bool, error) {
	states := make(map[uint64]bool, len(postIDs))
	for _, postID := range postIDs {
		states[postID] = false
	}
	if accountID == 0 || len(postIDs) == 0 {
		return states, nil
	}
	var likes []PostLike
	if err := s.repository.db.WithContext(ctx).
		Where("account_id = ? AND post_id IN ?", accountID, postIDs).
		Find(&likes).Error; err != nil {
		return nil, fmt.Errorf("batch like states: %w", err)
	}
	for _, like := range likes {
		states[like.PostID] = true
	}
	return states, nil
}
