package main

import (
	"context"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	mqrabbit "feedsystem_video_go/internal/middleware/rabbitmq"
	"feedsystem_video_go/internal/observability"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/worker"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

func connectWithRetry(name string, maxRetries int, fn func() error) {
	for i := 0; i < maxRetries; i++ {
		if err := fn(); err == nil {
			return
		}
		wait := time.Duration(1<<i) * time.Second
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		log.Printf("%s 不可用，%v 后重试 (%d/%d)...", name, wait, i+1, maxRetries)
		time.Sleep(wait)
	}
	log.Fatalf("%s: 超过最大重试次数", name)
}

// runWorkerWithRetry 为每个 Worker 创建独立 Channel，断开后自动重连
func runWorkerWithRetry(ctx context.Context, name string, rmq *mqrabbit.RabbitMQ, fn func(*amqp.Channel) error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ch, err := rmq.NewChannel()
		if err != nil {
			log.Printf("%s: 创建 Channel 失败: %v, 5秒后重试", name, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if err := ch.Qos(50, 0, false); err != nil {
			log.Printf("%s: QoS 设置失败: %v", name, err)
		}

		log.Printf("%s started, consuming", name)
		if err := fn(ch); err != nil {
			if ctx.Err() != nil {
				ch.Close()
				return
			}
			log.Printf("%s: %v, 5秒后重连...", name, err)
		}
		ch.Close()
		time.Sleep(5 * time.Second)
	}
}

func main() {
	// 加载 .env（本地开发）
	if err := godotenv.Load(); err != nil {
		log.Println(".env not found; continuing")
	}
	// 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	log.Printf("Loading config from %s", configPath)
	cfg, usedDefault, err := config.LoadLocalDev(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if usedDefault {
		log.Printf("Config File %s not found, using default local config", configPath)
	} else {
		log.Printf("Config loaded from file: %s", configPath)
	}
	// 连接数据库（带重试）
	var sqlDB *gorm.DB
	connectWithRetry("MySQL", 10, func() error {
		var err error
		sqlDB, err = db.NewDB(cfg.Database)
		return err
	})
	defer db.CloseDB(sqlDB)

	// 连接 RabbitMQ（带重试）
	var rmq *mqrabbit.RabbitMQ
	connectWithRetry("RabbitMQ", 10, func() error {
		var err error
		rmq, err = mqrabbit.NewRabbitMQ(&cfg.RabbitMQ)
		return err
	})
	defer rmq.Close()

	// 用临时 Channel 声明拓扑（持久化队列，声明一次即可）
	topoCh, err := rmq.NewChannel()
	if err != nil {
		log.Fatalf("Failed to open topology channel: %v", err)
	}
	if err := declareSocialTopology(topoCh); err != nil {
		log.Fatalf("Failed to declare social topology: %v", err)
	}
	topoCh.Close()

	// 准备 repo
	socialRepo := social.NewSocialRepository(sqlDB)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pprofServer, err := observability.NewPprofServer(
		"Worker",
		cfg.ObservabilityConfig.Pprof.Enabled,
		cfg.ObservabilityConfig.Pprof.WorkerAddr,
	)
	if err != nil {
		log.Printf("Failed to start worker pprof server: %v", err)
	}
	if pprofServer != nil {
		defer pprofServer.Close()
	}

	// 每个 Worker 独立 Channel + 自动重连
	go runWorkerWithRetry(ctx, "SocialWorker", rmq, func(ch *amqp.Channel) error {
		reliable, cleanup, err := worker.NewRabbitReliableDeliveryHandler(sqlDB, rmq, "social-worker", mqrabbit.SocialQueue)
		if err != nil {
			return err
		}
		defer cleanup()
		return worker.NewSocialWorker(ch, socialRepo, mqrabbit.SocialQueue, reliable).Run(ctx)
	})
	// 等待退出信号
	<-ctx.Done()
	log.Printf("Worker shutting down...")
	time.Sleep(2 * time.Second) // 等待正在处理的消息完成
	log.Printf("Worker stopped")
}

func declareSocialTopology(ch *amqp.Channel) error {
	return mqrabbit.DeclareReliableTopic(ch, mqrabbit.SocialExchange, mqrabbit.SocialQueue, mqrabbit.SocialBindingKey, mqrabbit.DefaultRetryDelay)
}
