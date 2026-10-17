package feed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/storefront"
)

const defaultFeedProductCardLimit = 3

type ContentFeedAuthor struct {
	ID        uint64 `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Bio       string `json:"bio,omitempty"`
}

type ContentFeedItem struct {
	ID             uint64                       `json:"id"`
	Author         ContentFeedAuthor            `json:"author"`
	Title          string                       `json:"title"`
	Body           string                       `json:"body"`
	Images         []content.PostImage          `json:"images"`
	Tags           []content.Tag                `json:"tags"`
	LikesCount     uint64                       `json:"likes_count"`
	FavoritesCount uint64                       `json:"favorites_count"`
	CommentsCount  uint64                       `json:"comments_count"`
	Popularity     float64                      `json:"popularity"`
	PublishedAt    *time.Time                   `json:"published_at,omitempty"`
	IsLiked        bool                         `json:"is_liked"`
	IsFavorited    bool                         `json:"is_favorited"`
	Shop           *storefront.FeedShopSummary  `json:"shop,omitempty"`
	Products       []storefront.FeedProductCard `json:"products"`
}

type FeedAssembler struct {
	contents     *content.Repository
	accounts     *account.AccountRepository
	interactions *interaction.Service
	commerce     *storefront.Service
}

func NewFeedAssembler(
	contents *content.Repository,
	accounts *account.AccountRepository,
	interactions *interaction.Service,
	commerce ...*storefront.Service,
) (*FeedAssembler, error) {
	if contents == nil || accounts == nil || interactions == nil {
		return nil, errors.New("feed assembler dependencies are required")
	}
	if len(commerce) > 1 {
		return nil, errors.New("feed assembler accepts at most one storefront service")
	}
	assembler := &FeedAssembler{contents: contents, accounts: accounts, interactions: interactions}
	if len(commerce) == 1 {
		assembler.commerce = commerce[0]
	}
	return assembler, nil
}

func (a *FeedAssembler) Assemble(ctx context.Context, posts []content.Post, viewerAccountID uint64) ([]ContentFeedItem, error) {
	if len(posts) == 0 {
		return []ContentFeedItem{}, nil
	}
	postIDs, authorIDs := uniqueFeedIDs(posts)
	imagesByPost, err := a.contents.ImagesByPostIDs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	tagsByPost, err := a.contents.TagsByPostIDs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	authors, err := a.accounts.FindByIDs(ctx, authorIDs)
	if err != nil {
		return nil, fmt.Errorf("batch feed authors: %w", err)
	}
	authorsByID := make(map[uint]account.Account, len(authors))
	for _, author := range authors {
		if author != nil {
			authorsByID[author.ID] = *author
		}
	}
	liked, err := a.interactions.LikeStates(ctx, viewerAccountID, postIDs)
	if err != nil {
		return nil, err
	}
	favorited, err := a.interactions.FavoriteStates(ctx, viewerAccountID, postIDs)
	if err != nil {
		return nil, err
	}
	commerceBatch := storefront.FeedCommerceBatch{
		ShopsByOwner:   make(map[uint64]*storefront.FeedShopSummary),
		ProductsByPost: make(map[uint64][]storefront.FeedProductCard),
	}
	if a.commerce != nil {
		authorIDs64 := make([]uint64, len(authorIDs))
		for index, authorID := range authorIDs {
			authorIDs64[index] = uint64(authorID)
		}
		commerceBatch, err = a.commerce.BuildFeedCommerce(ctx, postIDs, authorIDs64, defaultFeedProductCardLimit)
		if err != nil {
			return nil, err
		}
	}

	items := make([]ContentFeedItem, 0, len(posts))
	for _, post := range posts {
		author := authorsByID[uint(post.AuthorID)]
		products := commerceBatch.ProductsByPost[post.ID]
		if products == nil {
			products = []storefront.FeedProductCard{}
		}
		items = append(items, ContentFeedItem{
			ID: post.ID,
			Author: ContentFeedAuthor{
				ID: post.AuthorID, Username: author.Username, AvatarURL: author.AvatarURL, Bio: author.Bio,
			},
			Title:          post.Title,
			Body:           post.Body,
			Images:         imagesByPost[post.ID],
			Tags:           tagsByPost[post.ID],
			LikesCount:     post.LikesCount,
			FavoritesCount: post.FavoritesCount,
			CommentsCount:  post.CommentsCount,
			Popularity:     post.Popularity,
			PublishedAt:    post.PublishedAt,
			IsLiked:        liked[post.ID],
			IsFavorited:    favorited[post.ID],
			Shop:           commerceBatch.ShopsByOwner[post.AuthorID],
			Products:       products,
		})
	}
	return items, nil
}

func uniqueFeedIDs(posts []content.Post) ([]uint64, []uint) {
	postIDs := make([]uint64, 0, len(posts))
	authorIDs := make([]uint, 0, len(posts))
	seenPosts := make(map[uint64]struct{}, len(posts))
	seenAuthors := make(map[uint64]struct{}, len(posts))
	for _, post := range posts {
		if _, exists := seenPosts[post.ID]; !exists {
			seenPosts[post.ID] = struct{}{}
			postIDs = append(postIDs, post.ID)
		}
		if _, exists := seenAuthors[post.AuthorID]; !exists {
			seenAuthors[post.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, uint(post.AuthorID))
		}
	}
	return postIDs, authorIDs
}
