package rabbitmq

// v2 Queue 与旧版仅配置最终 DLX 的 Queue 并行，避免 RabbitMQ 因不可变参数
// x-dead-letter-exchange 变化而拒绝声明。Exchange 保持稳定，发布者/消费者在
// 同一次发布中切换到 v2 Queue；旧 Queue 留待部署清理任务审计后移除。
const (
	SocialExchange   = "social.events"
	SocialQueue      = "social.events.v2"
	SocialBindingKey = "social.*"

	NotificationSocialQueue = "notification.social.v2"

	ContentExchange          = "content.events"
	ContentPublishedKey      = "content.published"
	ContentLikedKey          = "content.liked"
	ContentFavoritedKey      = "content.favorited"
	ContentCommentCreatedKey = "content.comment.created"
	ContentTimelineQueue     = "content.timeline.v1"
	ContentNotificationQueue = "notification.content.v1"
	ContentPopularityQueue   = "content.popularity.v1"

	CacheExchange          = "cache.events"
	CacheInvalidationKey   = "cache.invalidate.requested"
	CacheInvalidationQueue = "cache.invalidation.v1"

	SearchExchange          = "search.events"
	SearchIndexRequestedKey = "search.index.requested"
	SearchIndexQueue        = "search.index.v1"

	BehaviorExchange   = "behavior.events"
	BehaviorQueue      = "behavior.statistics.v1"
	BehaviorBindingKey = "behavior.#"

	OrderExchange             = "order.events"
	OrderCreatedKey           = "order.created"
	OrderPaidKey              = "order.paid"
	OrderCanceledKey          = "order.canceled"
	OrderShippedKey           = "order.shipped"
	OrderCompletedKey         = "order.completed"
	OrderExpireRequestedKey   = "order.expire.requested"
	OrderExpireDueKey         = "order.expire.due"
	OrderLifecycleQueue       = "order.lifecycle.v1"
	OrderExpirationDelayQueue = "order.expiration.delay.v1"
	OrderExpirationQueue      = "order.expiration.due.v1"

	SeckillExchange          = "seckill.events"
	SeckillOrderQueue        = "seckill.order.requests.v1"
	SeckillOrderRequestedKey = "seckill.order.requested"
	SeckillCompensationQueue = "seckill.order.compensation.v1"
	SeckillOrderFailedKey    = "seckill.order.failed"
	SeckillLifecycleQueue    = "seckill.order.lifecycle.v1"
)
