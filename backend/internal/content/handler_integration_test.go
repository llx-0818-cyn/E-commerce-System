package content_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	"github.com/gin-gonic/gin"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestPostAndImageHTTPContractAuthenticationAndMediaFields(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 Post HTTP 契约集成测试")
	}
	database, cleanup := openContentTestDatabase(t, adminDSN)
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
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, err := content.NewRepository(gormDB)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	postService, err := content.NewService(repository, config.ContentConfig{MaxImages: 9, MaxImageSizeBytes: 1024 * 1024, MaxBodyLength: 10_000})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	storage, err := content.NewLocalStorage(t.TempDir(), "/static", 1024*1024)
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	imageService, err := content.NewImageService(repository, storage)
	if err != nil {
		t.Fatalf("NewImageService() error = %v", err)
	}
	handler, err := content.NewHandler(postService, imageService)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	router := contentTestRouter(handler)

	unauthorized := performJSONRequest(router, http.MethodPost, "/api/posts", `{"title":"未登录","body":"不能发布"}`, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized publish status = %d, body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	published := performJSONRequest(router, http.MethodPost, "/api/posts", `{"title":"HTTP 图文 #API","body":"响应不能有视频字段","tags":["契约"]}`, "1")
	if published.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, body=%s", published.Code, published.Body.String())
	}
	var post map[string]any
	if err := json.Unmarshal(published.Body.Bytes(), &post); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	assertNoAudioVideoFields(t, post)
	postID := uint64(post["id"].(float64))

	detail := performJSONRequest(router, http.MethodGet, fmt.Sprintf("/api/posts/%d", postID), "", "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body=%s", detail.Code, detail.Body.String())
	}
	var detailBody map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	assertNoAudioVideoFields(t, detailBody)

	authorList := performJSONRequest(router, http.MethodGet, "/api/authors/1/posts?limit=10", "", "")
	if authorList.Code != http.StatusOK {
		t.Fatalf("author list status = %d, body=%s", authorList.Code, authorList.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(authorList.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode author list: %v", err)
	}
	assertNoAudioVideoFields(t, listBody)

	forbidden := performJSONRequest(router, http.MethodPatch, fmt.Sprintf("/api/posts/%d", postID), `{"title":"越权","body":"不能修改"}`, "2")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden update status = %d, body=%s", forbidden.Code, forbidden.Body.String())
	}

	imageResponse := performImageUpload(router, "/api/images", integrationPNG(t, 4, 3), "1")
	if imageResponse.Code != http.StatusCreated {
		t.Fatalf("image upload status = %d, body=%s", imageResponse.Code, imageResponse.Body.String())
	}
	var imageBody map[string]any
	if err := json.Unmarshal(imageResponse.Body.Bytes(), &imageBody); err != nil {
		t.Fatalf("decode image response: %v", err)
	}
	assertNoAudioVideoFields(t, imageBody)
	imageID := uint64(imageBody["id"].(float64))
	deleteImage := performJSONRequest(router, http.MethodDelete, fmt.Sprintf("/api/images/%d", imageID), "", "1")
	if deleteImage.Code != http.StatusOK {
		t.Fatalf("delete image status = %d, body=%s", deleteImage.Code, deleteImage.Body.String())
	}
}

func contentTestRouter(handler *content.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/posts/:id", handler.Detail)
	router.GET("/api/authors/:author_id/posts", handler.ListByAuthor)
	protected := router.Group("/api")
	protected.Use(func(c *gin.Context) {
		if raw := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer account-"); raw != "" {
			if accountID, err := strconv.ParseUint(raw, 10, 64); err == nil {
				c.Set("accountID", uint(accountID))
			}
		}
		c.Next()
	})
	protected.POST("/posts", handler.Publish)
	protected.PATCH("/posts/:id", handler.Update)
	protected.DELETE("/posts/:id", handler.Delete)
	protected.PATCH("/posts/:id/images/order", handler.ReorderImages)
	protected.POST("/images", handler.UploadImage)
	protected.DELETE("/images/:id", handler.DeleteImage)
	return router
}

func performJSONRequest(router http.Handler, method, target, body, accountID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if accountID != "" {
		request.Header.Set("Authorization", "Bearer account-"+accountID)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func performImageUpload(router http.Handler, target string, data []byte, accountID string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "picture.png")
	if err != nil {
		panic(err)
	}
	if _, err := part.Write(data); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer account-"+accountID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertNoAudioVideoFields(t *testing.T, value any) {
	t.Helper()
	forbidden := map[string]struct{}{
		"audio_url": {}, "video_url": {}, "play_url": {}, "cover_url": {},
		"upload_id": {}, "chunk_index": {}, "chunk_size": {}, "total_chunks": {},
	}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, exists := forbidden[key]; exists {
					t.Fatalf("response contains forbidden media field %q: %#v", key, value)
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
}
