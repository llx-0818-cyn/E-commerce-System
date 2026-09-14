package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"feedsystem_video_go/internal/event"

	"gorm.io/gorm"
)

type ContentNotificationConsumer struct {
	db    *gorm.DB
	guard *event.ConsumerGuard
	hub   NotificationHub
}

func NewContentNotificationConsumer(db *gorm.DB, hub NotificationHub) (*ContentNotificationConsumer, error) {
	if db == nil {
		return nil, errors.New("content notification database is nil")
	}
	guard, err := event.NewConsumerGuard(db, "content-notification-consumer")
	if err != nil {
		return nil, err
	}
	return &ContentNotificationConsumer{db: db, guard: guard, hub: hub}, nil
}

func (c *ContentNotificationConsumer) Handle(ctx context.Context, eventID, eventType string, payload []byte) (bool, error) {
	if c == nil || c.guard == nil {
		return false, errors.New("content notification consumer is not initialized")
	}
	eventID = strings.TrimSpace(eventID)
	eventType = strings.TrimSpace(eventType)
	if eventID == "" || eventType == "" || !json.Valid(payload) {
		return false, errors.New("content notification event envelope is invalid")
	}
	created := make([]Notification, 0, 4)
	applied, err := c.guard.Process(ctx, event.ConsumedEvent{EventID: eventID, EventType: eventType}, func(tx *gorm.DB) error {
		notifications, err := buildContentNotifications(tx, eventID, eventType, payload)
		if err != nil {
			return err
		}
		for index := range notifications {
			if err := tx.WithContext(ctx).Create(&notifications[index]).Error; err != nil {
				return fmt.Errorf("create content notification: %w", err)
			}
		}
		created = notifications
		return nil
	})
	if err != nil {
		return false, err
	}
	if applied && c.hub != nil {
		for index := range created {
			notification := created[index]
			c.hub.Push(notification.RecipientID, &notification)
		}
	}
	return applied, nil
}

func buildContentNotifications(tx *gorm.DB, eventID, eventType string, payload []byte) ([]Notification, error) {
	switch eventType {
	case "content.liked":
		var value struct {
			PostID       uint64 `json:"post_id"`
			PostAuthorID uint64 `json:"post_author_id"`
			ActorID      uint64 `json:"actor_id"`
		}
		if err := json.Unmarshal(payload, &value); err != nil || value.PostID == 0 || value.PostAuthorID == 0 || value.ActorID == 0 {
			return nil, errors.New("content.liked payload is invalid")
		}
		if value.PostAuthorID == value.ActorID {
			return []Notification{}, nil
		}
		return []Notification{{
			EventID: eventID, RecipientID: value.PostAuthorID, SenderID: value.ActorID,
			Type: "like", TargetType: "post", TargetID: value.PostID, Content: "点赞了你的图文",
		}}, nil

	case "content.comment.created":
		var value struct {
			PostID             uint64   `json:"post_id"`
			PostAuthorID       uint64   `json:"post_author_id"`
			CommentID          uint64   `json:"comment_id"`
			ActorID            uint64   `json:"actor_id"`
			MentionedUsernames []string `json:"mentioned_usernames"`
		}
		if err := json.Unmarshal(payload, &value); err != nil || value.PostID == 0 || value.CommentID == 0 || value.PostAuthorID == 0 || value.ActorID == 0 {
			return nil, errors.New("content.comment.created payload is invalid")
		}
		recipients := make(map[uint64]Notification)
		if value.PostAuthorID != value.ActorID {
			recipients[value.PostAuthorID] = Notification{
				EventID: eventID, RecipientID: value.PostAuthorID, SenderID: value.ActorID,
				Type: "comment", TargetType: "post", TargetID: value.PostID, Content: "评论了你的图文",
			}
		}
		if len(value.MentionedUsernames) > 0 {
			names := make([]string, 0, len(value.MentionedUsernames))
			seenNames := make(map[string]struct{})
			for _, raw := range value.MentionedUsernames {
				name := strings.ToLower(strings.TrimSpace(raw))
				if name == "" {
					continue
				}
				if _, duplicate := seenNames[name]; duplicate {
					continue
				}
				seenNames[name] = struct{}{}
				names = append(names, name)
			}
			var accounts []struct {
				ID       uint64
				Username string
			}
			if len(names) > 0 {
				if err := tx.Table("accounts").Select("id, username").Where("LOWER(username) IN ?", names).Find(&accounts).Error; err != nil {
					return nil, fmt.Errorf("resolve mentioned accounts: %w", err)
				}
			}
			for _, account := range accounts {
				if account.ID == 0 || account.ID == value.ActorID {
					continue
				}
				if _, already := recipients[account.ID]; already {
					continue
				}
				recipients[account.ID] = Notification{
					EventID: eventID, RecipientID: account.ID, SenderID: value.ActorID,
					Type: "mention", TargetType: "post", TargetID: value.PostID, Content: "在评论中提到了你",
				}
			}
		}
		result := make([]Notification, 0, len(recipients))
		for _, notification := range recipients {
			result = append(result, notification)
		}
		return result, nil

	case "social.followed":
		var value struct {
			FollowerID uint64 `json:"follower_id"`
			VloggerID  uint64 `json:"vlogger_id"`
		}
		if err := json.Unmarshal(payload, &value); err != nil || value.FollowerID == 0 || value.VloggerID == 0 {
			return nil, errors.New("social.followed payload is invalid")
		}
		if value.FollowerID == value.VloggerID {
			return []Notification{}, nil
		}
		return []Notification{{
			EventID: eventID, RecipientID: value.VloggerID, SenderID: value.FollowerID,
			Type: "follow", TargetType: "account", TargetID: value.FollowerID, Content: "关注了你",
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported notification event type %q", eventType)
	}
}

func ListNotifications(ctx context.Context, db *gorm.DB, recipientID uint64, limit int) ([]Notification, error) {
	if db == nil || recipientID == 0 {
		return nil, errors.New("notification list database and recipient are required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return nil, errors.New("notification list limit cannot exceed 200")
	}
	var notifications []Notification
	if err := db.WithContext(ctx).Where("recipient_id = ?", recipientID).Order("created_at DESC, id DESC").Limit(limit).Find(&notifications).Error; err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	if notifications == nil {
		notifications = []Notification{}
	}
	return notifications, nil
}
