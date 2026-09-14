package interaction

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type APIService interface {
	SetLike(context.Context, uint64, uint64, bool) (StateResult, error)
	SetFavorite(context.Context, uint64, uint64, bool) (StateResult, error)
	LikeStates(context.Context, uint64, []uint64) (map[uint64]bool, error)
	FavoriteStates(context.Context, uint64, []uint64) (map[uint64]bool, error)
	ListFavorites(context.Context, FavoriteListQuery) (FavoriteListResult, error)
	PublishComment(context.Context, uint64, uint64, string) (*PostComment, error)
	DeleteComment(context.Context, uint64, uint64) error
	ListComments(context.Context, CommentListQuery) (CommentListResult, error)
}

type Handler struct{ service APIService }

func NewHandler(service APIService) (*Handler, error) {
	if service == nil {
		return nil, errors.New("interaction handler service is nil")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) SetLike(c *gin.Context)     { h.setState(c, h.service.SetLike) }
func (h *Handler) SetFavorite(c *gin.Context) { h.setState(c, h.service.SetFavorite) }

func (h *Handler) setState(c *gin.Context, setter func(context.Context, uint64, uint64, bool) (StateResult, error)) {
	accountID, ok := interactionActorID(c)
	if !ok {
		return
	}
	postID, ok := interactionPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Active *bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Active == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active is required"})
		return
	}
	result, err := setter(c.Request.Context(), accountID, postID, *request.Active)
	if err != nil {
		writeInteractionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) State(c *gin.Context) {
	accountID, ok := interactionActorID(c)
	if !ok {
		return
	}
	postID, ok := interactionPathID(c, "id")
	if !ok {
		return
	}
	liked, err := h.service.LikeStates(c.Request.Context(), accountID, []uint64{postID})
	if err != nil {
		writeInteractionError(c, err)
		return
	}
	favorited, err := h.service.FavoriteStates(c.Request.Context(), accountID, []uint64{postID})
	if err != nil {
		writeInteractionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"is_liked": liked[postID], "is_favorited": favorited[postID]})
}

func (h *Handler) Favorites(c *gin.Context) {
	accountID, ok := interactionActorID(c)
	if !ok {
		return
	}
	limit, beforeTime, beforeID, ok := interactionPageQuery(c, 100)
	if !ok {
		return
	}
	result, err := h.service.ListFavorites(c.Request.Context(), FavoriteListQuery{AccountID: accountID, Limit: limit, BeforeTime: beforeTime, BeforeID: beforeID})
	if err != nil {
		writeInteractionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) ListComments(c *gin.Context) {
	postID, ok := interactionPathID(c, "id")
	if !ok {
		return
	}
	limit, beforeTime, beforeID, ok := interactionPageQuery(c, 200)
	if !ok {
		return
	}
	result, err := h.service.ListComments(c.Request.Context(), CommentListQuery{PostID: postID, Limit: limit, BeforeTime: beforeTime, BeforeID: beforeID})
	if err != nil {
		writeInteractionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) PublishComment(c *gin.Context) {
	authorID, ok := interactionActorID(c)
	if !ok {
		return
	}
	postID, ok := interactionPathID(c, "id")
	if !ok {
		return
	}
	var request struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON request body"})
		return
	}
	comment, err := h.service.PublishComment(c.Request.Context(), authorID, postID, request.Content)
	if err != nil {
		writeInteractionError(c, err)
		return
	}
	c.JSON(http.StatusCreated, comment)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	authorID, ok := interactionActorID(c)
	if !ok {
		return
	}
	commentID, ok := interactionPathID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteComment(c.Request.Context(), authorID, commentID); err != nil {
		writeInteractionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "comment deleted"})
}

func interactionActorID(c *gin.Context) (uint64, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uint64(accountID), true
}

func interactionPathID(c *gin.Context, name string) (uint64, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || value == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid resource id"})
		return 0, false
	}
	return value, true
}

func interactionPageQuery(c *gin.Context, maximum int) (int, time.Time, uint64, bool) {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maximum {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return 0, time.Time{}, 0, false
		}
		limit = value
	}
	timeRaw, idRaw := strings.TrimSpace(c.Query("before_time")), strings.TrimSpace(c.Query("before_id"))
	if timeRaw == "" && idRaw == "" {
		return limit, time.Time{}, 0, true
	}
	if timeRaw == "" || idRaw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time and before_id must be provided together"})
		return 0, time.Time{}, 0, false
	}
	parsedTime, timeErr := time.Parse(time.RFC3339Nano, timeRaw)
	parsedID, idErr := strconv.ParseUint(idRaw, 10, 64)
	if timeErr != nil || idErr != nil || parsedID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
		return 0, time.Time{}, 0, false
	}
	return limit, parsedTime.UTC(), parsedID, true
}

func writeInteractionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInteractionValidation), errors.Is(err, ErrCommentValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, content.ErrPostNotFound), errors.Is(err, ErrCommentNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrCommentForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "interaction operation failed"})
	}
}
