package worker

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Notification struct {
	ID          uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	EventID     string    `gorm:"column:event_id" json:"event_id"`
	RecipientID uint64    `gorm:"column:recipient_id" json:"recipient_id"`
	SenderID    uint64    `gorm:"column:sender_id" json:"sender_id"`
	Type        string    `gorm:"column:type" json:"type"`
	TargetType  string    `gorm:"column:target_type" json:"target_type"`
	TargetID    uint64    `gorm:"column:target_id" json:"target_id"`
	Content     string    `gorm:"column:content" json:"content"`
	IsRead      bool      `gorm:"column:is_read" json:"is_read"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (Notification) TableName() string { return "notifications_v2" }

type NotificationWorker struct {
	ch       *amqp.Channel
	db       *gorm.DB
	queue    string
	hub      NotificationHub
	reliable *ReliableDeliveryHandler
}

type NotificationHub interface {
	Push(userID uint64, n *Notification)
}

func NewNotificationWorker(ch *amqp.Channel, db *gorm.DB, queue string, hub NotificationHub, reliable *ReliableDeliveryHandler) *NotificationWorker {
	return &NotificationWorker{ch: ch, db: db, queue: queue, hub: hub, reliable: reliable}
}

func (w *NotificationWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.db == nil || w.reliable == nil {
		return errors.New("notification worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}
	deliveries, err := w.ch.Consume(w.queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("deliveries channel closed")
			}
			w.handleDelivery(ctx, d)
		}
	}
}

func (w *NotificationWorker) handleDelivery(ctx context.Context, d amqp.Delivery) {
	_ = w.reliable.Handle(ctx, d, "handle", func(ctx context.Context, delivery amqp.Delivery) error {
		return w.process(ctx, delivery)
	})
}

func (w *NotificationWorker) process(ctx context.Context, d amqp.Delivery) error {
	body := d.Body
	if len(body) == 0 {
		return errors.New("notification event body is empty")
	}
	routingKey := d.RoutingKey

	var notif *Notification

	switch {
	case routingKey == "social.followed":
		var evt rabbitmq.SocialEvent
		if err := json.Unmarshal(body, &evt); err != nil {
			return fmt.Errorf("decode notification social event: %w", err)
		}
		if evt.FollowerID == 0 || evt.VloggerID == 0 {
			return errors.New("notification social event follower_id and vlogger_id are required")
		}
		notif = &Notification{EventID: evt.EventID, RecipientID: uint64(evt.VloggerID), SenderID: uint64(evt.FollowerID), Type: "follow", TargetType: "account", TargetID: uint64(evt.FollowerID), Content: "关注了你"}
	}

	if notif == nil {
		return nil
	}
	result := w.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(notif)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if w.hub != nil {
		w.hub.Push(notif.RecipientID, notif)
	}
	return nil
}
