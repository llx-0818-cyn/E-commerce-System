package event

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

const OutboxStatusPending = "pending"

var eventNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

// DomainEvent 是写入通用 Outbox 的稳定领域事件信封。
type DomainEvent struct {
	EventID       string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       any
	AvailableAt   time.Time
}

// OutboxEvent 是 outbox_events 的持久化映射；Schema 只由 SQL Migration 管理。
type OutboxEvent struct {
	ID            uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	EventID       string     `gorm:"column:event_id"`
	AggregateType string     `gorm:"column:aggregate_type"`
	AggregateID   string     `gorm:"column:aggregate_id"`
	EventType     string     `gorm:"column:event_type"`
	PayloadJSON   string     `gorm:"column:payload_json"`
	Status        string     `gorm:"column:status"`
	Attempts      uint       `gorm:"column:attempts"`
	NextAttemptAt time.Time  `gorm:"column:next_attempt_at"`
	LastError     *string    `gorm:"column:last_error"`
	ClaimedBy     *string    `gorm:"column:claimed_by"`
	ClaimedUntil  *time.Time `gorm:"column:claimed_until"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;autoUpdateTime"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
}

func (OutboxEvent) TableName() string {
	return "outbox_events"
}

// Repository 统一创建本地事务，并为回调绑定同一个 Outbox Writer。
type Repository struct {
	db         *gorm.DB
	newEventID func() (string, error)
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("event repository database is nil")
	}
	return &Repository{db: db, newEventID: randomEventID}, nil
}

// Writer 只能由 Repository.Transact 创建，保证事件写入与业务写入使用同一 tx。
type Writer struct {
	tx         *gorm.DB
	newEventID func() (string, error)
}

// Transact 在一个本地事务中执行业务修改和 Outbox 写入。
func (r *Repository) Transact(ctx context.Context, fn func(tx *gorm.DB, outbox *Writer) error) error {
	if r == nil || r.db == nil {
		return errors.New("event repository is not initialized")
	}
	if fn == nil {
		return errors.New("transaction callback is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		writer := &Writer{tx: tx, newEventID: r.newEventID}
		return fn(tx, writer)
	})
}

// AppendStandalone 在核心业务事务之外追加一条事件。适用于统计/行为事件：
// 追加失败由调用者记录，但不得反向回滚已经提交的核心读写。
func (r *Repository) AppendStandalone(ctx context.Context, domainEvent DomainEvent) (string, error) {
	ids, err := r.AppendStandaloneBatch(ctx, []DomainEvent{domainEvent})
	if err != nil {
		return "", err
	}
	return ids[0], nil
}

// AppendStandaloneBatch 预生成全部事件后用一条批量 INSERT 持久化 pending Outbox。
func (r *Repository) AppendStandaloneBatch(ctx context.Context, domainEvents []DomainEvent) ([]string, error) {
	if r == nil || r.db == nil || r.newEventID == nil {
		return nil, errors.New("event repository is not initialized")
	}
	if len(domainEvents) == 0 {
		return []string{}, nil
	}
	records := make([]OutboxEvent, len(domainEvents))
	ids := make([]string, len(domainEvents))
	for index, domainEvent := range domainEvents {
		record, err := prepareOutboxEvent(domainEvent, r.newEventID)
		if err != nil {
			return nil, err
		}
		records[index] = *record
		ids[index] = record.EventID
	}
	database := r.db
	if ctx != nil {
		database = database.WithContext(ctx)
	}
	if err := database.Create(&records).Error; err != nil {
		return nil, fmt.Errorf("批量写入 standalone outbox events: %w", err)
	}
	return ids, nil
}

// Append 验证并写入一条 pending 事件，返回最终 event_id。
func (w *Writer) Append(ctx context.Context, domainEvent DomainEvent) (string, error) {
	if w == nil || w.tx == nil || w.newEventID == nil {
		return "", errors.New("outbox writer is not initialized")
	}
	record, err := prepareOutboxEvent(domainEvent, w.newEventID)
	if err != nil {
		return "", err
	}
	transaction := w.tx
	if ctx != nil {
		transaction = transaction.WithContext(ctx)
	}
	if err := transaction.Create(record).Error; err != nil {
		return "", fmt.Errorf("写入 outbox event %s: %w", record.EventID, err)
	}
	return record.EventID, nil
}

func prepareOutboxEvent(domainEvent DomainEvent, idGenerator func() (string, error)) (*OutboxEvent, error) {
	domainEvent.EventID = strings.TrimSpace(domainEvent.EventID)
	domainEvent.AggregateType = strings.TrimSpace(domainEvent.AggregateType)
	domainEvent.AggregateID = strings.TrimSpace(domainEvent.AggregateID)
	domainEvent.EventType = strings.TrimSpace(domainEvent.EventType)

	if domainEvent.EventID == "" {
		generated, err := idGenerator()
		if err != nil {
			return nil, fmt.Errorf("生成 event_id: %w", err)
		}
		domainEvent.EventID = generated
	}
	if len(domainEvent.EventID) > 64 {
		return nil, errors.New("event_id 长度不能超过 64")
	}
	if len(domainEvent.AggregateType) > 64 || !eventNamePattern.MatchString(domainEvent.AggregateType) {
		return nil, errors.New("aggregate_type 必须使用小写字母、数字、点、下划线或短横线")
	}
	if domainEvent.AggregateID == "" || len(domainEvent.AggregateID) > 128 {
		return nil, errors.New("aggregate_id 必填且长度不能超过 128")
	}
	if !eventNamePattern.MatchString(domainEvent.EventType) {
		return nil, errors.New("event_type 必须使用小写字母、数字、点、下划线或短横线")
	}
	if domainEvent.Payload == nil {
		return nil, errors.New("event payload is required")
	}
	payload, err := json.Marshal(domainEvent.Payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 event payload: %w", err)
	}
	if !json.Valid(payload) {
		return nil, errors.New("event payload 不是合法 JSON")
	}
	nextAttempt := domainEvent.AvailableAt.UTC()
	if domainEvent.AvailableAt.IsZero() {
		nextAttempt = time.Now().UTC()
	}

	return &OutboxEvent{
		EventID:       domainEvent.EventID,
		AggregateType: domainEvent.AggregateType,
		AggregateID:   domainEvent.AggregateID,
		EventType:     domainEvent.EventType,
		PayloadJSON:   string(payload),
		Status:        OutboxStatusPending,
		Attempts:      0,
		NextAttemptAt: nextAttempt,
	}, nil
}

func randomEventID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
