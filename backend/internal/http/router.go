package http

import (
	"context"
	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/analytics"
	"feedsystem_video_go/internal/cacheinvalidation"
	"feedsystem_video_go/internal/checkout"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/content"
	domainevent "feedsystem_video_go/internal/event"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/message"
	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	"feedsystem_video_go/internal/middleware/ratelimit"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	orderdomain "feedsystem_video_go/internal/order"
	"feedsystem_video_go/internal/payment"
	"feedsystem_video_go/internal/profile"
	"feedsystem_video_go/internal/promotion"
	"feedsystem_video_go/internal/search"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/storefront"
	"feedsystem_video_go/internal/worker"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetRouter(db *gorm.DB, cache *rediscache.Client, rmq *rabbitmq.RabbitMQ, cfg config.Config) (*gin.Engine, error) {
	r := gin.Default()
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("SetTrustedProxies failed: %v", err)
	}
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.Static("/static", "./.run/uploads")
	// rate_limit
	loginLimiter := ratelimit.Limit(cache, "account_login", 10, time.Minute, ratelimit.KeyByIP)
	registerLimiter := ratelimit.Limit(cache, "account_register", 5, time.Hour, ratelimit.KeyByIP)

	likeLimiter := ratelimit.Limit(cache, "like_write", 30, time.Minute, ratelimit.KeyByAccount)
	commentLimiter := ratelimit.Limit(cache, "comment_write", 10, time.Minute, ratelimit.KeyByAccount)
	socialLimiter := ratelimit.Limit(cache, "social_write", 20, time.Minute, ratelimit.KeyByAccount)
	flashSaleLimiter := ratelimit.Limit(cache, "flash_sale_accept", 20, time.Minute, ratelimit.KeyByAccount)

	// account
	accountRepository := account.NewAccountRepository(db)
	accountService := account.NewAccountService(accountRepository, cache)
	accountHandler := account.NewAccountHandler(accountService)
	accountGroup := r.Group("/account")
	{
		accountGroup.POST("/register", registerLimiter, accountHandler.CreateAccount)
		accountGroup.POST("/login", loginLimiter, accountHandler.Login)
		accountGroup.POST("/changePassword", accountHandler.ChangePassword)
		accountGroup.POST("/findByID", accountHandler.FindByID)
		accountGroup.POST("/findByUsername", accountHandler.FindByUsername)
		accountGroup.POST("/refresh", accountHandler.Refresh)
	}
	protectedAccountGroup := accountGroup.Group("")
	protectedAccountGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedAccountGroup.POST("/logout", accountHandler.Logout)
		protectedAccountGroup.POST("/rename", accountHandler.Rename)
		protectedAccountGroup.POST("/uploadAvatar", accountHandler.UploadAvatar)
		protectedAccountGroup.POST("/updateProfile", accountHandler.UpdateProfile)
	}
	// content
	contentRepository, err := content.NewRepository(db)
	if err != nil {
		return nil, err
	}
	contentPublicReader, err := content.NewPublicEntityReader(contentRepository, cache)
	if err != nil {
		return nil, err
	}
	contentService, err := content.NewService(contentRepository, cfg.Content, contentPublicReader)
	if err != nil {
		return nil, err
	}
	imageStorage, err := content.NewLocalStorage("./.run/uploads", "/static", cfg.Content.MaxImageSizeBytes)
	if err != nil {
		return nil, err
	}
	imageService, err := content.NewImageService(contentRepository, imageStorage)
	if err != nil {
		return nil, err
	}
	behaviorRecorder, err := analytics.NewRecorder(db)
	if err != nil {
		return nil, err
	}
	contentHandler, err := content.NewHandler(contentService, imageService, behaviorRecorder)
	if err != nil {
		return nil, err
	}
	interactionRepository, err := interaction.NewRepository(db)
	if err != nil {
		return nil, err
	}
	interactionService, err := interaction.NewService(interactionRepository)
	if err != nil {
		return nil, err
	}
	interactionHandler, err := interaction.NewHandler(interactionService)
	if err != nil {
		return nil, err
	}
	storefrontRepository, err := storefront.NewRepository(db)
	if err != nil {
		return nil, err
	}
	storefrontPublicCache, err := storefront.NewPublicEntityCache(storefrontRepository, cache)
	if err != nil {
		return nil, err
	}
	storefrontService, err := storefront.NewService(storefrontRepository, storefrontPublicCache)
	if err != nil {
		return nil, err
	}
	storefrontHandler, err := storefront.NewHandler(storefrontService, behaviorRecorder)
	if err != nil {
		return nil, err
	}
	searchRepository, err := search.NewRepository(db)
	if err != nil {
		return nil, err
	}
	searchService, err := search.NewService(searchRepository, cfg.Search)
	if err != nil {
		return nil, err
	}
	searchHandler, err := search.NewHandler(searchService)
	if err != nil {
		return nil, err
	}
	elasticsearchAdapter, enhancedSearch := searchService.Elasticsearch()
	if enhancedSearch {
		contentService.EnableSearchIndexing()
		storefrontService.EnableSearchIndexing()
	}
	checkoutRepository, err := checkout.NewRepository(db)
	if err != nil {
		return nil, err
	}
	checkoutService, err := checkout.NewService(checkoutRepository, behaviorRecorder)
	if err != nil {
		return nil, err
	}
	checkoutHandler, err := checkout.NewHandler(checkoutService)
	if err != nil {
		return nil, err
	}
	orderRepository, err := orderdomain.NewRepository(db)
	if err != nil {
		return nil, err
	}
	orderService, err := orderdomain.NewService(orderRepository, cfg.Commerce.OrderPaymentValidFor.Value())
	if err != nil {
		return nil, err
	}
	orderHandler, err := orderdomain.NewHandler(orderService)
	if err != nil {
		return nil, err
	}
	mockPaymentProvider, err := payment.NewMockPaymentProvider("/api/mock-payments/cashier", cfg.MockPayment.CallbackSecret)
	if err != nil {
		return nil, err
	}
	paymentRepository, err := payment.NewRepository(db)
	if err != nil {
		return nil, err
	}
	paymentService, err := payment.NewService(paymentRepository, mockPaymentProvider, orderService)
	if err != nil {
		return nil, err
	}
	paymentHandler, err := payment.NewHandler(paymentService, mockPaymentProvider, cfg.Runtime.Environment, cfg.MockPayment.RoutesEnabled)
	if err != nil {
		return nil, err
	}
	promotionRepository, err := promotion.NewRepository(db)
	if err != nil {
		return nil, err
	}
	promotionService, err := promotion.NewService(promotionRepository, cache, orderService)
	if err != nil {
		return nil, err
	}
	promotionHandler, err := promotion.NewHandler(promotionService)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		go func() {
			if err := worker.RunFlashSaleReconciliation(context.Background(), promotionService, time.Minute, 100, func(reconcileErr error) {
				log.Printf("flash sale reconciliation failed: %v", reconcileErr)
			}); err != nil {
				log.Printf("flash sale reconciliation stopped: %v", err)
			}
		}()
	}
	go func() {
		if err := worker.RunOrderExpirationScanner(context.Background(), orderService, 30*time.Second, 100, func(scanErr error) {
			log.Printf("order expiration scan failed: %v", scanErr)
		}); err != nil {
			log.Printf("order expiration scanner stopped: %v", err)
		}
	}()
	apiGroup := r.Group("/api")
	{
		apiGroup.GET("/posts/:id", contentHandler.Detail)
		apiGroup.GET("/authors/:author_id/posts", contentHandler.ListByAuthor)
		apiGroup.GET("/shops/:id", storefrontHandler.GetShop)
		apiGroup.GET("/shops/:id/products", storefrontHandler.ListShopProducts)
		apiGroup.GET("/products/:id", storefrontHandler.GetProduct)
		apiGroup.GET("/products/:id/posts", storefrontHandler.ListProductPosts)
		apiGroup.GET("/posts/:id/comments", interactionHandler.ListComments)
		apiGroup.GET("/search", searchHandler.Search)
	}
	protectedAPIGroup := apiGroup.Group("")
	protectedAPIGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedAPIGroup.POST("/posts", contentHandler.Publish)
		protectedAPIGroup.PATCH("/posts/:id", contentHandler.Update)
		protectedAPIGroup.DELETE("/posts/:id", contentHandler.Delete)
		protectedAPIGroup.PATCH("/posts/:id/images/order", contentHandler.ReorderImages)
		protectedAPIGroup.POST("/images", contentHandler.UploadImage)
		protectedAPIGroup.DELETE("/images/:id", contentHandler.DeleteImage)
		protectedAPIGroup.PUT("/posts/:id/like", likeLimiter, interactionHandler.SetLike)
		protectedAPIGroup.PUT("/posts/:id/favorite", interactionHandler.SetFavorite)
		protectedAPIGroup.GET("/posts/:id/interaction", interactionHandler.State)
		protectedAPIGroup.GET("/favorites", interactionHandler.Favorites)
		protectedAPIGroup.POST("/posts/:id/comments", commentLimiter, interactionHandler.PublishComment)
		protectedAPIGroup.DELETE("/comments/:id", commentLimiter, interactionHandler.DeleteComment)
		protectedAPIGroup.POST("/shops", storefrontHandler.CreateShop)
		protectedAPIGroup.PATCH("/shops/:id", storefrontHandler.UpdateShop)
		protectedAPIGroup.PATCH("/shops/:id/status", storefrontHandler.SetShopStatus)
		protectedAPIGroup.POST("/seller/products", storefrontHandler.CreateProduct)
		protectedAPIGroup.GET("/seller/shop", storefrontHandler.GetOwnedShop)
		protectedAPIGroup.GET("/seller/products", storefrontHandler.ListSellerProducts)
		protectedAPIGroup.GET("/seller/dashboard", storefrontHandler.GetSellerDashboard)
		protectedAPIGroup.GET("/seller/products/:id", storefrontHandler.GetSellerProduct)
		protectedAPIGroup.PATCH("/seller/products/:id", storefrontHandler.UpdateProduct)
		protectedAPIGroup.PUT("/seller/products/:id/images", storefrontHandler.ReplaceProductImages)
		protectedAPIGroup.PATCH("/seller/products/:id/status", storefrontHandler.SetProductStatus)
		protectedAPIGroup.POST("/seller/products/:id/skus", storefrontHandler.CreateSKU)
		protectedAPIGroup.PATCH("/seller/skus/:id", storefrontHandler.UpdateSKU)
		protectedAPIGroup.PATCH("/seller/skus/:id/status", storefrontHandler.SetSKUStatus)
		protectedAPIGroup.POST("/seller/skus/:id/stock-adjustments", storefrontHandler.AdjustSKUStock)
		protectedAPIGroup.GET("/cart", checkoutHandler.ListCart)
		protectedAPIGroup.POST("/cart/items", checkoutHandler.AddCartItem)
		protectedAPIGroup.PATCH("/cart/items/:id", checkoutHandler.UpdateCartItem)
		protectedAPIGroup.DELETE("/cart/items/:id", checkoutHandler.DeleteCartItem)
		protectedAPIGroup.GET("/addresses", checkoutHandler.ListAddresses)
		protectedAPIGroup.POST("/addresses", checkoutHandler.CreateAddress)
		protectedAPIGroup.GET("/addresses/:id", checkoutHandler.GetAddress)
		protectedAPIGroup.PATCH("/addresses/:id", checkoutHandler.UpdateAddress)
		protectedAPIGroup.DELETE("/addresses/:id", checkoutHandler.DeleteAddress)
		protectedAPIGroup.PUT("/addresses/:id/default", checkoutHandler.SetDefaultAddress)
		protectedAPIGroup.POST("/checkout/preview", checkoutHandler.PreviewCheckout)
		protectedAPIGroup.POST("/orders", orderHandler.Create)
		protectedAPIGroup.GET("/orders", orderHandler.ListBuyer)
		protectedAPIGroup.GET("/orders/:id", orderHandler.Get)
		protectedAPIGroup.POST("/orders/:id/cancel", orderHandler.Cancel)
		protectedAPIGroup.POST("/orders/:id/confirm", orderHandler.Confirm)
		protectedAPIGroup.GET("/seller/orders", orderHandler.ListShop)
		protectedAPIGroup.POST("/seller/orders/:id/ship", orderHandler.Ship)
		protectedAPIGroup.POST("/payments", paymentHandler.CreatePayment)
		protectedAPIGroup.GET("/payments/:payment_no", paymentHandler.QueryPayment)
		protectedAPIGroup.POST("/flash-sales/:id/requests", flashSaleLimiter, promotionHandler.Accept)
		protectedAPIGroup.GET("/flash-sale-requests/:request_id", promotionHandler.Status)
		if cfg.MockPayment.RoutesEnabled {
			protectedAPIGroup.POST("/mock-payments/:payment_no/succeed", paymentHandler.SimulateSuccess)
			protectedAPIGroup.POST("/mock-payments/:payment_no/fail", paymentHandler.SimulateFailure)
			protectedAPIGroup.POST("/mock-payments/:payment_no/repeat", paymentHandler.RepeatCallback)
		}
	}
	// social
	socialMQ, err := rabbitmq.NewSocialMQ(rmq)
	if err != nil {
		log.Printf("SocialMQ init failed (mq disabled): %v", err)
		socialMQ = nil
	}
	socialRepository := social.NewSocialRepository(db)
	socialService := social.NewSocialService(socialRepository, accountRepository, socialMQ, cache)
	socialHandler := social.NewSocialHandler(socialService)
	authorProfileService, err := profile.NewService(accountRepository, socialRepository, contentRepository, interactionService)
	if err != nil {
		return nil, err
	}
	authorProfileHandler, err := profile.NewHandler(authorProfileService)
	if err != nil {
		return nil, err
	}
	authorProfileGroup := apiGroup.Group("")
	authorProfileGroup.Use(jwt.SoftJWTAuth(accountRepository, cache))
	authorProfileGroup.GET("/authors/:author_id/profile", authorProfileHandler.Get)
	socialGroup := r.Group("/social")
	protectedSocialGroup := socialGroup.Group("")
	protectedSocialGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedSocialGroup.POST("/follow", socialLimiter, socialHandler.Follow)
		protectedSocialGroup.POST("/unfollow", socialLimiter, socialHandler.Unfollow)
		protectedSocialGroup.POST("/getAllFollowers", socialHandler.GetAllFollowers)
		protectedSocialGroup.POST("/getAllVloggers", socialHandler.GetAllVloggers)
		protectedSocialGroup.POST("/getCounts", socialHandler.GetCounts)
	}

	// feed
	feedRepository := feed.NewFeedRepository(db, contentPublicReader)
	contentLatestService, err := feed.NewContentLatestService(feedRepository, cache)
	if err != nil {
		return nil, err
	}
	contentFollowingService, err := feed.NewContentFollowingService(feedRepository, cache)
	if err != nil {
		return nil, err
	}
	contentPopularityService, err := feed.NewContentPopularityService(feedRepository, cache, cfg.Popularity)
	if err != nil {
		return nil, err
	}
	contentTagService, err := feed.NewContentTagService(feedRepository)
	if err != nil {
		return nil, err
	}
	contentFeedAssembler, err := feed.NewFeedAssembler(contentRepository, accountRepository, interactionService, storefrontService)
	if err != nil {
		return nil, err
	}
	contentFeedHandler, err := feed.NewContentFeedHandler(
		contentLatestService,
		contentFollowingService,
		contentPopularityService,
		contentTagService,
		contentFeedAssembler,
		behaviorRecorder,
	)
	if err != nil {
		return nil, err
	}
	publicContentFeeds := apiGroup.Group("/feeds")
	publicContentFeeds.Use(jwt.SoftJWTAuth(accountRepository, cache))
	{
		publicContentFeeds.GET("/latest", contentFeedHandler.Latest)
		publicContentFeeds.GET("/popular", contentFeedHandler.Popular)
		publicContentFeeds.GET("/tags/:tag", contentFeedHandler.Tag)
	}
	protectedContentFeeds := apiGroup.Group("/feeds")
	protectedContentFeeds.Use(jwt.JWTAuth(accountRepository, cache))
	protectedContentFeeds.GET("/following", contentFeedHandler.Following)

	// message
	messageRepo := message.NewRepository(db)
	messageService := message.NewService(messageRepo)
	messageHandler := message.NewHandler(messageService)
	messageGroup := r.Group("/message")
	protectedMessageGroup := messageGroup.Group("")
	protectedMessageGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedMessageGroup.POST("/send", messageHandler.Send)
		protectedMessageGroup.POST("/list", messageHandler.List)
	}
	// SSE notification
	if rmq != nil {
		if notifCh, err := rmq.NewChannel(); err == nil {
			if err := rabbitmq.DeclareContentTopologies(notifCh, rabbitmq.DefaultRetryDelay); err != nil {
				log.Printf("content event topology init failed: %v", err)
			}
			if err := rabbitmq.DeclareBehaviorTopology(notifCh, rabbitmq.DefaultRetryDelay); err != nil {
				log.Printf("behavior event topology init failed: %v", err)
			}
			if err := rabbitmq.DeclareCacheInvalidationTopology(notifCh, rabbitmq.DefaultRetryDelay); err != nil {
				log.Printf("cache invalidation topology init failed: %v", err)
			}
			if err := rabbitmq.DeclareSearchIndexTopology(notifCh, rabbitmq.DefaultRetryDelay); err != nil {
				log.Printf("search index topology init failed: %v", err)
			}
			if err := rabbitmq.DeclareOrderTopologies(notifCh, cfg.Commerce.OrderPaymentValidFor.Value(), rabbitmq.DefaultRetryDelay); err != nil {
				log.Printf("order event topology init failed: %v", err)
			}
			if err := rabbitmq.DeclareSeckillTopology(notifCh, rabbitmq.DefaultRetryDelay); err != nil {
				log.Printf("seckill event topology init failed: %v", err)
			}
			if err := rabbitmq.DeclareTopic(notifCh, rabbitmq.SocialExchange, rabbitmq.NotificationSocialQueue, "social.followed"); err != nil {
				log.Printf("notification social topic init failed: %v", err)
			}
			notifCh.Close()
		}
	}
	sseHub := worker.NewSSEHub(db)
	notifGroup := r.Group("/notification")
	notifGroup.Use(sseHub.SSERequireAuth())
	sseHub.RegisterRoutes(r, notifGroup)

	if rmq != nil {
		if outboxPublisher, err := rabbitmq.NewRoutedOutboxPublisher(rmq, rabbitmq.ContentExchange, []rabbitmq.EventExchangeRoute{
			{EventTypePrefix: "behavior.", Exchange: rabbitmq.BehaviorExchange},
			{EventTypePrefix: "cache.", Exchange: rabbitmq.CacheExchange},
			{EventTypePrefix: "order.", Exchange: rabbitmq.OrderExchange},
			{EventTypePrefix: "search.", Exchange: rabbitmq.SearchExchange},
			{EventTypePrefix: "seckill.", Exchange: rabbitmq.SeckillExchange},
		}); err != nil {
			log.Printf("content outbox publisher init failed: %v", err)
		} else if poller, err := domainevent.NewPoller(db, outboxPublisher, domainevent.DefaultPollerConfig("api-content-outbox")); err != nil {
			log.Printf("content outbox poller init failed: %v", err)
		} else {
			go func() {
				for {
					if err := poller.Run(context.Background()); err != nil {
						log.Printf("content outbox poller stopped: %v; 5秒后重启", err)
						time.Sleep(5 * time.Second)
					}
				}
			}()
		}

		orderLifecycleProcessor, lifecycleErr := worker.NewOrderLifecycleProcessor(behaviorRecorder)
		orderExpirationProcessor, expirationErr := worker.NewOrderExpirationProcessor(orderService)
		if lifecycleErr != nil || expirationErr != nil {
			log.Printf("order workers init failed: lifecycle=%v expiration=%v", lifecycleErr, expirationErr)
		} else {
			go func() {
				for {
					channel, err := rmq.NewChannel()
					if err != nil {
						log.Printf("order lifecycle channel failed: %v", err)
						time.Sleep(5 * time.Second)
						continue
					}
					reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "order-lifecycle-worker", rabbitmq.OrderLifecycleQueue)
					if err != nil {
						channel.Close()
						log.Printf("order lifecycle reliable handler failed: %v", err)
						time.Sleep(5 * time.Second)
						continue
					}
					if err := worker.NewOrderLifecycleWorker(channel, reliable, orderLifecycleProcessor).Run(context.Background()); err != nil {
						log.Printf("order lifecycle worker stopped: %v", err)
					}
					cleanup()
					channel.Close()
					time.Sleep(5 * time.Second)
				}
			}()
			go func() {
				for {
					channel, err := rmq.NewChannel()
					if err != nil {
						log.Printf("order expiration channel failed: %v", err)
						time.Sleep(5 * time.Second)
						continue
					}
					reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "order-expiration-worker", rabbitmq.OrderExpirationQueue)
					if err != nil {
						channel.Close()
						log.Printf("order expiration reliable handler failed: %v", err)
						time.Sleep(5 * time.Second)
						continue
					}
					if err := worker.NewOrderExpirationWorker(channel, reliable, orderExpirationProcessor).Run(context.Background()); err != nil {
						log.Printf("order expiration worker stopped: %v", err)
					}
					cleanup()
					channel.Close()
					time.Sleep(5 * time.Second)
				}
			}()
		}

		go func() {
			for {
				channel, err := rmq.NewChannel()
				if err != nil {
					log.Printf("seckill order channel failed: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "seckill-order-worker", rabbitmq.SeckillOrderQueue)
				if err != nil {
					channel.Close()
					log.Printf("seckill order reliable handler failed: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				if err := worker.NewSeckillOrderWorker(channel, promotionService, reliable).Run(context.Background()); err != nil {
					log.Printf("seckill order worker stopped: %v", err)
				}
				cleanup()
				channel.Close()
				time.Sleep(5 * time.Second)
			}
		}()
		go func() {
			for {
				channel, err := rmq.NewChannel()
				if err != nil {
					log.Printf("seckill compensation channel failed: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "seckill-compensation-worker", rabbitmq.SeckillCompensationQueue)
				if err != nil {
					channel.Close()
					log.Printf("seckill compensation reliable handler failed: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				if err := worker.NewSeckillCompensationWorker(channel, promotionService, reliable).Run(context.Background()); err != nil {
					log.Printf("seckill compensation worker stopped: %v", err)
				}
				cleanup()
				channel.Close()
				time.Sleep(5 * time.Second)
			}
		}()
		go func() {
			for {
				channel, err := rmq.NewChannel()
				if err != nil {
					log.Printf("seckill lifecycle channel failed: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "seckill-order-lifecycle-worker", rabbitmq.SeckillLifecycleQueue)
				if err != nil {
					channel.Close()
					log.Printf("seckill lifecycle reliable handler failed: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				if err := worker.NewSeckillOrderLifecycleWorker(channel, promotionService, reliable).Run(context.Background()); err != nil {
					log.Printf("seckill lifecycle worker stopped: %v", err)
				}
				cleanup()
				channel.Close()
				time.Sleep(5 * time.Second)
			}
		}()

		if cache != nil {
			cacheProcessor, err := cacheinvalidation.NewProcessor(cacheinvalidation.Composite{contentPublicReader, storefrontPublicCache})
			if err != nil {
				log.Printf("cache invalidation processor init failed: %v", err)
			} else {
				go func() {
					for {
						channel, err := rmq.NewChannel()
						if err != nil {
							log.Printf("cache invalidation channel failed: %v", err)
							time.Sleep(5 * time.Second)
							continue
						}
						reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "cache-invalidation-worker", rabbitmq.CacheInvalidationQueue)
						if err != nil {
							channel.Close()
							log.Printf("cache invalidation reliable handler failed: %v", err)
							time.Sleep(5 * time.Second)
							continue
						}
						if err := worker.NewCacheInvalidationWorker(channel, cacheProcessor, reliable).Run(context.Background()); err != nil {
							log.Printf("cache invalidation worker stopped: %v", err)
						}
						cleanup()
						channel.Close()
						time.Sleep(5 * time.Second)
					}
				}()
			}

			if processor, err := worker.NewContentTimelineProcessor(cache); err != nil {
				log.Printf("content timeline processor init failed: %v", err)
			} else {
				go func() {
					for {
						channel, err := rmq.NewChannel()
						if err != nil {
							log.Printf("content timeline channel failed: %v", err)
							time.Sleep(5 * time.Second)
							continue
						}
						reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "content-timeline-worker", rabbitmq.ContentTimelineQueue)
						if err != nil {
							channel.Close()
							log.Printf("content timeline reliable handler failed: %v", err)
							time.Sleep(5 * time.Second)
							continue
						}
						contentWorker := worker.NewContentTimelineWorker(channel, rabbitmq.ContentTimelineQueue, processor, reliable)
						if err := contentWorker.Run(context.Background()); err != nil {
							log.Printf("content timeline worker stopped: %v", err)
						}
						cleanup()
						channel.Close()
						time.Sleep(5 * time.Second)
					}
				}()
			}
		}

		if enhancedSearch {
			searchConsumer, err := search.NewIndexConsumer(searchRepository, elasticsearchAdapter)
			if err != nil {
				log.Printf("search index consumer init failed: %v", err)
			} else {
				go func() {
					for {
						channel, err := rmq.NewChannel()
						if err != nil {
							log.Printf("search index channel failed: %v", err)
							time.Sleep(5 * time.Second)
							continue
						}
						reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "search-index-worker", rabbitmq.SearchIndexQueue)
						if err != nil {
							channel.Close()
							log.Printf("search index reliable handler failed: %v", err)
							time.Sleep(5 * time.Second)
							continue
						}
						if err := worker.NewSearchIndexWorker(channel, searchConsumer, reliable).Run(context.Background()); err != nil {
							log.Printf("search index worker stopped: %v", err)
						}
						cleanup()
						channel.Close()
						time.Sleep(5 * time.Second)
					}
				}()
			}
		}

		if cache != nil {
			calculator, calculatorErr := analytics.NewPopularityCalculator(cfg.Popularity)
			windowWriter, writerErr := feed.NewContentPopularityRecorder(cache)
			var popularityAggregator *analytics.PopularityAggregator
			var aggregatorErr error
			if calculatorErr == nil && writerErr == nil {
				popularityAggregator, aggregatorErr = analytics.NewPopularityAggregator(calculator, windowWriter)
			}
			if calculatorErr != nil || writerErr != nil || aggregatorErr != nil {
				log.Printf("content commerce popularity init failed: calculator=%v writer=%v aggregator=%v", calculatorErr, writerErr, aggregatorErr)
			} else {
				behaviorConsumer, consumerErr := analytics.NewConsumer(popularityAggregator)
				if consumerErr != nil {
					log.Printf("behavior popularity consumer init failed: %v", consumerErr)
				} else {
					go func() {
						for {
							channel, err := rmq.NewChannel()
							if err != nil {
								log.Printf("content popularity channel failed: %v", err)
								time.Sleep(5 * time.Second)
								continue
							}
							reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "content-popularity-worker", rabbitmq.ContentPopularityQueue)
							if err != nil {
								channel.Close()
								log.Printf("content popularity reliable handler failed: %v", err)
								time.Sleep(5 * time.Second)
								continue
							}
							contentPopularityWorker := worker.NewContentPopularityWorker(channel, rabbitmq.ContentPopularityQueue, popularityAggregator, reliable)
							if err := contentPopularityWorker.Run(context.Background()); err != nil {
								log.Printf("content popularity worker stopped: %v", err)
							}
							cleanup()
							channel.Close()
							time.Sleep(5 * time.Second)
						}
					}()
					go func() {
						for {
							channel, err := rmq.NewChannel()
							if err != nil {
								log.Printf("behavior popularity channel failed: %v", err)
								time.Sleep(5 * time.Second)
								continue
							}
							reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "behavior-popularity-worker", rabbitmq.BehaviorQueue)
							if err != nil {
								channel.Close()
								log.Printf("behavior popularity reliable handler failed: %v", err)
								time.Sleep(5 * time.Second)
								continue
							}
							behaviorPopularityWorker := worker.NewBehaviorPopularityWorker(channel, rabbitmq.BehaviorQueue, behaviorConsumer, reliable)
							if err := behaviorPopularityWorker.Run(context.Background()); err != nil {
								log.Printf("behavior popularity worker stopped: %v", err)
							}
							cleanup()
							channel.Close()
							time.Sleep(5 * time.Second)
						}
					}()
				}
			}
		}

		if consumer, err := worker.NewContentNotificationConsumer(db, sseHub); err != nil {
			log.Printf("content notification consumer init failed: %v", err)
		} else {
			go func() {
				for {
					channel, err := rmq.NewChannel()
					if err != nil {
						log.Printf("content notification channel failed: %v", err)
						time.Sleep(5 * time.Second)
						continue
					}
					reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, "content-notification-worker", rabbitmq.ContentNotificationQueue)
					if err != nil {
						channel.Close()
						log.Printf("content notification reliable handler failed: %v", err)
						time.Sleep(5 * time.Second)
						continue
					}
					contentWorker := worker.NewContentNotificationWorker(channel, rabbitmq.ContentNotificationQueue, consumer, reliable)
					if err := contentWorker.Run(context.Background()); err != nil {
						log.Printf("content notification worker stopped: %v", err)
					}
					cleanup()
					channel.Close()
					time.Sleep(5 * time.Second)
				}
			}()
		}
	}

	go func() {
		if rmq != nil {
			hub := sseHub
			ctx := context.Background()
			// 关注通知使用独立 Channel 并在断线后自动重连。
			for _, q := range []string{rabbitmq.NotificationSocialQueue} {
				go func(queue string) {
					for {
						ch, err := rmq.NewChannel()
						if err != nil {
							log.Printf("notification-%s: 创建 Channel 失败: %v, 5秒后重试", queue, err)
							time.Sleep(5 * time.Second)
							continue
						}
						reliable, cleanupReliable, err := worker.NewRabbitReliableDeliveryHandler(db, rmq, queue+".worker", queue)
						if err != nil {
							ch.Close()
							log.Printf("notification-%s: 创建可靠失败链路失败: %v, 5秒后重试", queue, err)
							time.Sleep(5 * time.Second)
							continue
						}
						w := worker.NewNotificationWorker(ch, db, queue, hub, reliable)
						if err := w.Run(ctx); err != nil {
							log.Printf("notification-%s: %v, 5秒后重连...", queue, err)
						}
						cleanupReliable()
						ch.Close()
						time.Sleep(5 * time.Second)
					}
				}(q)
			}
		} else {
			log.Printf("Notification SSE disabled (MQ not available)")
		}
	}()

	return r, nil
}
