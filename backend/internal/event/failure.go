package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type FailedEvent struct {
	ID             uint64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ConsumerName   string     `gorm:"column:consumer_name" json:"consumer_name"`
	EventID        string     `gorm:"column:event_id" json:"event_id"`
	EventType      string     `gorm:"column:event_type" json:"event_type"`
	PayloadJSON    string     `gorm:"column:payload_json" json:"-"`
	FailureStage   string     `gorm:"column:failure_stage" json:"failure_stage"`
	Attempts       uint       `gorm:"column:attempts" json:"attempts"`
	FirstFailedAt  time.Time  `gorm:"column:first_failed_at" json:"first_failed_at"`
	LastFailedAt   time.Time  `gorm:"column:last_failed_at" json:"last_failed_at"`
	LastError      string     `gorm:"column:last_error" json:"last_error"`
	ResolvedAt     *time.Time `gorm:"column:resolved_at" json:"resolved_at,omitempty"`
	ResolutionNote *string    `gorm:"column:resolution_note" json:"resolution_note,omitempty"`
}

func (FailedEvent) TableName() string {
	return "failed_events"
}

func (f FailedEvent) Payload() json.RawMessage {
	return json.RawMessage(f.PayloadJSON)
}

type FailureRecord struct {
	ConsumerName string
	EventID      string
	EventType    string
	Payload      []byte
	FailureStage string
	Attempts     uint
	LastError    error
	FailedAt     time.Time
}

type FailureQuery struct {
	ConsumerName string
	EventType    string
	BeforeTime   time.Time
	BeforeID     uint64
	Limit        int
}

type FailureRepository struct {
	db *gorm.DB
}

func NewFailureRepository(db *gorm.DB) (*FailureRepository, error) {
	if db == nil {
		return nil, errors.New("failure repository database is nil")
	}
	return &FailureRepository{db: db}, nil
}

// Record 以 (consumer_name,event_id) upsert 失败，重复记录只推进次数和最后原因。
func (r *FailureRepository) Record(ctx context.Context, failure FailureRecord) error {
	if r == nil || r.db == nil {
		return errors.New("failure repository is not initialized")
	}
	if err := validateFailureRecord(&failure); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.db.WithContext(ctx).Exec(`
INSERT INTO failed_events
    (consumer_name, event_id, event_type, payload_json, failure_stage, attempts,
     first_failed_at, last_failed_at, last_error, resolved_at, resolution_note)
VALUES
    (?, ?, ?, CAST(? AS JSON), ?, ?, ?, ?, ?, NULL, NULL)
ON DUPLICATE KEY UPDATE
    event_type = VALUES(event_type),
    payload_json = VALUES(payload_json),
    failure_stage = VALUES(failure_stage),
    attempts = GREATEST(attempts, VALUES(attempts)),
    last_failed_at = VALUES(last_failed_at),
    last_error = VALUES(last_error),
    resolved_at = NULL,
    resolution_note = NULL`,
		failure.ConsumerName,
		failure.EventID,
		failure.EventType,
		string(failure.Payload),
		failure.FailureStage,
		failure.Attempts,
		failure.FailedAt,
		failure.FailedAt,
		truncateError(failure.LastError, 4096),
	).Error
}

func (r *FailureRepository) ListUnresolved(ctx context.Context, query FailureQuery) ([]FailedEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("failure repository is not initialized")
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		return nil, errors.New("failure query limit 不能超过 200")
	}
	database := r.db.WithContext(ctx).Model(&FailedEvent{}).Where("resolved_at IS NULL")
	if query.ConsumerName = strings.TrimSpace(query.ConsumerName); query.ConsumerName != "" {
		database = database.Where("consumer_name = ?", query.ConsumerName)
	}
	if query.EventType = strings.TrimSpace(query.EventType); query.EventType != "" {
		database = database.Where("event_type = ?", query.EventType)
	}
	if !query.BeforeTime.IsZero() {
		if query.BeforeID == 0 {
			return nil, errors.New("failure query 使用 BeforeTime 时必须提供 BeforeID")
		}
		database = database.Where(
			"last_failed_at < ? OR (last_failed_at = ? AND id < ?)",
			query.BeforeTime,
			query.BeforeTime,
			query.BeforeID,
		)
	}
	var failures []FailedEvent
	if err := database.Order("last_failed_at DESC, id DESC").Limit(query.Limit).Find(&failures).Error; err != nil {
		return nil, fmt.Errorf("query unresolved failed events: %w", err)
	}
	return failures, nil
}

func (r *FailureRepository) Resolve(ctx context.Context, id uint64, note string, resolvedAt time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("failure repository is not initialized")
	}
	note = strings.TrimSpace(note)
	if id == 0 || note == "" {
		return false, errors.New("failure id 和 resolution note 必填")
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	result := r.db.WithContext(ctx).Model(&FailedEvent{}).
		Where("id = ? AND resolved_at IS NULL", id).
		Updates(map[string]any{"resolved_at": resolvedAt, "resolution_note": note})
	if result.Error != nil {
		return false, fmt.Errorf("resolve failed event %d: %w", id, result.Error)
	}
	return result.RowsAffected == 1, nil
}

func validateFailureRecord(failure *FailureRecord) error {
	failure.ConsumerName = strings.TrimSpace(failure.ConsumerName)
	failure.EventID = strings.TrimSpace(failure.EventID)
	failure.EventType = strings.TrimSpace(failure.EventType)
	failure.FailureStage = strings.TrimSpace(failure.FailureStage)
	if failure.ConsumerName == "" || len(failure.ConsumerName) > 128 || !eventNamePattern.MatchString(failure.ConsumerName) {
		return errors.New("failure consumer_name 格式非法")
	}
	if failure.EventID == "" || len(failure.EventID) > 64 {
		return errors.New("failure event_id 必填且长度不能超过 64")
	}
	if !eventNamePattern.MatchString(failure.EventType) {
		return errors.New("failure event_type 格式非法")
	}
	switch failure.FailureStage {
	case "deserialize", "handle", "publish", "compensate", "reconcile":
	default:
		return errors.New("failure stage 格式非法")
	}
	if failure.Attempts == 0 || failure.LastError == nil {
		return errors.New("failure attempts 和 last error 必填")
	}
	if !json.Valid(failure.Payload) {
		return errors.New("failure payload 必须是合法 JSON")
	}
	if failure.FailedAt.IsZero() {
		failure.FailedAt = time.Now().UTC()
	} else {
		failure.FailedAt = failure.FailedAt.UTC()
	}
	return nil
}
