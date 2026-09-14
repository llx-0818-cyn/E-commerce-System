package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var migrationFilenamePattern = regexp.MustCompile(`^(\d{6})_([a-z0-9][a-z0-9_]*?)\.(up|down)\.sql$`)

var (
	ErrDirtyMigration        = errors.New("数据库存在 dirty migration")
	ErrIrreversibleMigration = errors.New("migration 不可回滚")
	ErrChecksumMismatch      = errors.New("已应用 migration checksum 不一致")
)

// Migration 是一对按版本排序的正向/回滚 SQL。
type Migration struct {
	Version  uint64
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

type migrationBuilder struct {
	version uint64
	name    string
	up      string
	down    string
}

// Load 从 fs.FS 读取并严格校验所有 migration 文件。
func Load(source fs.FS) ([]Migration, error) {
	if source == nil {
		return nil, errors.New("migration source is nil")
	}

	builders := make(map[uint64]*migrationBuilder)
	err := fs.WalkDir(source, ".", func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(filename) != ".sql" {
			return nil
		}

		base := path.Base(filename)
		parts := migrationFilenamePattern.FindStringSubmatch(base)
		if parts == nil {
			return fmt.Errorf("非法 migration 文件名 %q", filename)
		}
		version, parseErr := strconv.ParseUint(parts[1], 10, 64)
		if parseErr != nil || version == 0 {
			return fmt.Errorf("非法 migration 版本 %q", parts[1])
		}

		body, readErr := fs.ReadFile(source, filename)
		if readErr != nil {
			return fmt.Errorf("读取 migration %q: %w", filename, readErr)
		}
		sqlText := strings.TrimSpace(string(body))
		if sqlText == "" {
			return fmt.Errorf("migration %q 内容为空", filename)
		}

		builder, ok := builders[version]
		if !ok {
			builder = &migrationBuilder{version: version, name: parts[2]}
			builders[version] = builder
		} else if builder.name != parts[2] {
			return fmt.Errorf("migration 版本 %06d 使用了不同名称 %q 和 %q", version, builder.name, parts[2])
		}

		switch parts[3] {
		case "up":
			if builder.up != "" {
				return fmt.Errorf("migration %06d 存在重复 up 文件", version)
			}
			builder.up = sqlText
		case "down":
			if builder.down != "" {
				return fmt.Errorf("migration %06d 存在重复 down 文件", version)
			}
			builder.down = sqlText
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	versions := make([]uint64, 0, len(builders))
	for version := range builders {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	migrations := make([]Migration, 0, len(versions))
	for _, version := range versions {
		builder := builders[version]
		if builder.up == "" {
			return nil, fmt.Errorf("migration %06d_%s 缺少 up 文件", version, builder.name)
		}
		hash := sha256.Sum256([]byte(builder.name + "\x00" + builder.up + "\x00" + builder.down))
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     builder.name,
			UpSQL:    builder.up,
			DownSQL:  builder.down,
			Checksum: hex.EncodeToString(hash[:]),
		})
	}
	return migrations, nil
}
