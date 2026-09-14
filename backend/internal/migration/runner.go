package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

const migrationLockName = "feedsystem_content_commerce_migrations"

const createMigrationsTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    checksum CHAR(64) NOT NULL,
    dirty BOOLEAN NOT NULL DEFAULT TRUE,
    applied_at TIMESTAMP(6) NULL,
    PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`

// AppliedMigration 是数据库中已记录的版本状态。
type AppliedMigration struct {
	Version   uint64
	Name      string
	Checksum  string
	Dirty     bool
	AppliedAt sql.NullTime
}

// Status 描述一个源码版本与数据库状态的对应关系。
type Status struct {
	Version   uint64
	Name      string
	Checksum  string
	Applied   bool
	Dirty     bool
	AppliedAt *time.Time
}

// Runner 串行执行嵌入式 SQL migration，并检测已应用文件漂移。
type Runner struct {
	db         *sql.DB
	migrations []Migration
	lockWait   time.Duration
}

func NewRunner(db *sql.DB, source fs.FS) (*Runner, error) {
	if db == nil {
		return nil, errors.New("migration database is nil")
	}
	migrations, err := Load(source)
	if err != nil {
		return nil, err
	}
	return &Runner{db: db, migrations: migrations, lockWait: 15 * time.Second}, nil
}

// Up 应用所有尚未执行的 migration，返回本次应用数量。
func (r *Runner) Up(ctx context.Context) (int, error) {
	appliedCount := 0
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		if err := ensureMigrationsTable(ctx, conn); err != nil {
			return err
		}
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateApplied(r.migrations, applied); err != nil {
			return err
		}

		for _, item := range r.migrations {
			if _, ok := applied[item.Version]; ok {
				continue
			}
			if _, err := conn.ExecContext(
				ctx,
				"INSERT INTO schema_migrations (version, name, checksum, dirty, applied_at) VALUES (?, ?, ?, TRUE, NULL)",
				item.Version,
				item.Name,
				item.Checksum,
			); err != nil {
				return fmt.Errorf("记录 migration %06d dirty 状态: %w", item.Version, err)
			}
			if _, err := conn.ExecContext(ctx, item.UpSQL); err != nil {
				return fmt.Errorf("执行 migration %06d_%s up（已标记 dirty）: %w", item.Version, item.Name, err)
			}
			if _, err := conn.ExecContext(
				ctx,
				"UPDATE schema_migrations SET dirty = FALSE, applied_at = CURRENT_TIMESTAMP(6) WHERE version = ? AND dirty = TRUE",
				item.Version,
			); err != nil {
				return fmt.Errorf("完成 migration %06d 状态: %w", item.Version, err)
			}
			appliedCount++
		}
		return nil
	})
	return appliedCount, err
}

// Down 按版本倒序回滚指定步数，返回实际回滚数量。
func (r *Runner) Down(ctx context.Context, steps int) (int, error) {
	if steps <= 0 {
		return 0, errors.New("down steps 必须大于 0")
	}

	rolledBack := 0
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		if err := ensureMigrationsTable(ctx, conn); err != nil {
			return err
		}
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateApplied(r.migrations, applied); err != nil {
			return err
		}

		byVersion := make(map[uint64]Migration, len(r.migrations))
		for _, item := range r.migrations {
			byVersion[item.Version] = item
		}
		versions, err := appliedVersionsDescending(applied)
		if err != nil {
			return err
		}
		if len(versions) > steps {
			versions = versions[:steps]
		}

		for _, version := range versions {
			item, ok := byVersion[version]
			if !ok {
				return fmt.Errorf("数据库版本 %06d 不存在于 migration 源码", version)
			}
			if item.DownSQL == "" {
				return fmt.Errorf("%w: %06d_%s", ErrIrreversibleMigration, item.Version, item.Name)
			}
			if _, err := conn.ExecContext(ctx, "UPDATE schema_migrations SET dirty = TRUE WHERE version = ? AND dirty = FALSE", version); err != nil {
				return fmt.Errorf("标记 migration %06d 回滚中: %w", version, err)
			}
			if _, err := conn.ExecContext(ctx, item.DownSQL); err != nil {
				return fmt.Errorf("执行 migration %06d_%s down（已标记 dirty）: %w", item.Version, item.Name, err)
			}
			if _, err := conn.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = ? AND dirty = TRUE", version); err != nil {
				return fmt.Errorf("删除 migration %06d 版本记录: %w", version, err)
			}
			rolledBack++
		}
		return nil
	})
	return rolledBack, err
}

// Status 返回所有源码 migration 的当前应用状态。
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	var statuses []Status
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		if err := ensureMigrationsTable(ctx, conn); err != nil {
			return err
		}
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateApplied(r.migrations, applied); err != nil {
			return err
		}

		statuses = make([]Status, 0, len(r.migrations))
		for _, item := range r.migrations {
			status := Status{Version: item.Version, Name: item.Name, Checksum: item.Checksum}
			if existing, ok := applied[item.Version]; ok {
				status.Applied = true
				status.Dirty = existing.Dirty
				if existing.AppliedAt.Valid {
					value := existing.AppliedAt.Time
					status.AppliedAt = &value
				}
			}
			statuses = append(statuses, status)
		}
		return nil
	})
	return statuses, err
}

func (r *Runner) withLock(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取 migration 数据库连接: %w", err)
	}
	defer conn.Close()

	waitSeconds := int(r.lockWait.Round(time.Second) / time.Second)
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", migrationLockName, waitSeconds).Scan(&acquired); err != nil {
		return fmt.Errorf("获取 migration 锁: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return fmt.Errorf("等待 migration 锁超过 %s", r.lockWait)
	}
	defer func() {
		var released sql.NullInt64
		_ = conn.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&released)
	}()

	return fn(conn)
}

func ensureMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, createMigrationsTableSQL); err != nil {
		return fmt.Errorf("创建 schema_migrations: %w", err)
	}
	return nil
}

func loadApplied(ctx context.Context, conn *sql.Conn) (map[uint64]AppliedMigration, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version, name, checksum, dirty, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("读取 schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[uint64]AppliedMigration)
	for rows.Next() {
		var item AppliedMigration
		if err := rows.Scan(&item.Version, &item.Name, &item.Checksum, &item.Dirty, &item.AppliedAt); err != nil {
			return nil, fmt.Errorf("解析 schema_migrations: %w", err)
		}
		applied[item.Version] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 schema_migrations: %w", err)
	}
	return applied, nil
}

func validateApplied(source []Migration, applied map[uint64]AppliedMigration) error {
	byVersion := make(map[uint64]Migration, len(source))
	for _, item := range source {
		byVersion[item.Version] = item
	}
	for version, existing := range applied {
		if existing.Dirty {
			return fmt.Errorf("%w: %06d_%s", ErrDirtyMigration, version, existing.Name)
		}
		item, ok := byVersion[version]
		if !ok {
			return fmt.Errorf("数据库 migration %06d_%s 在源码中不存在", version, existing.Name)
		}
		if item.Name != existing.Name || item.Checksum != existing.Checksum {
			return fmt.Errorf("%w: %06d_%s", ErrChecksumMismatch, version, existing.Name)
		}
	}
	return nil
}

func appliedVersionsDescending(applied map[uint64]AppliedMigration) ([]uint64, error) {
	versions := make([]uint64, 0, len(applied))
	for version, item := range applied {
		if item.Dirty {
			return nil, fmt.Errorf("%w: %06d_%s", ErrDirtyMigration, version, item.Name)
		}
		versions = append(versions, version)
	}
	for i := 0; i < len(versions); i++ {
		for j := i + 1; j < len(versions); j++ {
			if versions[j] > versions[i] {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}
	return versions, nil
}
