package profile

import (
	"context"
	"errors"
	"fmt"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/social"

	"gorm.io/gorm"
)

var ErrAuthorNotFound = errors.New("author not found")

type AuthorPost struct {
	content.Post
	IsFavorited bool `json:"is_favorited"`
}

type AuthorProfile struct {
	Author             account.FindByIDResponse `json:"author"`
	ContentCount       int64                    `json:"content_count"`
	ReceivedLikesCount uint64                   `json:"received_likes_count"`
	IsFollowing        bool                     `json:"is_following"`
	Posts              []AuthorPost             `json:"posts"`
}

type Service struct {
	accounts     *account.AccountRepository
	social       *social.SocialRepository
	content      *content.Repository
	interactions *interaction.Service
}

func NewService(
	accounts *account.AccountRepository,
	socialRepository *social.SocialRepository,
	contentRepository *content.Repository,
	interactions *interaction.Service,
) (*Service, error) {
	if accounts == nil || socialRepository == nil || contentRepository == nil || interactions == nil {
		return nil, errors.New("author profile dependencies are required")
	}
	return &Service{accounts: accounts, social: socialRepository, content: contentRepository, interactions: interactions}, nil
}

func (s *Service) Get(ctx context.Context, authorID, viewerID uint64) (*AuthorProfile, error) {
	if authorID == 0 {
		return nil, ErrAuthorNotFound
	}
	author, err := s.accounts.FindByID(ctx, uint(authorID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAuthorNotFound
	}
	if err != nil {
		return nil, err
	}
	contentCount, err := s.content.CountPublicByAuthor(ctx, authorID)
	if err != nil {
		return nil, err
	}
	receivedLikes, err := s.content.SumPublicLikesByAuthor(ctx, authorID)
	if err != nil {
		return nil, err
	}
	posts, err := s.content.ListPublicByAuthor(ctx, content.AuthorListQuery{AuthorID: authorID, Limit: 20})
	if err != nil {
		return nil, err
	}
	postIDs := make([]uint64, len(posts))
	for index := range posts {
		postIDs[index] = posts[index].ID
	}
	favoriteStates, err := s.interactions.FavoriteStates(ctx, viewerID, postIDs)
	if err != nil {
		return nil, err
	}
	isFollowing := false
	if viewerID != 0 && viewerID != authorID {
		isFollowing, err = s.social.IsFollowed(ctx, &social.Social{FollowerID: uint(viewerID), VloggerID: uint(authorID)})
		if err != nil {
			return nil, fmt.Errorf("query author follow state: %w", err)
		}
	}
	profilePosts := make([]AuthorPost, len(posts))
	for index := range posts {
		profilePosts[index] = AuthorPost{Post: posts[index], IsFavorited: favoriteStates[posts[index].ID]}
	}
	return &AuthorProfile{
		Author: account.FindByIDResponse{
			ID: author.ID, Username: author.Username, AvatarURL: author.AvatarURL, Bio: author.Bio,
		},
		ContentCount:       contentCount,
		ReceivedLikesCount: receivedLikes,
		IsFollowing:        isFollowing,
		Posts:              profilePosts,
	}, nil
}
