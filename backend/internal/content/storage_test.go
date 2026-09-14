package content

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalStorageSavesDecodedImageWithRandomPathAndMetadata(t *testing.T) {
	root := t.TempDir()
	data := testPNG(t, 3, 2)
	storage, err := NewLocalStorage(root, "/static", int64(len(data)+10))
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	storage.now = func() time.Time { return time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC) }
	storage.randomName = func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }

	stored, err := storage.Save(context.Background(), 7, "../../client-name.exe", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if stored.StorageKey != "images/7/2026/08/0123456789abcdef0123456789abcdef.png" {
		t.Fatalf("storage key = %q", stored.StorageKey)
	}
	if stored.ImageURL != "/static/"+stored.StorageKey || stored.MIMEType != "image/png" || stored.Width != 3 || stored.Height != 2 || stored.ByteSize != uint64(len(data)) {
		t.Fatalf("stored image = %#v", stored)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(stored.StorageKey))); err != nil {
		t.Fatalf("stored file stat: %v", err)
	}
	if err := storage.Delete(context.Background(), stored.StorageKey); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(stored.StorageKey))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file stat error = %v, want not exist", err)
	}
}

func TestLocalStorageRejectsCorruptAndOversizedImages(t *testing.T) {
	valid := testPNG(t, 4, 4)
	tests := []struct {
		name     string
		maxBytes int64
		data     []byte
		want     error
	}{
		{"corrupt bytes", int64(len(valid) + 1), []byte("not-an-image"), ErrCorruptImage},
		{"truncated pixels", int64(len(valid) + 1), valid[:len(valid)-8], ErrCorruptImage},
		{"over byte limit", int64(len(valid) - 1), valid, ErrImageTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			storage, err := NewLocalStorage(root, "/static", test.maxBytes)
			if err != nil {
				t.Fatalf("NewLocalStorage() error = %v", err)
			}
			_, err = storage.Save(context.Background(), 1, "upload.png", bytes.NewReader(test.data))
			if !errors.Is(err, test.want) {
				t.Fatalf("Save() error = %v, want %v", err, test.want)
			}
			entries, readErr := os.ReadDir(root)
			if readErr != nil {
				t.Fatalf("ReadDir() error = %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("rejected upload left files: %v", entries)
			}
		})
	}
}

func TestLocalStorageRejectsTraversalKey(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "/static", 1024)
	if err != nil {
		t.Fatalf("NewLocalStorage() error = %v", err)
	}
	for _, key := range []string{"../secret", "/images/secret", "other/secret", "images/../../secret"} {
		if err := storage.Delete(context.Background(), key); !errors.Is(err, ErrUnsafeStorageKey) {
			t.Fatalf("Delete(%q) error = %v", key, err)
		}
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, color.RGBA{R: uint8(x + 10), G: uint8(y + 20), B: 30, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buffer.Bytes()
}

func TestImageFormatMetadata(t *testing.T) {
	if extension, mimeType, ok := imageFormatMetadata("png"); !ok || extension != ".png" || !strings.HasPrefix(mimeType, "image/") {
		t.Fatalf("imageFormatMetadata(png) = %q, %q, %v", extension, mimeType, ok)
	}
}
