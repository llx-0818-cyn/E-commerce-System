package interaction

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestInteractionHTTPUsesAuthenticatedActorTargetStateAndPublicComments(t *testing.T) {
	application := &fakeInteractionApplication{}
	handler, _ := NewHandler(application)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	api.Use(func(c *gin.Context) {
		if c.GetHeader("X-Account") != "" {
			c.Set("accountID", uint(7))
		}
		c.Next()
	})
	api.PUT("/posts/:id/like", handler.SetLike)
	api.GET("/posts/:id/comments", handler.ListComments)
	api.POST("/posts/:id/comments", handler.PublishComment)

	unauthorized := interactionRequest(router, http.MethodPut, "/api/posts/9/like", `{"active":true}`, false)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	liked := interactionRequest(router, http.MethodPut, "/api/posts/9/like", `{"active":true}`, true)
	if liked.Code != http.StatusOK || application.actorID != 7 || application.postID != 9 || !application.active {
		t.Fatalf("like response/capture = %d %#v", liked.Code, application)
	}
	comments := interactionRequest(router, http.MethodGet, "/api/posts/9/comments?limit=10", "", false)
	if comments.Code != http.StatusOK || application.commentLimit != 10 {
		t.Fatalf("public comments = %d %#v", comments.Code, application)
	}
	created := interactionRequest(router, http.MethodPost, "/api/posts/9/comments", `{"content":"hello @Alice"}`, true)
	if created.Code != http.StatusCreated || application.commentBody != "hello @Alice" {
		t.Fatalf("create comment = %d %#v", created.Code, application)
	}
}

func interactionRequest(router http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if auth {
		request.Header.Set("X-Account", "7")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type fakeInteractionApplication struct {
	actorID, postID uint64
	active          bool
	commentLimit    int
	commentBody     string
}

func (f *fakeInteractionApplication) SetLike(_ context.Context, actor, post uint64, active bool) (StateResult, error) {
	f.actorID, f.postID, f.active = actor, post, active
	return StateResult{Active: active, Count: 1}, nil
}
func (f *fakeInteractionApplication) SetFavorite(context.Context, uint64, uint64, bool) (StateResult, error) {
	return StateResult{}, nil
}
func (f *fakeInteractionApplication) LikeStates(context.Context, uint64, []uint64) (map[uint64]bool, error) {
	return map[uint64]bool{}, nil
}
func (f *fakeInteractionApplication) FavoriteStates(context.Context, uint64, []uint64) (map[uint64]bool, error) {
	return map[uint64]bool{}, nil
}
func (f *fakeInteractionApplication) ListFavorites(context.Context, FavoriteListQuery) (FavoriteListResult, error) {
	return FavoriteListResult{Posts: nil}, nil
}
func (f *fakeInteractionApplication) PublishComment(_ context.Context, actor, post uint64, body string) (*PostComment, error) {
	f.actorID, f.postID, f.commentBody = actor, post, body
	return &PostComment{ID: 1, PostID: post, AuthorID: actor, Content: body, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now(), MentionedUsernames: []string{"alice"}}, nil
}
func (f *fakeInteractionApplication) DeleteComment(context.Context, uint64, uint64) error { return nil }
func (f *fakeInteractionApplication) ListComments(_ context.Context, query CommentListQuery) (CommentListResult, error) {
	f.commentLimit = query.Limit
	return CommentListResult{Comments: []PostComment{}}, nil
}
