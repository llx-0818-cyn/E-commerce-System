package content

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"feedsystem_video_go/internal/config"
)

func TestValidatePostInputSupportsTextAndRejectsInvalidImages(t *testing.T) {
	service := &Service{limits: config.ContentConfig{MaxImages: 2, MaxImageSizeBytes: 1024, MaxBodyLength: 10}}
	if err := service.validatePostInput(1, "标题", "纯文字正文", nil); err != nil {
		t.Fatalf("pure text validate error = %v", err)
	}
	if err := service.validatePostInput(1, "标题", "正文", []uint64{1, 1}); !errors.Is(err, ErrPostValidation) {
		t.Fatalf("duplicate images error = %v", err)
	}
	if err := service.validatePostInput(1, "标题", "正文", []uint64{1, 2, 3}); !errors.Is(err, ErrPostValidation) {
		t.Fatalf("too many images error = %v", err)
	}
	if err := service.validatePostInput(1, "标题", strings.Repeat("文", 11), nil); !errors.Is(err, ErrPostValidation) {
		t.Fatalf("long body error = %v", err)
	}
}

func TestNormalizeTagsExtractsChineseEnglishAndDeduplicates(t *testing.T) {
	got, err := normalizeTags("#Go 图文 #电商", "继续讨论 #go、#Redis 和 #电商", []string{"Go", "#缓存", "redis"})
	if err != nil {
		t.Fatalf("normalizeTags() error = %v", err)
	}
	want := []string{"go", "电商", "redis", "缓存"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeTags() = %#v, want %#v", got, want)
	}
	if _, err := normalizeTags("标题", "正文", []string{"invalid tag"}); !errors.Is(err, ErrTagValidation) {
		t.Fatalf("invalid tag error = %v", err)
	}
}
