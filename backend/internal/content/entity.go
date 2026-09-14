package content

import "time"

type PostStatus string

const (
	PostStatusDraft     PostStatus = "draft"
	PostStatusReview    PostStatus = "review"
	PostStatusPublished PostStatus = "published"
	PostStatusHidden    PostStatus = "hidden"
	PostStatusDeleted   PostStatus = "deleted"
)

type Post struct {
	ID             uint64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	AuthorID       uint64      `gorm:"column:author_id" json:"author_id"`
	Title          string      `gorm:"column:title" json:"title"`
	Body           string      `gorm:"column:body" json:"body"`
	Status         PostStatus  `gorm:"column:status" json:"status"`
	LikesCount     uint64      `gorm:"column:likes_count" json:"likes_count"`
	FavoritesCount uint64      `gorm:"column:favorites_count" json:"favorites_count"`
	CommentsCount  uint64      `gorm:"column:comments_count" json:"comments_count"`
	Popularity     float64     `gorm:"column:popularity" json:"popularity"`
	PublishedAt    *time.Time  `gorm:"column:published_at" json:"published_at,omitempty"`
	CreatedAt      time.Time   `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time   `gorm:"column:updated_at" json:"updated_at"`
	DeletedAt      *time.Time  `gorm:"column:deleted_at" json:"-"`
	Images         []PostImage `gorm:"foreignKey:PostID" json:"images"`
	Tags           []Tag       `gorm:"many2many:post_tags;joinForeignKey:PostID;joinReferences:TagID" json:"tags"`
	ProductIDs     []uint64    `gorm:"-" json:"product_ids"`
}

func (Post) TableName() string { return "posts" }

type PostImage struct {
	ID         uint64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	OwnerID    uint64     `gorm:"column:owner_id" json:"-"`
	PostID     *uint64    `gorm:"column:post_id" json:"-"`
	StorageKey string     `gorm:"column:storage_key" json:"-"`
	ImageURL   string     `gorm:"column:image_url" json:"image_url"`
	MIMEType   string     `gorm:"column:mime_type" json:"mime_type"`
	ByteSize   uint64     `gorm:"column:byte_size" json:"byte_size"`
	Width      uint       `gorm:"column:width" json:"width"`
	Height     uint       `gorm:"column:height" json:"height"`
	SortOrder  *uint      `gorm:"column:sort_order" json:"sort_order"`
	CreatedAt  time.Time  `gorm:"column:created_at" json:"created_at"`
	AttachedAt *time.Time `gorm:"column:attached_at" json:"-"`
}

func (PostImage) TableName() string { return "post_images" }

type Tag struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name string `gorm:"column:name" json:"name"`
}

func (Tag) TableName() string { return "tags" }

type PostTag struct {
	PostID    uint64    `gorm:"column:post_id;primaryKey"`
	TagID     uint64    `gorm:"column:tag_id;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (PostTag) TableName() string { return "post_tags" }

type PostProduct struct {
	PostID    uint64    `gorm:"column:post_id;primaryKey"`
	ProductID uint64    `gorm:"column:product_id;primaryKey"`
	SortOrder uint      `gorm:"column:sort_order"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (PostProduct) TableName() string { return "post_products" }

type PublishInput struct {
	AuthorID   uint64
	Title      string
	Body       string
	ImageIDs   []uint64
	Tags       []string
	ProductIDs []uint64
}

type UpdateInput struct {
	AuthorID   uint64
	PostID     uint64
	Title      string
	Body       string
	ImageIDs   []uint64
	Tags       []string
	ProductIDs []uint64
}

type AuthorListQuery struct {
	AuthorID   uint64
	BeforeTime time.Time
	BeforeID   uint64
	Limit      int
}

type PublishRequest struct {
	Title      string   `json:"title" binding:"required"`
	Body       string   `json:"body" binding:"required"`
	ImageIDs   []uint64 `json:"image_ids"`
	Tags       []string `json:"tags"`
	ProductIDs []uint64 `json:"product_ids"`
}

type UpdateRequest struct {
	Title      string   `json:"title" binding:"required"`
	Body       string   `json:"body" binding:"required"`
	ImageIDs   []uint64 `json:"image_ids"`
	Tags       []string `json:"tags"`
	ProductIDs []uint64 `json:"product_ids"`
}

type ReorderImagesRequest struct {
	ImageIDs []uint64 `json:"image_ids" binding:"required"`
}

type AuthorListResponse struct {
	Posts          []Post     `json:"posts"`
	NextBeforeTime *time.Time `json:"next_before_time,omitempty"`
	NextBeforeID   uint64     `json:"next_before_id,omitempty"`
	HasMore        bool       `json:"has_more"`
}
