package feed

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

type ContentFeedPageResponse struct {
	Items      []ContentFeedItem `json:"items"`
	NextCursor *PostCursor       `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type ContentPopularityFeedResponse struct {
	Items              []ContentFeedItem `json:"items"`
	SnapshotAt         *time.Time        `json:"snapshot_at,omitempty"`
	NextOffset         int               `json:"next_offset"`
	NextFallbackCursor *PopularityCursor `json:"next_fallback_cursor,omitempty"`
	HasMore            bool              `json:"has_more"`
	Degraded           bool              `json:"degraded"`
}

type ContentFeedHandler struct {
	latest     *ContentLatestService
	following  *ContentFollowingService
	popularity *ContentPopularityService
	tags       *ContentTagService
	assembler  *FeedAssembler
	behavior   ContentExposureRecorder
}

type ContentExposureRecorder interface {
	RecordContentExposures(context.Context, uint64, []uint64) error
}

func NewContentFeedHandler(
	latest *ContentLatestService,
	following *ContentFollowingService,
	popularity *ContentPopularityService,
	tags *ContentTagService,
	assembler *FeedAssembler,
	behavior ...ContentExposureRecorder,
) (*ContentFeedHandler, error) {
	if latest == nil || following == nil || popularity == nil || tags == nil || assembler == nil {
		return nil, errors.New("content feed handler dependencies are required")
	}
	if len(behavior) > 1 {
		return nil, errors.New("content feed handler accepts at most one behavior recorder")
	}
	handler := &ContentFeedHandler{latest: latest, following: following, popularity: popularity, tags: tags, assembler: assembler}
	if len(behavior) == 1 {
		handler.behavior = behavior[0]
	}
	return handler, nil
}

func (h *ContentFeedHandler) Latest(c *gin.Context) {
	limit, cursor, ok := parseContentPageQuery(c)
	if !ok {
		return
	}
	page, err := h.latest.ListLatest(c.Request.Context(), limit, cursor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.respondPostPage(c, page, optionalViewerID(c))
}

func (h *ContentFeedHandler) Following(c *gin.Context) {
	viewerID, err := jwt.GetAccountID(c)
	if err != nil || viewerID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	limit, cursor, ok := parseContentPageQuery(c)
	if !ok {
		return
	}
	page, err := h.following.ListFollowing(c.Request.Context(), uint64(viewerID), limit, cursor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.respondPostPage(c, page, uint64(viewerID))
}

func (h *ContentFeedHandler) Popular(c *gin.Context) {
	limit, ok := parseContentLimit(c)
	if !ok {
		return
	}
	offset, err := parseNonNegativeInt(c.Query("offset"), 0)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be a non-negative integer"})
		return
	}
	var snapshotAt time.Time
	if raw := strings.TrimSpace(c.Query("snapshot_at")); raw != "" {
		snapshotAt, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "snapshot_at must be RFC3339"})
			return
		}
	}
	fallbackCursor, ok := parsePopularityFallbackCursor(c)
	if !ok {
		return
	}
	page, err := h.popularity.ListPopular(c.Request.Context(), limit, snapshotAt, offset, fallbackCursor)
	if errors.Is(err, ErrContentPopularitySnapshotExpired) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items, err := h.assembler.Assemble(c.Request.Context(), page.Posts, optionalViewerID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	viewerID := optionalViewerID(c)
	h.recordExposures(c.Request.Context(), viewerID, items)
	c.JSON(http.StatusOK, ContentPopularityFeedResponse{
		Items: items, SnapshotAt: page.SnapshotAt, NextOffset: page.NextOffset,
		NextFallbackCursor: page.NextFallbackCursor, HasMore: page.HasMore, Degraded: page.Degraded,
	})
}

func (h *ContentFeedHandler) Tag(c *gin.Context) {
	limit, cursor, ok := parseContentPageQuery(c)
	if !ok {
		return
	}
	page, err := h.tags.ListByTag(c.Request.Context(), c.Param("tag"), limit, cursor)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.respondPostPage(c, page, optionalViewerID(c))
}

func (h *ContentFeedHandler) respondPostPage(c *gin.Context, page LatestPostPage, viewerID uint64) {
	items, err := h.assembler.Assemble(c.Request.Context(), page.Posts, viewerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.recordExposures(c.Request.Context(), viewerID, items)
	c.JSON(http.StatusOK, ContentFeedPageResponse{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore})
}

func (h *ContentFeedHandler) recordExposures(ctx context.Context, viewerID uint64, items []ContentFeedItem) {
	if h.behavior == nil || len(items) == 0 {
		return
	}
	postIDs := make([]uint64, len(items))
	for index := range items {
		postIDs[index] = items[index].ID
	}
	if err := h.behavior.RecordContentExposures(ctx, viewerID, postIDs); err != nil {
		log.Printf("record content exposures failed: posts=%v err=%v", postIDs, err)
	}
}

func optionalViewerID(c *gin.Context) uint64 {
	viewerID, err := jwt.GetAccountID(c)
	if err != nil {
		return 0
	}
	return uint64(viewerID)
}

func parseContentPageQuery(c *gin.Context) (int, *PostCursor, bool) {
	limit, ok := parseContentLimit(c)
	if !ok {
		return 0, nil, false
	}
	beforeTime := strings.TrimSpace(c.Query("before_time"))
	beforeID := strings.TrimSpace(c.Query("before_id"))
	if beforeTime == "" && beforeID == "" {
		return limit, nil, true
	}
	if beforeTime == "" || beforeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time and before_id must be provided together"})
		return 0, nil, false
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, beforeTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_time must be RFC3339"})
		return 0, nil, false
	}
	id, err := strconv.ParseUint(beforeID, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "before_id must be a positive integer"})
		return 0, nil, false
	}
	return limit, &PostCursor{PublishedAt: publishedAt, ID: id}, true
}

func parseContentLimit(c *gin.Context) (int, bool) {
	limit, err := parseNonNegativeInt(c.Query("limit"), 20)
	if err != nil || limit == 0 || limit > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be in 1..100"})
		return 0, false
	}
	return limit, true
}

func parseNonNegativeInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid non-negative integer")
	}
	return value, nil
}

func parsePopularityFallbackCursor(c *gin.Context) (*PopularityCursor, bool) {
	popularityRaw := strings.TrimSpace(c.Query("fallback_popularity"))
	timeRaw := strings.TrimSpace(c.Query("fallback_time"))
	idRaw := strings.TrimSpace(c.Query("fallback_id"))
	if popularityRaw == "" && timeRaw == "" && idRaw == "" {
		return nil, true
	}
	if popularityRaw == "" || timeRaw == "" || idRaw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fallback_popularity, fallback_time and fallback_id must be provided together"})
		return nil, false
	}
	popularity, err := strconv.ParseFloat(popularityRaw, 64)
	if err != nil || popularity < 0 || math.IsNaN(popularity) || math.IsInf(popularity, 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fallback_popularity must be a finite non-negative number"})
		return nil, false
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, timeRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fallback_time must be RFC3339"})
		return nil, false
	}
	id, err := strconv.ParseUint(idRaw, 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fallback_id must be a positive integer"})
		return nil, false
	}
	return &PopularityCursor{Popularity: popularity, PublishedAt: publishedAt, ID: id}, true
}
