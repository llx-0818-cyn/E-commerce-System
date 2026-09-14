package profile

import (
	"errors"
	"net/http"
	"strconv"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("author profile service is nil")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) Get(c *gin.Context) {
	authorID, err := strconv.ParseUint(c.Param("author_id"), 10, 64)
	if err != nil || authorID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid author_id"})
		return
	}
	viewerID := uint64(0)
	if accountID, err := jwt.GetAccountID(c); err == nil {
		viewerID = uint64(accountID)
	}
	profile, err := h.service.Get(c.Request.Context(), authorID, viewerID)
	if err != nil {
		if errors.Is(err, ErrAuthorNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, profile)
}
