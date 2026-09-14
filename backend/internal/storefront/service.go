package storefront

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"feedsystem_video_go/internal/cacheinvalidation"
	domainevent "feedsystem_video_go/internal/event"
	searchdomain "feedsystem_video_go/internal/search"

	mysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

var (
	ErrShopValidation       = errors.New("invalid shop")
	ErrShopNotFound         = errors.New("shop not found")
	ErrShopForbidden        = errors.New("shop operation forbidden")
	ErrShopAlreadyExists    = errors.New("account already owns a shop")
	ErrShopDisabled         = errors.New("disabled shop status cannot be changed by owner")
	ErrProductValidation    = errors.New("invalid product query")
	ErrProductNotFound      = errors.New("product not found")
	ErrProductForbidden     = errors.New("product operation forbidden")
	ErrProductDisabled      = errors.New("disabled product cannot be managed by owner")
	ErrShopNotOpen          = errors.New("shop must be open before product can go on sale")
	ErrProductNoSellableSKU = errors.New("product requires an active SKU with available stock")
	ErrSKUValidation        = errors.New("invalid SKU")
	ErrSKUNotFound          = errors.New("SKU not found")
	ErrSKUDisabled          = errors.New("disabled SKU cannot be managed by owner")
	ErrSKUCodeConflict      = errors.New("SKU code already exists")
	ErrStockInsufficient    = errors.New("available stock cannot become negative")
)

var skuCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,63}$`)

type ShopAlreadyExistsError struct {
	ShopID uint64
}

func (e *ShopAlreadyExistsError) Error() string {
	return fmt.Sprintf("%s: shop_id=%d", ErrShopAlreadyExists, e.ShopID)
}

func (e *ShopAlreadyExistsError) Unwrap() error { return ErrShopAlreadyExists }

type Service struct {
	repository         *Repository
	publicCache        *PublicEntityCache
	events             *domainevent.Repository
	searchIndexEnabled bool
}

func NewService(repository *Repository, caches ...*PublicEntityCache) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("storefront service repository is nil")
	}
	if len(caches) > 1 {
		return nil, errors.New("storefront service accepts at most one public entity cache")
	}
	events, err := domainevent.NewRepository(repository.db)
	if err != nil {
		return nil, err
	}
	var publicCache *PublicEntityCache
	if len(caches) == 1 {
		publicCache = caches[0]
	}
	return &Service{repository: repository, publicCache: publicCache, events: events}, nil
}

func (s *Service) CreateShop(ctx context.Context, ownerID uint64, input ShopProfileInput) (*Shop, error) {
	if ownerID == 0 {
		return nil, fmt.Errorf("%w: owner_id is required", ErrShopValidation)
	}
	normalized, err := normalizeShopProfile(input)
	if err != nil {
		return nil, err
	}
	if existing, err := s.repository.findShopByOwner(ctx, ownerID); err == nil {
		return existing, &ShopAlreadyExistsError{ShopID: existing.ID}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	shop := &Shop{
		OwnerID: ownerID, Name: normalized.Name, Description: normalized.Description,
		AvatarURL: normalized.AvatarURL, BannerURL: normalized.BannerURL, Status: ShopStatusClosed,
	}
	if err := s.repository.createShop(ctx, shop); err != nil {
		if isStorefrontDuplicate(err) {
			existing, loadErr := s.repository.findShopByOwner(ctx, ownerID)
			if loadErr != nil {
				return nil, err
			}
			return existing, &ShopAlreadyExistsError{ShopID: existing.ID}
		}
		return nil, err
	}
	s.invalidateAfterCommit(ctx, cacheinvalidation.Target{Entity: cacheinvalidation.ShopSummary, ID: shop.ID})
	s.recordSearchAfterCommit(searchdomain.ScopeShop, shop.ID, searchdomain.IndexUpsert)
	return shop, nil
}

func (s *Service) UpdateShop(ctx context.Context, actorID, shopID uint64, input ShopProfileInput) (*Shop, error) {
	if actorID == 0 || shopID == 0 {
		return nil, ErrShopValidation
	}
	normalized, err := normalizeShopProfile(input)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadOwnedShop(ctx, actorID, shopID); err != nil {
		return nil, err
	}
	if err := s.repository.updateShopProfile(ctx, shopID, actorID, normalized); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx, cacheinvalidation.Target{Entity: cacheinvalidation.ShopSummary, ID: shopID})
	s.recordSearchAfterCommit(searchdomain.ScopeShop, shopID, searchdomain.IndexUpsert)
	return s.repository.findShopByID(ctx, shopID)
}

func (s *Service) OpenShop(ctx context.Context, actorID, shopID uint64) (*Shop, error) {
	return s.setOperatingStatus(ctx, actorID, shopID, ShopStatusOpen)
}

func (s *Service) CloseShop(ctx context.Context, actorID, shopID uint64) (*Shop, error) {
	return s.setOperatingStatus(ctx, actorID, shopID, ShopStatusClosed)
}

func (s *Service) setOperatingStatus(ctx context.Context, actorID, shopID uint64, target ShopStatus) (*Shop, error) {
	if actorID == 0 || shopID == 0 {
		return nil, ErrShopValidation
	}
	shop, err := s.loadOwnedShop(ctx, actorID, shopID)
	if err != nil {
		return nil, err
	}
	if shop.Status == ShopStatusDisabled {
		return nil, ErrShopDisabled
	}
	if shop.Status == target {
		return shop, nil
	}
	if err := s.repository.updateShopStatus(ctx, shopID, actorID, target); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx, cacheinvalidation.Target{Entity: cacheinvalidation.ShopSummary, ID: shopID})
	s.recordSearchAfterCommit(searchdomain.ScopeShop, shopID, searchdomain.IndexUpsert)
	return s.repository.findShopByID(ctx, shopID)
}

func (s *Service) loadOwnedShop(ctx context.Context, actorID, shopID uint64) (*Shop, error) {
	shop, err := s.repository.findShopByID(ctx, shopID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShopNotFound
	}
	if err != nil {
		return nil, err
	}
	if shop.OwnerID != actorID {
		return nil, ErrShopForbidden
	}
	return shop, nil
}

func normalizeShopProfile(input ShopProfileInput) (ShopProfileInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.AvatarURL = strings.TrimSpace(input.AvatarURL)
	input.BannerURL = strings.TrimSpace(input.BannerURL)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 120 {
		return ShopProfileInput{}, fmt.Errorf("%w: name must contain 1..120 characters", ErrShopValidation)
	}
	if utf8.RuneCountInString(input.Description) > 1000 {
		return ShopProfileInput{}, fmt.Errorf("%w: description exceeds 1000 characters", ErrShopValidation)
	}
	if utf8.RuneCountInString(input.AvatarURL) > 1024 || utf8.RuneCountInString(input.BannerURL) > 1024 {
		return ShopProfileInput{}, fmt.Errorf("%w: shop image URL exceeds 1024 characters", ErrShopValidation)
	}
	return input, nil
}

func isStorefrontDuplicate(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func (s *Service) GetPublicShop(ctx context.Context, shopID uint64) (*PublicShop, error) {
	if shopID == 0 {
		return nil, ErrShopValidation
	}
	if s.publicCache != nil {
		shop, found, err := s.publicCache.getShop(ctx, shopID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrShopNotFound
		}
		return shop, nil
	}
	shop, err := s.repository.findShopByID(ctx, shopID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && shop.Status == ShopStatusDisabled) {
		return nil, ErrShopNotFound
	}
	if err != nil {
		return nil, err
	}
	productCount, err := s.repository.countPublicProducts(ctx, shopID)
	if err != nil {
		return nil, err
	}
	return &PublicShop{Shop: *shop, ProductCount: productCount, AcceptingOrders: shop.Status == ShopStatusOpen}, nil
}

func (s *Service) GetSellerDashboard(ctx context.Context, actorID uint64) (*SellerDashboard, error) {
	if actorID == 0 {
		return nil, ErrShopValidation
	}
	shop, err := s.repository.findShopByOwner(ctx, actorID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShopNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.repository.sellerDashboard(ctx, shop.ID)
}

func (s *Service) GetOwnedShop(ctx context.Context, actorID uint64) (*Shop, error) {
	if actorID == 0 {
		return nil, ErrShopValidation
	}
	shop, err := s.repository.findShopByOwner(ctx, actorID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShopNotFound
	}
	return shop, err
}

func (s *Service) ListSellerProducts(ctx context.Context, actorID uint64, limit int) (*SellerProductPage, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrProductValidation
	}
	shop, err := s.GetOwnedShop(ctx, actorID)
	if err != nil {
		return nil, err
	}
	products, err := s.repository.listSellerProducts(ctx, shop.ID, limit)
	if err != nil {
		return nil, err
	}
	return &SellerProductPage{Products: products}, nil
}

func (s *Service) GetSellerProduct(ctx context.Context, actorID, productID uint64) (*SellerProductDetail, error) {
	if _, _, err := s.loadOwnedProduct(ctx, actorID, productID); err != nil {
		return nil, err
	}
	return s.repository.sellerProductDetail(ctx, productID)
}

func (s *Service) ListPublicProducts(ctx context.Context, shopID uint64, limit int, cursor *ProductCursor) (PublicProductPage, error) {
	shop, err := s.GetPublicShop(ctx, shopID)
	if err != nil {
		return PublicProductPage{}, err
	}
	if limit <= 0 || limit > 100 {
		return PublicProductPage{}, fmt.Errorf("%w: limit must be in 1..100", ErrProductValidation)
	}
	if cursor != nil && (cursor.CreatedAt.IsZero() || cursor.ID == 0) {
		return PublicProductPage{}, fmt.Errorf("%w: product cursor is incomplete", ErrProductValidation)
	}
	products, err := s.repository.listPublicProducts(ctx, shopID, limit+1, cursor)
	if err != nil {
		return PublicProductPage{}, err
	}
	hasMore := len(products) > limit
	if hasMore {
		products = products[:limit]
	}
	ids := make([]uint64, len(products))
	for index := range products {
		ids[index] = products[index].ID
	}
	minimumPrices, err := s.repository.productMinimumSellablePrices(ctx, ids)
	if err != nil {
		return PublicProductPage{}, err
	}
	page := PublicProductPage{Products: make([]PublicProductItem, 0, len(products)), HasMore: hasMore}
	for _, product := range products {
		var minimumPrice *int64
		if value, ok := minimumPrices[product.ID]; ok {
			valueCopy := value
			minimumPrice = &valueCopy
		}
		page.Products = append(page.Products, PublicProductItem{
			Product: product, MinPriceCent: minimumPrice,
			CanPurchase: shop.AcceptingOrders && minimumPrice != nil,
		})
	}
	if len(products) > 0 {
		last := products[len(products)-1]
		page.NextCursor = &ProductCursor{CreatedAt: last.CreatedAt.UTC(), ID: last.ID}
	}
	return page, nil
}

func (s *Service) GetPublicProduct(ctx context.Context, productID uint64) (*PublicProductDetail, error) {
	if productID == 0 {
		return nil, ErrProductValidation
	}
	if s.publicCache != nil {
		cached, found, err := s.publicCache.getProduct(ctx, productID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrProductNotFound
		}
		shop, err := s.GetPublicShop(ctx, cached.Product.ShopID)
		if errors.Is(err, ErrShopNotFound) {
			return nil, ErrProductNotFound
		}
		if err != nil {
			return nil, err
		}
		skus, err := s.publicCache.getSKUs(ctx, cached.SKUIDs)
		if err != nil {
			return nil, err
		}
		return buildPublicProductDetail(cached.Product, *shop, cached.Images, skus), nil
	}
	product, err := s.repository.findPublicProduct(ctx, productID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProductNotFound
	}
	if err != nil {
		return nil, err
	}
	shop, err := s.GetPublicShop(ctx, product.ShopID)
	if errors.Is(err, ErrShopNotFound) {
		return nil, ErrProductNotFound
	}
	if err != nil {
		return nil, err
	}
	images, err := s.repository.listProductImages(ctx, product.ID)
	if err != nil {
		return nil, err
	}
	skus, err := s.repository.listPublicProductSKUs(ctx, product.ID)
	if err != nil {
		return nil, err
	}
	return buildPublicProductDetail(*product, *shop, images, skus), nil
}

func buildPublicProductDetail(product Product, shop PublicShop, images []ProductImage, skus []ProductSKU) *PublicProductDetail {
	publicSKUs := make([]PublicSKU, 0, len(skus))
	canPurchase := false
	var minimumPrice *int64
	for _, sku := range skus {
		skuSellable := sku.Status == SKUStatusActive && sku.AvailableStock > 0
		available := shop.AcceptingOrders && skuSellable
		publicSKUs = append(publicSKUs, PublicSKU{
			ID: sku.ID, SKUCode: sku.SKUCode, Specs: sku.Specs, PriceCent: sku.PriceCent,
			OriginalPriceCent: sku.OriginalPriceCent, AvailableStock: sku.AvailableStock,
			Status: sku.Status, CanPurchase: available,
		})
		canPurchase = canPurchase || available
		if skuSellable && (minimumPrice == nil || sku.PriceCent < *minimumPrice) {
			value := sku.PriceCent
			minimumPrice = &value
		}
	}
	return &PublicProductDetail{
		Product: product, Shop: shop, Images: images, SKUs: publicSKUs,
		MinPriceCent: minimumPrice, CanPurchase: canPurchase,
		RelatedPostsURL: fmt.Sprintf("/api/products/%d/posts", product.ID),
	}
}

func (s *Service) ListRelatedPublicPosts(ctx context.Context, productID uint64, limit int, cursor *RelatedPostCursor) (RelatedPostPage, error) {
	if productID == 0 || limit <= 0 || limit > 100 {
		return RelatedPostPage{}, ErrProductValidation
	}
	if cursor != nil && (cursor.PublishedAt.IsZero() || cursor.ID == 0) {
		return RelatedPostPage{}, ErrProductValidation
	}
	product, err := s.repository.findPublicProduct(ctx, productID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RelatedPostPage{}, ErrProductNotFound
	}
	if err != nil {
		return RelatedPostPage{}, err
	}
	shop, err := s.repository.findShopByID(ctx, product.ShopID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && shop.Status == ShopStatusDisabled) {
		return RelatedPostPage{}, ErrProductNotFound
	}
	if err != nil {
		return RelatedPostPage{}, err
	}
	posts, err := s.repository.listRelatedPublicPosts(ctx, productID, limit+1, cursor)
	if err != nil {
		return RelatedPostPage{}, err
	}
	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	page := RelatedPostPage{Posts: posts, HasMore: hasMore}
	if len(posts) > 0 {
		last := posts[len(posts)-1]
		if last.PublishedAt != nil {
			page.NextCursor = &RelatedPostCursor{PublishedAt: last.PublishedAt.UTC(), ID: last.ID}
		}
	}
	return page, nil
}

func (s *Service) CreateProduct(ctx context.Context, actorID uint64, input ProductProfileInput, images []ProductImageInput) (*Product, error) {
	if actorID == 0 {
		return nil, ErrProductValidation
	}
	shop, err := s.repository.findShopByOwner(ctx, actorID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrShopNotFound
	}
	if err != nil {
		return nil, err
	}
	if shop.Status == ShopStatusDisabled {
		return nil, ErrShopDisabled
	}
	normalized, err := s.normalizeProductProfile(ctx, input)
	if err != nil {
		return nil, err
	}
	normalizedImages, err := normalizeProductImages(images)
	if err != nil {
		return nil, err
	}
	product := &Product{
		ShopID: shop.ID, CategoryID: normalized.CategoryID, Name: normalized.Name,
		Subtitle: normalized.Subtitle, Description: normalized.Description,
		CoverURL: normalized.CoverURL, Status: ProductStatusDraft,
	}
	if err := s.repository.createProductWithImages(ctx, product, normalizedImages); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductDetail, ID: product.ID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ShopSummary, ID: shop.ID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, product.ID, searchdomain.IndexUpsert)
	return product, nil
}

func (s *Service) UpdateProduct(ctx context.Context, actorID, productID uint64, input ProductProfileInput) (*Product, error) {
	product, _, err := s.loadOwnedProduct(ctx, actorID, productID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	normalized, err := s.normalizeProductProfile(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := s.repository.updateProductProfile(ctx, productID, normalized); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx, cacheinvalidation.Target{Entity: cacheinvalidation.ProductDetail, ID: productID})
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, productID, searchdomain.IndexUpsert)
	return s.repository.findProductByID(ctx, productID)
}

func (s *Service) ReplaceProductImages(ctx context.Context, actorID, productID uint64, images []ProductImageInput) ([]ProductImage, error) {
	product, _, err := s.loadOwnedProduct(ctx, actorID, productID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	normalized, err := normalizeProductImages(images)
	if err != nil {
		return nil, err
	}
	if err := s.repository.replaceProductImages(ctx, productID, normalized); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx, cacheinvalidation.Target{Entity: cacheinvalidation.ProductDetail, ID: productID})
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, productID, searchdomain.IndexUpsert)
	return s.repository.listProductImages(ctx, productID)
}

func (s *Service) CreateSKU(ctx context.Context, actorID, productID uint64, input CreateSKUInput) (*ProductSKU, error) {
	product, _, err := s.loadOwnedProduct(ctx, actorID, productID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	normalized, err := normalizeCreateSKU(input)
	if err != nil {
		return nil, err
	}
	sku := &ProductSKU{
		ProductID: productID, SKUCode: normalized.SKUCode, Specs: normalized.Specs,
		PriceCent: normalized.PriceCent, OriginalPriceCent: normalized.OriginalPriceCent,
		AvailableStock: normalized.AvailableStock, Status: SKUStatusInactive,
	}
	if err := s.repository.createSKU(ctx, sku); err != nil {
		if isStorefrontDuplicate(err) {
			return nil, ErrSKUCodeConflict
		}
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductDetail, ID: productID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductSKU, ID: sku.ID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductInventory, ID: sku.ID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductPriceRange, ID: productID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, productID, searchdomain.IndexUpsert)
	return sku, nil
}

func (s *Service) UpdateSKU(ctx context.Context, actorID, skuID uint64, input UpdateSKUInput) (*ProductSKU, error) {
	sku, product, _, err := s.loadOwnedSKU(ctx, actorID, skuID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	if sku.Status == SKUStatusDisabled {
		return nil, ErrSKUDisabled
	}
	normalized, err := normalizeUpdateSKU(input)
	if err != nil {
		return nil, err
	}
	if err := s.repository.updateSKUProfile(ctx, skuID, normalized); err != nil {
		if isStorefrontDuplicate(err) {
			return nil, ErrSKUCodeConflict
		}
		return nil, err
	}
	updated, err := s.repository.findSKUByID(ctx, skuID)
	if err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductSKU, ID: skuID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductPriceRange, ID: product.ID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, product.ID, searchdomain.IndexUpsert)
	return updated, nil
}

func (s *Service) SetSKUStatus(ctx context.Context, actorID, skuID uint64, target SKUStatus) (*ProductSKU, error) {
	if target != SKUStatusActive && target != SKUStatusInactive {
		return nil, ErrSKUValidation
	}
	sku, product, _, err := s.loadOwnedSKU(ctx, actorID, skuID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	if sku.Status == SKUStatusDisabled {
		return nil, ErrSKUDisabled
	}
	if sku.Status == target {
		return sku, nil
	}
	if err := s.repository.updateSKUStatus(ctx, skuID, target); err != nil {
		return nil, err
	}
	updated, err := s.repository.findSKUByID(ctx, skuID)
	if err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductSKU, ID: skuID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductPriceRange, ID: product.ID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, product.ID, searchdomain.IndexUpsert)
	return updated, nil
}

func (s *Service) AdjustSKUStock(ctx context.Context, actorID, skuID uint64, delta int64) (*ProductSKU, error) {
	if delta == 0 {
		return nil, ErrSKUValidation
	}
	sku, product, _, err := s.loadOwnedSKU(ctx, actorID, skuID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	if sku.Status == SKUStatusDisabled {
		return nil, ErrSKUDisabled
	}
	if err := s.repository.adjustAvailableStock(ctx, skuID, delta); errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrStockInsufficient
	} else if err != nil {
		return nil, err
	}
	updated, err := s.repository.findSKUByID(ctx, skuID)
	if err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductInventory, ID: skuID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductPriceRange, ID: product.ID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, product.ID, searchdomain.IndexUpsert)
	return updated, nil
}

func (s *Service) PublishProduct(ctx context.Context, actorID, productID uint64) (*Product, error) {
	product, shop, err := s.loadOwnedProduct(ctx, actorID, productID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	if shop.Status == ShopStatusDisabled {
		return nil, ErrShopDisabled
	}
	if shop.Status != ShopStatusOpen {
		return nil, ErrShopNotOpen
	}
	count, err := s.repository.countSellableSKUs(ctx, productID)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrProductNoSellableSKU
	}
	if product.Status == ProductStatusOnSale {
		return product, nil
	}
	if err := s.repository.updateProductStatus(ctx, productID, ProductStatusOnSale); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductDetail, ID: productID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductPriceRange, ID: productID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ShopSummary, ID: shop.ID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, productID, searchdomain.IndexUpsert)
	return s.repository.findProductByID(ctx, productID)
}

func (s *Service) OffSaleProduct(ctx context.Context, actorID, productID uint64) (*Product, error) {
	product, shop, err := s.loadOwnedProduct(ctx, actorID, productID)
	if err != nil {
		return nil, err
	}
	if product.Status == ProductStatusDisabled {
		return nil, ErrProductDisabled
	}
	if product.Status == ProductStatusOffSale {
		return product, nil
	}
	if err := s.repository.updateProductStatus(ctx, productID, ProductStatusOffSale); err != nil {
		return nil, err
	}
	s.invalidateAfterCommit(ctx,
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductDetail, ID: productID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ProductPriceRange, ID: productID},
		cacheinvalidation.Target{Entity: cacheinvalidation.ShopSummary, ID: shop.ID},
	)
	s.recordSearchAfterCommit(searchdomain.ScopeProduct, productID, searchdomain.IndexDelete)
	return s.repository.findProductByID(ctx, productID)
}

func (s *Service) invalidateAfterCommit(ctx context.Context, targets ...cacheinvalidation.Target) {
	if s.publicCache == nil || len(targets) == 0 {
		return
	}
	if err := s.publicCache.Invalidate(ctx, targets); err != nil {
		eventCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if appendErr := cacheinvalidation.AppendFallback(eventCtx, s.events, targets); appendErr != nil {
			log.Printf("append storefront cache invalidation fallback failed: delete_err=%v append_err=%v", err, appendErr)
		}
	}
}

func (s *Service) recordSearchAfterCommit(scope searchdomain.Scope, id uint64, action searchdomain.IndexAction) {
	if !s.searchIndexEnabled {
		return
	}
	eventCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := searchdomain.AppendIndexRequest(eventCtx, s.events, searchdomain.IndexRequest{Scope: scope, ID: id, Action: action}); err != nil {
		log.Printf("append storefront search index event failed: scope=%s id=%d action=%s err=%v", scope, id, action, err)
	}
}

func (s *Service) EnableSearchIndexing() {
	s.searchIndexEnabled = true
}

func (s *Service) loadOwnedProduct(ctx context.Context, actorID, productID uint64) (*Product, *Shop, error) {
	if actorID == 0 || productID == 0 {
		return nil, nil, ErrProductValidation
	}
	product, err := s.repository.findProductByID(ctx, productID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrProductNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	shop, err := s.repository.findShopByID(ctx, product.ShopID)
	if err != nil {
		return nil, nil, err
	}
	if shop.OwnerID != actorID {
		return nil, nil, ErrProductForbidden
	}
	return product, shop, nil
}

func (s *Service) loadOwnedSKU(ctx context.Context, actorID, skuID uint64) (*ProductSKU, *Product, *Shop, error) {
	if actorID == 0 || skuID == 0 {
		return nil, nil, nil, ErrSKUValidation
	}
	sku, err := s.repository.findSKUByID(ctx, skuID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, ErrSKUNotFound
	}
	if err != nil {
		return nil, nil, nil, err
	}
	product, shop, err := s.loadOwnedProduct(ctx, actorID, sku.ProductID)
	if err != nil {
		return nil, nil, nil, err
	}
	return sku, product, shop, nil
}

func (s *Service) normalizeProductProfile(ctx context.Context, input ProductProfileInput) (ProductProfileInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Subtitle = strings.TrimSpace(input.Subtitle)
	input.Description = strings.TrimSpace(input.Description)
	input.CoverURL = strings.TrimSpace(input.CoverURL)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 200 {
		return ProductProfileInput{}, fmt.Errorf("%w: name must contain 1..200 characters", ErrProductValidation)
	}
	if utf8.RuneCountInString(input.Subtitle) > 255 || utf8.RuneCountInString(input.Description) > 20_000 || utf8.RuneCountInString(input.CoverURL) > 1024 {
		return ProductProfileInput{}, fmt.Errorf("%w: product text field exceeds limit", ErrProductValidation)
	}
	if input.CategoryID != nil {
		if *input.CategoryID == 0 {
			return ProductProfileInput{}, fmt.Errorf("%w: category_id must be positive", ErrProductValidation)
		}
		active, err := s.repository.categoryIsActive(ctx, *input.CategoryID)
		if err != nil {
			return ProductProfileInput{}, err
		}
		if !active {
			return ProductProfileInput{}, fmt.Errorf("%w: category is unavailable", ErrProductValidation)
		}
	}
	return input, nil
}

func normalizeProductImages(inputs []ProductImageInput) ([]ProductImageInput, error) {
	if len(inputs) > 20 {
		return nil, fmt.Errorf("%w: product images cannot exceed 20", ErrProductValidation)
	}
	normalized := make([]ProductImageInput, len(inputs))
	seenStorageKeys := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		input.StorageKey = strings.TrimSpace(input.StorageKey)
		input.ImageURL = strings.TrimSpace(input.ImageURL)
		input.MIMEType = strings.TrimSpace(input.MIMEType)
		if input.StorageKey == "" || input.ImageURL == "" || input.MIMEType == "" || input.ByteSize == 0 || input.Width == 0 || input.Height == 0 {
			return nil, fmt.Errorf("%w: product image %d is incomplete", ErrProductValidation, index)
		}
		if len(input.StorageKey) > 255 || utf8.RuneCountInString(input.ImageURL) > 1024 || len(input.MIMEType) > 64 {
			return nil, fmt.Errorf("%w: product image %d field exceeds limit", ErrProductValidation, index)
		}
		if _, exists := seenStorageKeys[input.StorageKey]; exists {
			return nil, fmt.Errorf("%w: product image storage_key is duplicated", ErrProductValidation)
		}
		seenStorageKeys[input.StorageKey] = struct{}{}
		normalized[index] = input
	}
	return normalized, nil
}

func normalizeCreateSKU(input CreateSKUInput) (CreateSKUInput, error) {
	code, specs, price, original, err := normalizeSKUFields(input.SKUCode, input.Specs, input.PriceCent, input.OriginalPriceCent)
	if err != nil {
		return CreateSKUInput{}, err
	}
	if input.AvailableStock < 0 {
		return CreateSKUInput{}, fmt.Errorf("%w: available_stock must not be negative", ErrSKUValidation)
	}
	input.SKUCode, input.Specs, input.PriceCent, input.OriginalPriceCent = code, specs, price, original
	return input, nil
}

func normalizeUpdateSKU(input UpdateSKUInput) (UpdateSKUInput, error) {
	code, specs, price, original, err := normalizeSKUFields(input.SKUCode, input.Specs, input.PriceCent, input.OriginalPriceCent)
	if err != nil {
		return UpdateSKUInput{}, err
	}
	input.SKUCode, input.Specs, input.PriceCent, input.OriginalPriceCent = code, specs, price, original
	return input, nil
}

func normalizeSKUFields(code string, specs SKUSpecs, price int64, original *int64) (string, SKUSpecs, int64, *int64, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !skuCodePattern.MatchString(code) {
		return "", nil, 0, nil, fmt.Errorf("%w: sku_code format is invalid", ErrSKUValidation)
	}
	if price <= 0 || (original != nil && *original < price) {
		return "", nil, 0, nil, fmt.Errorf("%w: integer-cent prices are invalid", ErrSKUValidation)
	}
	normalizedSpecs := make(SKUSpecs, len(specs))
	for rawKey, rawValue := range specs {
		key, value := strings.TrimSpace(rawKey), strings.TrimSpace(rawValue)
		if key == "" || value == "" || utf8.RuneCountInString(key) > 50 || utf8.RuneCountInString(value) > 100 {
			return "", nil, 0, nil, fmt.Errorf("%w: SKU spec is invalid", ErrSKUValidation)
		}
		if _, duplicate := normalizedSpecs[key]; duplicate {
			return "", nil, 0, nil, fmt.Errorf("%w: normalized SKU spec key is duplicated", ErrSKUValidation)
		}
		normalizedSpecs[key] = value
	}
	return code, normalizedSpecs, price, original, nil
}

func (s *Service) BuildFeedCommerce(ctx context.Context, postIDs, authorIDs []uint64, productLimit int) (FeedCommerceBatch, error) {
	if productLimit <= 0 || productLimit > 10 {
		return FeedCommerceBatch{}, ErrProductValidation
	}
	return s.repository.feedCommerceBatch(ctx, uniqueUint64(postIDs), uniqueUint64(authorIDs), productLimit)
}

func uniqueUint64(values []uint64) []uint64 {
	result := make([]uint64, 0, len(values))
	seen := make(map[uint64]struct{}, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
