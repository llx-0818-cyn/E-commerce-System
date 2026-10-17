package feed_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/content"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/interaction"
	"feedsystem_video_go/internal/migration"
	"feedsystem_video_go/migrations"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestFeedAssemblerUsesFixedBatchQueriesWithoutPerPostNPlusOne(t *testing.T) {
	adminDSN := os.Getenv("MIGRATION_TEST_DSN")
	if adminDSN == "" {
		t.Skip("设置 MIGRATION_TEST_DSN 后运行 FeedAssembler 查询计数集成测试")
	}
	database, cleanup := openFeedTestDatabase(t, adminDSN)
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
	createFollowingFixtures(t, database)

	base := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	seededIDs := make([]uint64, 0, 5)
	for index := 0; index < 5; index++ {
		authorID := uint64(1 + index%2)
		result, err := database.Exec(`
INSERT INTO posts (author_id,title,body,status,likes_count,favorites_count,comments_count,popularity,published_at)
VALUES (?, ?, '批量富化正文', 'published', ?, ?, ?, ?, ?)`,
			authorID,
			fmt.Sprintf("批量内容-%d", index),
			index+1,
			index+2,
			index+3,
			float64(index)+0.5,
			base.Add(time.Duration(index)*time.Second),
		)
		if err != nil {
			t.Fatalf("seed assembler post %d: %v", index, err)
		}
		postID, _ := result.LastInsertId()
		seededIDs = append(seededIDs, uint64(postID))
		seedAssemblerImages(t, database, uint64(postID), authorID)
	}
	const viewerID = uint64(4)
	if _, err := database.Exec("INSERT INTO tags (name) VALUES ('杯壶日常'), ('桌面好物')"); err != nil {
		t.Fatalf("seed feed tags: %v", err)
	}
	if _, err := database.Exec("INSERT INTO post_tags (post_id, tag_id) SELECT ?, id FROM tags WHERE name IN ('杯壶日常', '桌面好物')", seededIDs[4]); err != nil {
		t.Fatalf("seed feed post tags: %v", err)
	}
	if _, err := database.Exec("INSERT INTO post_likes (post_id, account_id) VALUES (?, ?), (?, ?)", seededIDs[4], viewerID, seededIDs[1], viewerID); err != nil {
		t.Fatalf("seed feed likes: %v", err)
	}
	if _, err := database.Exec("INSERT INTO post_favorites (post_id, account_id) VALUES (?, ?), (?, ?)", seededIDs[3], viewerID, seededIDs[1], viewerID); err != nil {
		t.Fatalf("seed feed favorites: %v", err)
	}

	counter := newSQLTraceCounter()
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: database}), &gorm.Config{Logger: counter})
	if err != nil {
		t.Fatalf("open counted GORM database: %v", err)
	}
	feedRepository := feed.NewFeedRepository(gormDB)
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
	assembler, err := feed.NewFeedAssembler(contentRepository, account.NewAccountRepository(gormDB), interactionService)
	if err != nil {
		t.Fatalf("NewFeedAssembler() error = %v", err)
	}

	counter.Reset()
	posts, err := feedRepository.ListPublishedPosts(ctx, 10, nil)
	if err != nil {
		t.Fatalf("ListPublishedPosts() error = %v", err)
	}
	items, err := assembler.Assemble(ctx, posts, viewerID)
	if err != nil {
		t.Fatalf("Assemble(authenticated) error = %v", err)
	}
	if got := counter.Count(); got != 6 {
		t.Fatalf("five-item feed query count = %d, want 6 (base + 5 fixed batches)", got)
	}
	wantOrder := reverseIDs(seededIDs)
	if got := contentFeedItemIDs(items); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("assembled order = %v, want %v", got, wantOrder)
	}
	for _, item := range items {
		if item.ID == seededIDs[4] && (len(item.Tags) != 2 || item.Tags[0].Name != "杯壶日常" || item.Tags[1].Name != "桌面好物") {
			t.Fatalf("post %d missing ordered feed tags: %#v", item.ID, item.Tags)
		}
		if item.ID != seededIDs[4] && (item.Tags == nil || len(item.Tags) != 0) {
			t.Fatalf("post %d should have an empty tag array: %#v", item.ID, item.Tags)
		}
		if item.Author.ID == 0 || item.Author.Username == "" {
			t.Fatalf("post %d missing author summary: %#v", item.ID, item.Author)
		}
		if len(item.Images) != 2 || item.Images[0].SortOrder == nil || *item.Images[0].SortOrder != 0 || item.Images[1].SortOrder == nil || *item.Images[1].SortOrder != 1 {
			t.Fatalf("post %d images are not batch-ordered: %#v", item.ID, item.Images)
		}
	}
	assertFeedViewerState(t, items, seededIDs[4], true, false)
	assertFeedViewerState(t, items, seededIDs[3], false, true)
	assertFeedViewerState(t, items, seededIDs[1], true, true)

	counter.Reset()
	onePost, err := feedRepository.ListPublishedPosts(ctx, 1, nil)
	if err != nil {
		t.Fatalf("ListPublishedPosts(one) error = %v", err)
	}
	if _, err := assembler.Assemble(ctx, onePost, viewerID); err != nil {
		t.Fatalf("Assemble(one authenticated) error = %v", err)
	}
	if got := counter.Count(); got != 6 {
		t.Fatalf("one-item feed query count = %d, want same fixed 6 as five-item page", got)
	}

	counter.Reset()
	anonymousPosts, err := feedRepository.ListPublishedPosts(ctx, 10, nil)
	if err != nil {
		t.Fatalf("ListPublishedPosts(anonymous) error = %v", err)
	}
	anonymousItems, err := assembler.Assemble(ctx, anonymousPosts, 0)
	if err != nil {
		t.Fatalf("Assemble(anonymous) error = %v", err)
	}
	if got := counter.Count(); got != 4 {
		t.Fatalf("anonymous feed query count = %d, want 4 (base + images + tags + authors)", got)
	}
	for _, item := range anonymousItems {
		if item.IsLiked || item.IsFavorited {
			t.Fatalf("anonymous post %d has active interaction state", item.ID)
		}
	}
}

