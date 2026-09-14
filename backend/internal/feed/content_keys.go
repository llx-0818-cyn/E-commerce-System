package feed

import (
	"time"

	"feedsystem_video_go/internal/entitycache"
	rediscache "feedsystem_video_go/internal/middleware/redis"
)

func ContentTimelineKey(cache *rediscache.Client) string {
	return cache.Key("feed:content:global_timeline")
}

func ContentEntityKey(cache *rediscache.Client, postID uint64) string {
	return entitycache.ContentEntityKey(cache, postID)
}

func ContentFollowingCacheKey(cache *rediscache.Client, accountID uint64, limit int, cursor *PostCursor) string {
	var publishedAtUnixNano int64
	var postID uint64
	if cursor != nil {
		publishedAtUnixNano = cursor.PublishedAt.UTC().UnixNano()
		postID = cursor.ID
	}
	return cache.Key(
		"feed:content:following:accountID=%d:limit=%d:before=%d:%d",
		accountID,
		limit,
		publishedAtUnixNano,
		postID,
	)
}

func ContentFollowingCachePattern(cache *rediscache.Client, accountID uint64) string {
	return cache.Key("feed:content:following:accountID=%d:*", accountID)
}

func ContentPopularityWindowKey(cache *rediscache.Client, minute time.Time) string {
	return cache.Key("hot:content:1m:%s", minute.UTC().Truncate(time.Minute).Format("200601021504"))
}

func ContentPopularitySnapshotKey(cache *rediscache.Client, minute time.Time) string {
	return cache.Key("hot:content:snapshot:1m:%s", minute.UTC().Truncate(time.Minute).Format("200601021504"))
}

func ContentPopularityPaidWindowKey(cache *rediscache.Client, minute time.Time) string {
	return cache.Key("hot:content:paid:1m:%s", minute.UTC().Truncate(time.Minute).Format("200601021504"))
}
