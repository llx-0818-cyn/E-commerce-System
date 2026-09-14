package interaction

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInteractionDomainDoesNotDependOnLegacyVideoPackage(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	directory := filepath.Dir(currentFile)
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatalf("glob interaction sources: %v", err)
	}
	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(filename), err)
		}
		text := string(data)
		if strings.Contains(text, "feedsystem_video_go/internal/video") || strings.Contains(text, "video.VideoRepository") {
			t.Fatalf("interaction domain source %s depends on legacy video package", filepath.Base(filename))
		}
	}
}
