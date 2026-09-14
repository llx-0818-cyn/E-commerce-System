package interaction

import (
	"time"

	"feedsystem_video_go/internal/content"
)

type PostLike struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	PostID    uint64    `gorm:"column:post_id"`
	AccountID uint64    `gorm:"column:account_id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (PostLike) TableName() string { return "post_likes" }

type PostFavorite struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	PostID    uint64    `gorm:"column:post_id"`
	AccountID uint64    `gorm:"column:account_id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (PostFavorite) TableName() string { return "post_favorites" }

type PostComment struct {
	ID                 uint64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	PostID             uint64     `gorm:"column:post_id" json:"post_id"`
	AuthorID           uint64     `gorm:"column:author_id" json:"author_id"`
	Content            string     `gorm:"column:content" json:"content"`
	Status             string     `gorm:"column:status" json:"status"`
	CreatedAt          time.Time  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
	DeletedAt          *time.Time `gorm:"column:deleted_at" json:"-"`
	MentionedUsernames []string   `gorm:"-" json:"mentioned_usernames"`
}

func (PostComment) TableName() string { return "post_comments" }

type StateResult struct {
	Active bool   `json:"active"`
	Count  uint64 `json:"count"`
}

type FavoriteListQuery struct {
	AccountID  uint64
	BeforeTime time.Time
	BeforeID   uint64
	Limit      int
}

type FavoriteListResult struct {
	Posts          []content.Post `json:"posts"`
	NextBeforeTime *time.Time     `json:"next_before_time,omitempty"`
	NextBeforeID   uint64         `json:"next_before_id,omitempty"`
	HasMore        bool           `json:"has_more"`
}

type CommentListQuery struct {
	PostID     uint64
	BeforeTime time.Time
	BeforeID   uint64
	Limit      int
}

type CommentListResult struct {
	Comments       []PostComment `json:"comments"`
	NextBeforeTime *time.Time    `json:"next_before_time,omitempty"`
	NextBeforeID   uint64        `json:"next_before_id,omitempty"`
	HasMore        bool          `json:"has_more"`
}