func seedAssemblerImages(t *testing.T, database *sql.DB, postID, ownerID uint64) {
	t.Helper()
	for sortOrder := 1; sortOrder >= 0; sortOrder-- {
		if _, err := database.Exec(`
INSERT INTO post_images (owner_id,post_id,storage_key,image_url,mime_type,byte_size,width,height,sort_order,attached_at)
VALUES (?, ?, ?, ?, 'image/jpeg', 100, 640, 480, ?, ?)`,
			ownerID,
			postID,
			fmt.Sprintf("feed/%d-%d.jpg", postID, sortOrder),
			fmt.Sprintf("/static/feed/%d-%d.jpg", postID, sortOrder),
			sortOrder,
			time.Now().UTC(),
		); err != nil {
			t.Fatalf("seed post %d image %d: %v", postID, sortOrder, err)
		}
	}
}

func contentFeedItemIDs(items []feed.ContentFeedItem) []uint64 {
	ids := make([]uint64, len(items))
	for index := range items {
		ids[index] = items[index].ID
	}
	return ids
}

func assertFeedViewerState(t *testing.T, items []feed.ContentFeedItem, postID uint64, liked, favorited bool) {
	t.Helper()
	for _, item := range items {
		if item.ID == postID {
			if item.IsLiked != liked || item.IsFavorited != favorited {
				t.Fatalf("post %d states = liked:%v favorited:%v, want %v/%v", postID, item.IsLiked, item.IsFavorited, liked, favorited)
			}
			return
		}
	}
	t.Fatalf("post %d not found in assembled items", postID)
}

type sqlTraceCounter struct {
	gormlogger.Interface
	count atomic.Int64
}

func newSQLTraceCounter() *sqlTraceCounter {
	return &sqlTraceCounter{Interface: gormlogger.Discard}
}

func (c *sqlTraceCounter) LogMode(gormlogger.LogLevel) gormlogger.Interface { return c }

func (c *sqlTraceCounter) Trace(context.Context, time.Time, func() (string, int64), error) {
	c.count.Add(1)
}

func (c *sqlTraceCounter) Reset()       { c.count.Store(0) }
func (c *sqlTraceCounter) Count() int64 { return c.count.Load() }
