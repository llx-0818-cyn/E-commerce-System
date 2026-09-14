package content

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrImageTooLarge    = errors.New("image exceeds size limit")
	ErrCorruptImage     = errors.New("image is corrupt or unsupported")
	ErrUnsafeStorageKey = errors.New("unsafe image storage key")
	ErrImageInUse       = errors.New("image is attached to a post")
)

const (
	maximumImageDimension = 20_000
	maximumImagePixels    = 40_000_000
)

type StoredImage struct {
	StorageKey string
	ImageURL   string
	MIMEType   string
	ByteSize   uint64
	Width      uint
	Height     uint
}

type Storage interface {
	Save(ctx context.Context, ownerID uint64, filename string, source io.Reader) (StoredImage, error)
	Delete(ctx context.Context, storageKey string) error
}

type LocalStorage struct {
	root       string
	baseURL    string
	maxBytes   int64
	now        func() time.Time
	randomName func() (string, error)
}

func NewLocalStorage(root, baseURL string, maxBytes int64) (*LocalStorage, error) {
	root = strings.TrimSpace(root)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if root == "" || baseURL == "" || maxBytes <= 0 {
		return nil, errors.New("local image storage root/base URL/max bytes are required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve image storage root: %w", err)
	}
	return &LocalStorage{
		root:       absoluteRoot,
		baseURL:    baseURL,
		maxBytes:   maxBytes,
		now:        func() time.Time { return time.Now().UTC() },
		randomName: randomStorageName,
	}, nil
}

func (s *LocalStorage) Save(ctx context.Context, ownerID uint64, _ string, source io.Reader) (StoredImage, error) {
	if s == nil || s.root == "" || source == nil || ownerID == 0 {
		return StoredImage{}, errors.New("local image storage is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return StoredImage{}, ctx.Err()
	default:
	}

	data, err := io.ReadAll(io.LimitReader(source, s.maxBytes+1))
	if err != nil {
		return StoredImage{}, fmt.Errorf("read image upload: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return StoredImage{}, ErrImageTooLarge
	}
	if len(data) == 0 {
		return StoredImage{}, ErrCorruptImage
	}
	imageConfig, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || imageConfig.Width <= 0 || imageConfig.Height <= 0 {
		return StoredImage{}, ErrCorruptImage
	}
	if imageConfig.Width > maximumImageDimension || imageConfig.Height > maximumImageDimension || imageConfig.Width*imageConfig.Height > maximumImagePixels {
		return StoredImage{}, fmt.Errorf("%w: decoded dimensions exceed safety limit", ErrImageTooLarge)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return StoredImage{}, ErrCorruptImage
	}
	extension, mimeType, ok := imageFormatMetadata(format)
	if !ok {
		return StoredImage{}, ErrCorruptImage
	}

	now := s.now().UTC()
	directoryKey := filepath.ToSlash(filepath.Join("images", fmt.Sprintf("%d", ownerID), now.Format("2006"), now.Format("01")))
	directoryPath := filepath.Join(s.root, filepath.FromSlash(directoryKey))
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		return StoredImage{}, fmt.Errorf("create image directory: %w", err)
	}

	var storageKey, filePath string
	for attempt := 0; attempt < 3; attempt++ {
		name, err := s.randomName()
		if err != nil {
			return StoredImage{}, fmt.Errorf("generate image storage name: %w", err)
		}
		storageKey = directoryKey + "/" + name + extension
		filePath, err = s.resolveKey(storageKey)
		if err != nil {
			return StoredImage{}, err
		}
		file, openErr := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(openErr, os.ErrExist) {
			continue
		}
		if openErr != nil {
			return StoredImage{}, fmt.Errorf("create image file: %w", openErr)
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(filePath)
			if writeErr != nil {
				return StoredImage{}, fmt.Errorf("write image file: %w", writeErr)
			}
			return StoredImage{}, fmt.Errorf("close image file: %w", closeErr)
		}
		return StoredImage{
			StorageKey: storageKey,
			ImageURL:   s.baseURL + "/" + storageKey,
			MIMEType:   mimeType,
			ByteSize:   uint64(len(data)),
			Width:      uint(imageConfig.Width),
			Height:     uint(imageConfig.Height),
		}, nil
	}
	return StoredImage{}, errors.New("failed to allocate unique image path")
}

func (s *LocalStorage) Delete(ctx context.Context, storageKey string) error {
	if s == nil || s.root == "" {
		return errors.New("local image storage is not initialized")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	filePath, err := s.resolveKey(storageKey)
	if err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete image file: %w", err)
	}
	return nil
}

func (s *LocalStorage) resolveKey(storageKey string) (string, error) {
	storageKey = filepath.ToSlash(strings.TrimSpace(storageKey))
	if storageKey == "" || strings.HasPrefix(storageKey, "/") || !strings.HasPrefix(storageKey, "images/") {
		return "", ErrUnsafeStorageKey
	}
	cleanKey := filepath.Clean(filepath.FromSlash(storageKey))
	if cleanKey == "." || strings.HasPrefix(cleanKey, ".."+string(filepath.Separator)) || cleanKey == ".." {
		return "", ErrUnsafeStorageKey
	}
	filePath := filepath.Join(s.root, cleanKey)
	relative, err := filepath.Rel(s.root, filePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrUnsafeStorageKey
	}
	return filePath, nil
}

func randomStorageName() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func imageFormatMetadata(format string) (extension, mimeType string, ok bool) {
	switch format {
	case "jpeg":
		return ".jpg", "image/jpeg", true
	case "png":
		return ".png", "image/png", true
	case "gif":
		return ".gif", "image/gif", true
	default:
		return "", "", false
	}
}
