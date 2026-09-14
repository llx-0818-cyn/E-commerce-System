package feed_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/auth"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/gin-gonic/gin"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestContentFeedHTTPSoftAndRequiredAuthenticationContracts(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行图文 Feed HTTP 鉴权契约测试")
	}
	t.Setenv("JWT_SECRET", "content-feed-http-contract-secret-32-bytes")
	database, cleanup := openFeedTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	migrationItems, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(migrationItems) {
		t.Fatalf("migration Up() = %d, %v; want %d, nil", count, err, len(migrationItems))
	}
	createFollowingFixtures(t, database)

	base := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	followedPost := seedHTTPFeedPost(t, database, 1, "关注作者内容", 20, base.Add(2*time.Second))
	_ = seedHTTPFeedPost(t, database, 3, "未关注作者内容", 10, base.Add(time.Second))
	if _, err := database.Exec("INSERT INTO tags (name) VALUES ('go')"); err != nil {
		t.Fatalf("seed go tag: %v", err)
	}
	if _, err := database.Exec("INSERT INTO post_tags (post_id, tag_id) SELECT ?, id FROM tags WHERE name = 'go'", followedPost); err != nil {
		t.Fatalf("attach go tag: %v", err)
	}
	const viewerID = uint64(4)
	if _, err := database.Exec("INSERT INTO post_likes (post_id, account_id) VALUES (?, ?)", followedPost, viewerID); err != nil {
		t.Fatalf("seed viewer like: %v", err)
	}
	if _, err := database.Exec("INSERT INTO post_favorites (post_id, account_id) VALUES (?, ?)", followedPost, viewerID); err != nil {
		t.Fatalf("seed viewer favorite: %v", err)
	}
	validToken, err := auth.GenerateToken(uint(viewerID), "viewer")
	if err != nil {
		t.Fatalf("GenerateToken(viewer) error = %v", err)
	}
	if _, err := database.Exec("UPDATE accounts SET token = ? WHERE id = ?", validToken, viewerID); err != nil {
		t.Fatalf("persist viewer token: %v", err)
	}
	revokedToken, err := auth.GenerateToken(3, "outsider")
	if err != nil {
		t.Fatalf("GenerateToken(revoked) error = %v", err)
	}

	router := newContentFeedContractRouter(t, database)
	softPaths := []string{
		"/api/feeds/latest?limit=10",
		"/api/feeds/popular?limit=10",
		"/api/feeds/tags/GO?limit=10",
	}
	for _, path := range softPaths {
		if response := performFeedRequest(router, path, ""); response.Code != http.StatusOK {
			t.Fatalf("anonymous GET %s status = %d body=%s, want 200", path, response.Code, response.Body.String())
		}
		if response := performFeedRequest(router, path, validToken); response.Code != http.StatusOK {
			t.Fatalf("valid-token GET %s status = %d body=%s, want 200", path, response.Code, response.Body.String())
		}
		if response := performFeedRequest(router, path, revokedToken); response.Code != http.StatusUnauthorized {
			t.Fatalf("revoked-token GET %s status = %d body=%s, want 401", path, response.Code, response.Body.String())
		}
	}

	anonymousLatest := decodeContentFeedPage(t, performFeedRequest(router, softPaths[0], ""))
	validLatest := decodeContentFeedPage(t, performFeedRequest(router, softPaths[0], validToken))
	anonymousItem := findContentFeedItem(t, anonymousLatest.Items, followedPost)
	validItem := findContentFeedItem(t, validLatest.Items, followedPost)
	if anonymousItem.IsLiked || anonymousItem.IsFavorited {
		t.Fatalf("anonymous latest states = liked:%v favorited:%v", anonymousItem.IsLiked, anonymousItem.IsFavorited)
	}
	if !validItem.IsLiked || !validItem.IsFavorited {
		t.Fatalf("valid-token latest states = liked:%v favorited:%v", validItem.IsLiked, validItem.IsFavorited)
	}

	followingPath := "/api/feeds/following?limit=10"
	if response := performFeedRequest(router, followingPath, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous following status = %d body=%s, want 401", response.Code, response.Body.String())
	}
	if response := performFeedRequest(router, followingPath, revokedToken); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked-token following status = %d body=%s, want 401", response.Code, response.Body.String())
	}
	validFollowingResponse := performFeedRequest(router, followingPath, validToken)
	if validFollowingResponse.Code != http.StatusOK {
		t.Fatalf("valid-token following status = %d body=%s, want 200", validFollowingResponse.Code, validFollowingResponse.Body.String())
	}
	validFollowing := decodeContentFeedPage(t, validFollowingResponse)
	if len(validFollowing.Items) != 1 || validFollowing.Items[0].ID != followedPost {
		t.Fatalf("valid-token following items = %#v, want only post %d", validFollowing.Items, followedPost)
	}
}

func newContentFeedContractRouter(t *testing.T, database *sql.DB) *gin.Engine {
	t.Helper()
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	accountRepository := account.NewAccountRepository(gormDB)
	contentRepository, err := content.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("New content repository: %v", err)
	}
	interactionRepository, err := interaction.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("New interaction repository: %v", err)
	}
	interactionService, err := interaction.NewService(interactionRepository)
	if err != nil {
		t.Fatalf("New interaction service: %v", err)
	}
	feedRepository := feed.NewFeedRepository(gormDB)
	latest, _ := feed.NewContentLatestService(feedRepository, nil)
	following, _ := feed.NewContentFollowingService(feedRepository, nil)
	popularity, _ := feed.NewContentPopularityService(feedRepository, nil)
	tags, _ := feed.NewContentTagService(feedRepository)
	assembler, _ := feed.NewFeedAssembler(contentRepository, accountRepository, interactionService)
	handler, err := feed.NewContentFeedHandler(latest, following, popularity, tags, assembler)
	if err != nil {
		t.Fatalf("NewContentFeedHandler() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	public := router.Group("/api/feeds")
	public.Use(jwt.SoftJWTAuth(accountRepository, nil))
	public.GET("/latest", handler.Latest)
	public.GET("/popular", handler.Popular)
	public.GET("/tags/:tag", handler.Tag)
	protected := router.Group("/api/feeds")
	protected.Use(jwt.JWTAuth(accountRepository, nil))
	protected.GET("/following", handler.Following)
	return router
}

func seedHTTPFeedPost(t *testing.T, database *sql.DB, authorID uint64, title string, popularity float64, publishedAt time.Time) uint64 {
	t.Helper()
	result, err := database.Exec(`
INSERT INTO posts (author_id,title,body,status,popularity,published_at)
VALUES (?, ?, 'HTTP Feed 正文', 'published', ?, ?)`, authorID, title, popularity, publishedAt)
	if err != nil {
		t.Fatalf("seed HTTP feed post %q: %v", title, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("HTTP feed post LastInsertId: %v", err)
	}
	return uint64(id)
}

func performFeedRequest(router http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeContentFeedPage(t *testing.T, response *httptest.ResponseRecorder) feed.ContentFeedPageResponse {
	t.Helper()
	var page feed.ContentFeedPageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode content feed response %q: %v", response.Body.String(), err)
	}
	return page
}

func findContentFeedItem(t *testing.T, items []feed.ContentFeedItem, postID uint64) feed.ContentFeedItem {
	t.Helper()
	for _, item := range items {
		if item.ID == postID {
			return item
		}
	}
	t.Fatalf("post %d not found in items: %s", postID, fmt.Sprint(contentFeedItemIDs(items)))
	return feed.ContentFeedItem{}
}
