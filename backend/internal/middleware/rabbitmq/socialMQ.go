package rabbitmq

import (
	"context"
	"errors"
	"time"
)

type SocialMQ struct {
	publisher *ConfirmPublisher
}

const (
	socialFollowRK   = "social.followed"
	socialUnfollowRK = "social.unfollowed"
)

type SocialEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	FollowerID uint      `json:"follower_id"`
	VloggerID  uint      `json:"vlogger_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func NewSocialMQ(base *RabbitMQ) (*SocialMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	ch, err := base.NewChannel()
	if err != nil {
		return nil, err
	}
	if err := DeclareTopic(ch, SocialExchange, SocialQueue, SocialBindingKey); err != nil {
		ch.Close()
		return nil, err
	}
	publisher, err := NewConfirmPublisher(ch, defaultPublisherConfirmTimeout)
	if err != nil {
		ch.Close()
		return nil, err
	}
	return &SocialMQ{publisher: publisher}, nil
}

func (s *SocialMQ) Follow(ctx context.Context, followerID, vloggerID uint) error {
	return s.publish(ctx, "follow", socialFollowRK, followerID, vloggerID)
}

func (s *SocialMQ) UnFollow(ctx context.Context, followerID, vloggerID uint) error {
	return s.publish(ctx, "unfollow", socialUnfollowRK, followerID, vloggerID)
}

func (s *SocialMQ) publish(ctx context.Context, action, routingKey string, followerID, vloggerID uint) error {
	if s == nil || s.publisher == nil {
		return errors.New("social mq is not initialized")
	}
	if followerID == 0 || vloggerID == 0 {
		return errors.New("followerID and vloggerID are required")
	}
	id, err := newEventID(16)
	if err != nil {
		return err
	}
	evt := SocialEvent{
		EventID:    id,
		Action:     action,
		FollowerID: followerID,
		VloggerID:  vloggerID,
		OccurredAt: time.Now().UTC(),
	}
	return PublishJSON(ctx, s.publisher, SocialExchange, routingKey, evt)
}
