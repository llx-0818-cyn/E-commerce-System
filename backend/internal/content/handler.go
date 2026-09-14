package content

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	posts    *Service
	images   *ImageService
	behavior ContentClickRecorder
}

type ContentClickRecorder interface {
	RecordContentClick(context.Context, uint64, uint64) error
}

func NewHandler(posts *Service, images *ImageService, behavior ...ContentClickRecorder) (*Handler, error) {
	if posts == nil || images == nil {
		return nil, errors.New("content handler services are required")
	}
	if len(behavior) > 1 {
		return nil, errors.New("content handler accepts at most one behavior recorder")
	}
	handler := &Handler{posts: posts, images: images}
	if len(behavior) == 1 {
		handler.behavior = behavior[0]
	}
	return handler, nil
}

func (h *Handler) Publish(c *gin.Context) {
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	var request PublishRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	post, err := h.posts.Publish(c.Request.Context(), PublishInput{
		AuthorID:   authorID,
		Title:      request.Title,
		Body:       request.Body,
		ImageIDs:   request.ImageIDs,
		Tags:       request.Tags,
		ProductIDs: request.ProductIDs,
	})
	if err != nil {
		writeContentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, post)
}

func (h *Handler) Detail(c *gin.Context) {
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	post, err := h.posts.Detail(c.Request.Context(), postID)
	if err != nil {
		writeContentError(c, err)
		return
	}
	if h.behavior != nil {
		viewerID, _ := jwt.GetAccountID(c)
		if err := h.behavior.RecordContentClick(c.Request.Context(), uint64(viewerID), post.ID); err != nil {
			log.Printf("record content click failed: post_id=%d err=%v", post.ID, err)
		}
	}
	c.JSON(http.StatusOK, post)
}

func (h *Handler) Update(c *gin.Context) {
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var request UpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	post, err := h.posts.Update(c.Request.Context(), UpdateInput{
		AuthorID:   authorID,
		PostID:     postID,
		Title:      request.Title,
		Body:       request.Body,
		ImageIDs:   request.ImageIDs,
		Tags:       request.Tags,
		ProductIDs: request.ProductIDs,
	})
	if err != nil {
		writeContentError(c, err)
		return
	}
	c.JSON(http.StatusOK, post)
}

func (h *Handler) Delete(c *gin.Context) {
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.posts.Delete(c.Request.Context(), authorID, postID); err != nil {
		writeContentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "post deleted"})
}

func (h *Handler) ListByAuthor(c *gin.Context) {
	authorID, ok := pathID(c, "author_id")
	if !ok {
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 50 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be in 1..50"})
			return
		}
		limit = parsed
	}
	var beforeTime time.Time
	var beforeID uint64
	if raw := c.Query("before_time"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid before_time"})
			return
		}
		beforeTime = parsed
		beforeID, ok = queryUint64(c, "before_id", true)
		if !ok {
			return
		}
	} else if c.Query("before_id") != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time and before_id must be provided together"})
		return
	}
	posts, err := h.posts.ListByAuthor(c.Request.Context(), AuthorListQuery{
		AuthorID:   authorID,
		BeforeTime: beforeTime,
		BeforeID:   beforeID,
		Limit:      limit + 1,
	})
	if err != nil {
		writeContentError(c, err)
		return
	}
	response := AuthorListResponse{Posts: posts, HasMore: len(posts) > limit}
	if response.HasMore {
		response.Posts = response.Posts[:limit]
	}
	if len(response.Posts) > 0 {
		last := response.Posts[len(response.Posts)-1]
		response.NextBeforeTime = last.PublishedAt
		response.NextBeforeID = last.ID
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) UploadImage(c *gin.Context) {
	ownerID, ok := currentAccountID(c)
	if !ok {
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing image file"})
		return
	}
	file, err := header.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read image file"})
		return
	}
	defer file.Close()
	image, err := h.images.Upload(c.Request.Context(), ownerID, header.Filename, file)
	if err != nil {
		writeContentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, image)
}

func (h *Handler) DeleteImage(c *gin.Context) {
	ownerID, ok := currentAccountID(c)
	if !ok {
		return
	}
	imageID, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.images.DeleteUnused(c.Request.Context(), ownerID, imageID); err != nil {
		writeContentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "image deleted"})
}

func (h *Handler) ReorderImages(c *gin.Context) {
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	postID, ok := pathID(c, "id")
	if !ok {
		return
	}
	var request ReorderImagesRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	post, err := h.posts.ReorderImages(c.Request.Context(), authorID, postID, request.ImageIDs)
	if err != nil {
		writeContentError(c, err)
		return
	}
	c.JSON(http.StatusOK, post)
}

func currentAccountID(c *gin.Context) (uint64, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uint64(accountID), true
}

func pathID(c *gin.Context, name string) (uint64, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || value == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + name})
		return 0, false
	}
	return value, true
}

func queryUint64(c *gin.Context, name string, required bool) (uint64, bool) {
	raw := c.Query(name)
	if raw == "" && !required {
		return 0, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + name})
		return 0, false
	}
	return value, true
}

func writeContentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPostValidation), errors.Is(err, ErrTagValidation), errors.Is(err, ErrCorruptImage), errors.Is(err, ErrUnsafeStorageKey):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrImageTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	case errors.Is(err, ErrPostForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, ErrPostNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrImageUnavailable), errors.Is(err, ErrImageInUse), errors.Is(err, ErrProductUnavailable):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
