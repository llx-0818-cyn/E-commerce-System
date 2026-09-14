package profile_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/internal/profile"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/migrations"

	mysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAuthorContentProfileCountsViewerStatesAndPublicPosts(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行作者主页集成测试")
	}
	database, cleanup := openProfileTestDatabase(t, adminDSN)
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
	if _, err := database.Exec(`
CREATE TABLE accounts (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL DEFAULT '',
    token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    avatar_url VARCHAR(512) NOT NULL DEFAULT '',
    bio VARCHAR(255) NOT NULL DEFAULT ''
) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create accounts fixture: %v", err)
	}
	if _, err := database.Exec(`
CREATE TABLE socials (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    follower_id BIGINT UNSIGNED NOT NULL,
    vlogger_id BIGINT UNSIGNED NOT NULL,
    UNIQUE KEY uq_social_fixture (follower_id, vlogger_id)
) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create socials fixture: %v", err)
	}
	for id, username := range map[uint64]string{1: "author", 2: "viewer"} {
		if _, err := database.Exec("INSERT INTO accounts (id, username, token, refresh_token) VALUES (?, ?, '', '')", id, username); err != nil {
			t.Fatalf("seed account %d: %v", id, err)
		}
	}
	if _, err := database.Exec("INSERT INTO socials (follower_id, vlogger_id) VALUES (2, 1)"); err != nil {
		t.Fatalf("seed follow relation: %v", err)
	}
	post1 := seedProfilePost(t, database, "公开一", "published", 3, nil)
	post2 := seedProfilePost(t, database, "公开二", "published", 5, nil)
	deletedAt := time.Now().UTC()
	_ = seedProfilePost(t, database, "已删除", "deleted", 100, &deletedAt)
	_ = seedProfilePost(t, database, "已隐藏", "hidden", 100, nil)
	if _, err := database.Exec("INSERT INTO post_favorites (post_id, account_id) VALUES (?, 2)", post2); err != nil {
		t.Fatalf("seed favorite relation: %v", err)
	}

	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM database: %v", err)
	}
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
	service, err := profile.NewService(
		account.NewAccountRepository(gormDB),
		social.NewSocialRepository(gormDB),
		contentRepository,
		interactionService,
	)
	if err != nil {
		t.Fatalf("New profile service: %v", err)
	}

	viewerProfile, err := service.Get(ctx, 1, 2)
	if err != nil {
		t.Fatalf("Get(viewer) error = %v", err)
	}
	if viewerProfile.ContentCount != 2 || viewerProfile.ReceivedLikesCount != 8 || !viewerProfile.IsFollowing || len(viewerProfile.Posts) != 2 {
		t.Fatalf("viewer profile = %#v", viewerProfile)
	}
	favoriteByID := make(map[uint64]bool)
	for _, post := range viewerProfile.Posts {
		favoriteByID[post.ID] = post.IsFavorited
	}
	if favoriteByID[post1] || !favoriteByID[post2] {
		t.Fatalf("favorite states = %#v", favoriteByID)
	}

	anonymousProfile, err := service.Get(ctx, 1, 0)
	if err != nil {
		t.Fatalf("Get(anonymous) error = %v", err)
	}
	if anonymousProfile.IsFollowing {
		t.Fatal("anonymous profile unexpectedly following author")
	}
	for _, post := range anonymousProfile.Posts {
		if post.IsFavorited {
			t.Fatalf("anonymous post %d unexpectedly favorited", post.ID)
		}
	}
}

func seedProfilePost(t *testing.T, database *sql.DB, title, status string, likes uint64, deletedAt *time.Time) uint64 {
	t.Helper()
	publishedAt := any(time.Now().UTC())
	if status != "published" {
		publishedAt = nil
	}
	result, err := database.Exec(`
INSERT INTO posts (author_id, title, body, status, likes_count, published_at, deleted_at)
VALUES (1, ?, '作者主页正文', ?, ?, ?, ?)`, title, status, likes, publishedAt, deletedAt)
	if err != nil {
		t.Fatalf("seed profile post %q: %v", title, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("profile post LastInsertId: %v", err)
	}
	return uint64(id)
}

func openProfileTestDatabase(t *testing.T, adminDSN string) (*sql.DB, func()) {
	t.Helper()
	adminConfig, err := mysql.ParseDSN(adminDSN)
	if err != nil {
		t.Fatalf("parse MIGRATION_TEST_DSN: %v", err)
	}
	adminConfig.DBName = ""
	adminConfig.MultiStatements = true
	adminConfig.ParseTime = true
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	databaseName := fmt.Sprintf("feedsystem_profile_test_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		adminDB.Close()
		t.Fatalf("create temporary database: %v", err)
	}
	testConfig := *adminConfig
	testConfig.DBName = databaseName
	database, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		_, _ = adminDB.Exec("DROP DATABASE `" + databaseName + "`")
		adminDB.Close()
		t.Fatalf("open temporary database: %v", err)
	}
	cleanup := func() {
		database.Close()
		if _, err := adminDB.Exec("DROP DATABASE `" + databaseName + "`"); err != nil {
			t.Errorf("drop temporary database: %v", err)
		}
		adminDB.Close()
	}
	return database, cleanup
}
