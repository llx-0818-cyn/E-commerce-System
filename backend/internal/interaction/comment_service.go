package interaction

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"feedsystem_video_go/internal/content"
	domainevent "feedsystem_video_go/internal/event"

	"gorm.io/gorm"
)

var (
	ErrCommentValidation = errors.New("invalid post comment")
	ErrCommentNotFound   = errors.New("post comment not found")
	ErrCommentForbidden  = errors.New("post comment operation forbidden")
)

var mentionPattern = regexp.MustCompile(`@([\p{L}\p{N}_-]+)`)

func (s *Service) PublishComment(ctx context.Context, authorID, postID uint64, body string) (*PostComment, error) {
	body = strings.TrimSpace(body)
	if authorID == 0 || postID == 0 || body == "" || utf8.RuneCountInString(body) > 2000 {
		return nil, ErrCommentValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mentions := ParseMentions(body)
	var commentID uint64
	err := s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		post, err := lockPublicPost(ctx, tx, postID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return content.ErrPostNotFound
		} else if err != nil {
			return err
		}
		comment := &PostComment{PostID: postID, AuthorID: authorID, Content: body, Status: "active"}
		if err := tx.WithContext(ctx).Create(comment).Error; err != nil {
			return fmt.Errorf("create post comment: %w", err)
		}
		commentID = comment.ID
		if err := tx.WithContext(ctx).Model(&content.Post{}).Where("id = ?", postID).
			UpdateColumn("comments_count", gorm.Expr("comments_count + 1")).Error; err != nil {
			return fmt.Errorf("increment post comments_count: %w", err)
		}
		if _, err := outbox.Append(ctx, domainevent.DomainEvent{
			AggregateType: "post",
			AggregateID:   fmt.Sprintf("%d", postID),
			EventType:     "content.comment.created",
			Payload: map[string]any{
				"post_id":             postID,
				"post_author_id":      post.AuthorID,
				"comment_id":          comment.ID,
				"actor_id":            authorID,
				"content":             body,
				"mentioned_usernames": mentions,
			},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var comment PostComment
	if err := s.repository.db.WithContext(ctx).Where("id = ?", commentID).First(&comment).Error; err != nil {
		return nil, err
	}
	comment.MentionedUsernames = mentions
	return &comment, nil
}

func (s *Service) DeleteComment(ctx context.Context, authorID, commentID uint64) error {
	if authorID == 0 || commentID == 0 {
		return ErrCommentValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.repository.transaction(ctx, func(tx *gorm.DB) error {
		comment, err := s.repository.lockComment(ctx, tx, commentID)
		if errors.Is(err, gorm.ErrRecordNotFound) || comment.Status == "deleted" {
			return ErrCommentNotFound
		}
		if err != nil {
			return err
		}
		if comment.AuthorID != authorID {
			return ErrCommentForbidden
		}
		if _, err := lockPublicPost(ctx, tx, comment.PostID); errors.Is(err, gorm.ErrRecordNotFound) {
			return content.ErrPostNotFound
		} else if err != nil {
			return err
		}
		now := time.Now().UTC()
		result := tx.WithContext(ctx).Model(&PostComment{}).
			Where("id = ? AND status = 'active'", comment.ID).
			Updates(map[string]any{"status": "deleted", "deleted_at": now})
		if result.Error != nil {
			return fmt.Errorf("soft delete post comment: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrCommentNotFound
		}
		if err := tx.WithContext(ctx).Model(&content.Post{}).Where("id = ?", comment.PostID).
			UpdateColumn("comments_count", gorm.Expr("IF(comments_count > 0, comments_count - 1, 0)")).Error; err != nil {
			return fmt.Errorf("decrement post comments_count: %w", err)
		}
		return nil
	})
}

func (s *Service) ListComments(ctx context.Context, query CommentListQuery) (CommentListResult, error) {
	if query.PostID == 0 {
		return CommentListResult{}, ErrCommentValidation
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		return CommentListResult{}, fmt.Errorf("%w: limit 不能超过 200", ErrCommentValidation)
	}
	if !query.BeforeTime.IsZero() && query.BeforeID == 0 {
		return CommentListResult{}, fmt.Errorf("%w: 复合游标缺少 comment id", ErrCommentValidation)
	}
	var publicCount int64
	if err := s.repository.db.WithContext(ctx).Model(&content.Post{}).
		Where("id = ? AND status = ? AND deleted_at IS NULL", query.PostID, content.PostStatusPublished).
		Count(&publicCount).Error; err != nil {
		return CommentListResult{}, err
	}
	if publicCount != 1 {
		return CommentListResult{}, content.ErrPostNotFound
	}
	requested := query.Limit
	query.Limit++
	comments, err := s.repository.listComments(ctx, query)
	if err != nil {
		return CommentListResult{}, err
	}
	result := CommentListResult{Comments: comments, HasMore: len(comments) > requested}
	if result.HasMore {
		result.Comments = result.Comments[:requested]
	}
	if len(result.Comments) > 0 {
		last := result.Comments[len(result.Comments)-1]
		result.NextBeforeTime = &last.CreatedAt
		result.NextBeforeID = last.ID
	}
	return result, nil
}

func ParseMentions(body string) []string {
	seen := make(map[string]struct{})
	mentions := make([]string, 0)
	for _, match := range mentionPattern.FindAllStringSubmatch(body, -1) {
		username := strings.ToLower(strings.TrimSpace(match[1]))
		if username == "" {
			continue
		}
		if _, duplicate := seen[username]; duplicate {
			continue
		}
		seen[username] = struct{}{}
		mentions = append(mentions, username)
	}
	return mentions
}
