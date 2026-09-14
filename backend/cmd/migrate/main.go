package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: go run ./cmd/migrate <status|up|down> [--steps N]")
	}
	_ = godotenv.Load()

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	cfg, usedDefault, err := config.LoadLocalDev(configPath)
	if err != nil {
		return err
	}
	if usedDefault {
		log.Printf("配置文件 %s 不存在，使用本地默认配置", configPath)
	}

	database, err := openDatabase(cfg.Database)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		return fmt.Errorf("连接 migration 数据库: %w", err)
	}

	runner, err := migration.NewRunner(database, migrations.Files)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch args[0] {
	case "status":
		statusFlags := flag.NewFlagSet("status", flag.ContinueOnError)
		if err := statusFlags.Parse(args[1:]); err != nil {
			return err
		}
		if statusFlags.NArg() != 0 {
			return fmt.Errorf("status 不接受位置参数")
		}
		statuses, err := runner.Status(ctx)
		if err != nil {
			return err
		}
		for _, item := range statuses {
			state := "pending"
			if item.Dirty {
				state = "dirty"
			} else if item.Applied {
				state = "applied"
			}
			fmt.Printf("%06d %-40s %s\n", item.Version, item.Name, state)
		}
		return nil
	case "up":
		upFlags := flag.NewFlagSet("up", flag.ContinueOnError)
		if err := upFlags.Parse(args[1:]); err != nil {
			return err
		}
		if upFlags.NArg() != 0 {
			return fmt.Errorf("up 不接受位置参数")
		}
		count, err := runner.Up(ctx)
		if err != nil {
			return err
		}
		log.Printf("migration up 完成，本次应用 %d 个版本", count)
		return nil
	case "down":
		downFlags := flag.NewFlagSet("down", flag.ContinueOnError)
		steps := downFlags.Int("steps", 1, "回滚版本数")
		if err := downFlags.Parse(args[1:]); err != nil {
			return err
		}
		if downFlags.NArg() != 0 {
			return fmt.Errorf("down 不接受位置参数")
		}
		count, err := runner.Down(ctx, *steps)
		if err != nil {
			return err
		}
		log.Printf("migration down 完成，本次回滚 %d 个版本", count)
		return nil
	default:
		return fmt.Errorf("未知 migration 命令 %q；可选 status、up、down", args[0])
	}
}

func openDatabase(cfg config.DatabaseConfig) (*sql.DB, error) {
	mysqlConfig := mysql.NewConfig()
	mysqlConfig.Net = "tcp"
	mysqlConfig.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	mysqlConfig.User = cfg.User
	mysqlConfig.Passwd = cfg.Password
	mysqlConfig.DBName = cfg.DBName
	mysqlConfig.ParseTime = true
	mysqlConfig.Loc = time.Local
	mysqlConfig.Collation = "utf8mb4_0900_ai_ci"
	mysqlConfig.MultiStatements = true

	database, err := sql.Open("mysql", mysqlConfig.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("打开 migration 数据库: %w", err)
	}
	return database, nil
}
