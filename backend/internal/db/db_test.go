package db

import (
	"reflect"
	"sort"
	"testing"
)

func TestLegacyAutoMigrateModelsAreExplicitlyLimited(t *testing.T) {
	models := LegacyAutoMigrateModels()
	got := make([]string, 0, len(models))
	for _, model := range models {
		modelType := reflect.TypeOf(model)
		if modelType == nil || modelType.Kind() != reflect.Pointer || modelType.Elem().Kind() != reflect.Struct {
			t.Fatalf("legacy model must be pointer to struct, got %T", model)
		}
		got = append(got, modelType.Elem().PkgPath()+"."+modelType.Elem().Name())
	}
	sort.Strings(got)

	want := []string{
		"feedsystem_video_go/internal/account.Account",
		"feedsystem_video_go/internal/message.Message",
		"feedsystem_video_go/internal/social.Social",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LegacyAutoMigrateModels() = %#v, want %#v", got, want)
	}
}
