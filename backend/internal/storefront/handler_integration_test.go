package storefront_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"feedsystem_video_go/internal/analytics"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/migrations"

	"github.com/gin-gonic/gin"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestStorefrontHTTPPublicContractAndSellerResourceOwnership(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行店铺商品 HTTP 契约测试")
	}
	database, cleanup := openStorefrontTestDatabase(t, adminDSN)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	items, err := migration.Load(migrations.Files)
	if err != nil {
		t.Fatalf("Load(migrations) error = %v", err)
	}
	if count, err := runner.Up(ctx); err != nil || count != len(items) {
		t.Fatalf("Up() = %d, %v; want %d, nil", count, err, len(items))
	}
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
	repository, _ := storefront.NewRepository(gormDB)
	service, _ := storefront.NewService(repository)
	behaviorRecorder, _ := analytics.NewRecorder(gormDB)
	handler, _ := storefront.NewHandler(service, behaviorRecorder)
	router := newStorefrontContractRouter(handler)

	if response := performStorefrontRequest(router, http.MethodPost, "/api/shops", `{"name":"匿名店"}`, 0); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous CreateShop status = %d body=%s, want 401", response.Code, response.Body.String())
	}
	createShop := performStorefrontRequest(router, http.MethodPost, "/api/shops", `{"owner_id":999,"name":"HTTP 店铺"}`, 1)
	if createShop.Code != http.StatusCreated {
		t.Fatalf("CreateShop status = %d body=%s", createShop.Code, createShop.Body.String())
	}
	var shop storefront.Shop
	decodeStorefrontResponse(t, createShop, &shop)
	if shop.OwnerID != 1 {
		t.Fatalf("created shop owner_id = %d, want JWT actor 1", shop.OwnerID)
	}
	duplicate := performStorefrontRequest(router, http.MethodPost, "/api/shops", `{"name":"重复店"}`, 1)
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), `"shop_id":`+strconv.FormatUint(shop.ID, 10)) {
		t.Fatalf("duplicate CreateShop status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
	otherShop := performStorefrontRequest(router, http.MethodPost, "/api/shops", `{"name":"其他店"}`, 2)
	if otherShop.Code != http.StatusCreated {
		t.Fatalf("CreateShop(other) status = %d body=%s", otherShop.Code, otherShop.Body.String())
	}
	if response := performStorefrontRequest(router, http.MethodPatch, "/api/shops/"+strconv.FormatUint(shop.ID, 10), `{"name":"越权店名"}`, 2); response.Code != http.StatusForbidden {
		t.Fatalf("other owner UpdateShop status = %d body=%s, want 403", response.Code, response.Body.String())
	}
	openShop := performStorefrontRequest(router, http.MethodPatch, "/api/shops/"+strconv.FormatUint(shop.ID, 10)+"/status", `{"status":"open"}`, 1)
	if openShop.Code != http.StatusOK {
		t.Fatalf("SetShopStatus(open) status = %d body=%s", openShop.Code, openShop.Body.String())
	}

	createProduct := performStorefrontRequest(router, http.MethodPost, "/api/seller/products", `{
"shop_id":999,
"name":"HTTP 商品",
"description":"卖家接口商品",
"cover_url":"/cover.jpg",
"images":[{"storage_key":"products/http.jpg","image_url":"/http.jpg","mime_type":"image/jpeg","byte_size":100,"width":800,"height":800}]
}`, 1)
	if createProduct.Code != http.StatusCreated {
		t.Fatalf("CreateProduct status = %d body=%s", createProduct.Code, createProduct.Body.String())
	}
	var product storefront.Product
	decodeStorefrontResponse(t, createProduct, &product)
	if product.ShopID != shop.ID {
		t.Fatalf("created product shop_id = %d, want actor shop %d", product.ShopID, shop.ID)
	}
	productPath := "/api/seller/products/" + strconv.FormatUint(product.ID, 10)
	if response := performStorefrontRequest(router, http.MethodPatch, productPath, `{"name":"越权商品","description":"x"}`, 2); response.Code != http.StatusForbidden {
		t.Fatalf("other owner UpdateProduct status = %d body=%s, want 403", response.Code, response.Body.String())
	}
	createSKU := performStorefrontRequest(router, http.MethodPost, productPath+"/skus", `{
"sku_code":"HTTP-SKU-1","specs":{"颜色":"白色"},"price_cent":9900,"available_stock":5
}`, 1)
	if createSKU.Code != http.StatusCreated {
		t.Fatalf("CreateSKU status = %d body=%s", createSKU.Code, createSKU.Body.String())
	}
	var sku storefront.ProductSKU
	decodeStorefrontResponse(t, createSKU, &sku)
	skuPath := "/api/seller/skus/" + strconv.FormatUint(sku.ID, 10)
	if response := performStorefrontRequest(router, http.MethodPost, skuPath+"/stock-adjustments", `{"delta":-1}`, 2); response.Code != http.StatusForbidden {
		t.Fatalf("other owner AdjustSKUStock status = %d body=%s, want 403", response.Code, response.Body.String())
	}
	if response := performStorefrontRequest(router, http.MethodPatch, skuPath+"/status", `{"status":"active"}`, 1); response.Code != http.StatusOK {
		t.Fatalf("SetSKUStatus(active) status = %d body=%s", response.Code, response.Body.String())
	}
	if response := performStorefrontRequest(router, http.MethodPatch, productPath+"/status", `{"status":"on_sale"}`, 1); response.Code != http.StatusOK {
		t.Fatalf("SetProductStatus(on_sale) status = %d body=%s", response.Code, response.Body.String())
	}

	publicShopPath := "/api/shops/" + strconv.FormatUint(shop.ID, 10)
	for _, path := range []string{publicShopPath, publicShopPath + "/products", "/api/products/" + strconv.FormatUint(product.ID, 10) + "?source_post_id=777"} {
		response := performStorefrontRequest(router, http.MethodGet, path, "", 0)
		if response.Code != http.StatusOK {
			t.Fatalf("public GET %s status = %d body=%s", path, response.Code, response.Body.String())
		}
	}
	var sourcePostID uint64
	if err := database.QueryRow(`
SELECT CAST(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.post_id')) AS UNSIGNED)
FROM outbox_events WHERE event_type=? ORDER BY id DESC LIMIT 1`, analytics.ProductClickedEvent).Scan(&sourcePostID); err != nil {
		t.Fatalf("query attributed product click: %v", err)
	}
	if sourcePostID != 777 {
		t.Fatalf("product click source_post_id = %d, want 777", sourcePostID)
	}
}

func newStorefrontContractRouter(handler *storefront.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/shops/:id", handler.GetShop)
	router.GET("/api/shops/:id/products", handler.ListShopProducts)
	router.GET("/api/products/:id", handler.GetProduct)
	router.GET("/api/products/:id/posts", handler.ListProductPosts)
	protected := router.Group("/api")
	protected.Use(func(c *gin.Context) {
		raw := c.GetHeader("X-Test-Account")
		accountID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || accountID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Set("accountID", uint(accountID))
		c.Next()
	})
	protected.POST("/shops", handler.CreateShop)
	protected.PATCH("/shops/:id", handler.UpdateShop)
	protected.PATCH("/shops/:id/status", handler.SetShopStatus)
	protected.POST("/seller/products", handler.CreateProduct)
	protected.PATCH("/seller/products/:id", handler.UpdateProduct)
	protected.PUT("/seller/products/:id/images", handler.ReplaceProductImages)
	protected.PATCH("/seller/products/:id/status", handler.SetProductStatus)
	protected.POST("/seller/products/:id/skus", handler.CreateSKU)
	protected.PATCH("/seller/skus/:id", handler.UpdateSKU)
	protected.PATCH("/seller/skus/:id/status", handler.SetSKUStatus)
	protected.POST("/seller/skus/:id/stock-adjustments", handler.AdjustSKUStock)
	return router
}

func performStorefrontRequest(router http.Handler, method, path, body string, actorID uint64) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if actorID != 0 {
		request.Header.Set("X-Test-Account", strconv.FormatUint(actorID, 10))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeStorefrontResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode storefront response %q: %v", response.Body.String(), err)
	}
}
