package entitycache

import rediscache "feedsystem_video_go/internal/middleware/redis"

func ContentEntityKey(cache *rediscache.Client, postID uint64) string {
	return cache.Key("content:entity:%d", postID)
}

func ShopSummaryKey(cache *rediscache.Client, shopID uint64) string {
	return cache.Key("shop:summary:%d", shopID)
}

func ProductDetailKey(cache *rediscache.Client, productID uint64) string {
	return cache.Key("product:detail:%d", productID)
}

func ProductSKUKey(cache *rediscache.Client, skuID uint64) string {
	return cache.Key("product:sku:%d", skuID)
}

func ProductInventoryKey(cache *rediscache.Client, skuID uint64) string {
	return cache.Key("product:inventory:%d", skuID)
}

func ProductPriceRangeKey(cache *rediscache.Client, productID uint64) string {
	return cache.Key("product:price-range:%d", productID)
}
