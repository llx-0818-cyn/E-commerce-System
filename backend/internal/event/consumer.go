package event

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type ConsumedEvent struct {
	EventID       string
	EventType     string
	AggregateType string
	AggregateID   string
}

// ProcessedEvent 是 processed_events 的持久化映射；Schema 只由 SQL Migration 管理。
type ProcessedEvent struct {
	ConsumerName  string    `gorm:"column:consumer_name;primaryKey"`
	EventID       string    `gorm:"column:event_id;primaryKey"`
	EventType     string    `gorm:"column:event_type"`
	AggregateType *string   `gorm:"column:aggregate_type"`
	AggregateID   *string   `gorm:"column:aggregate_id"`
	ProcessedAt   time.Time `gorm:"column:processed_at;autoCreateTime"`
}

func (ProcessedEvent) TableName() string {
	return "processed_events"
}

// ConsumerGuard 把 (consumer_name, event_id) 标记与业务效果放进同一事务。
type ConsumerGuard struct {
	db           *gorm.DB
	consumerName string
}

func NewConsumerGuard(db *gorm.DB, consumerName string) (*ConsumerGuard, error) {
	consumerName = strings.TrimSpace(consumerName)
	if db == nil {
		return nil, errors.New("consumer guard database is nil")
	}
	if consumerName == "" || len(consumerName) > 128 || !eventNamePattern.MatchString(consumerName) {
		return nil, errors.New("consumer_name 必须是长度不超过 128 的小写稳定标识")
	}
	return &ConsumerGuard{db: db, consumerName: consumerName}, nil
}

// Process 返回 applied=true 表示本次首次执行了业务 Handler；重复事件返回 false,nil。
func (g *ConsumerGuard) Process(
	ctx context.Context,
	consumed ConsumedEvent,
	handler func(tx *gorm.DB) error,
) (bool, error) {
	if g == nil || g.db == nil {
		return false, errors.New("consumer guard is not initialized")
	}
	if handler == nil {
		return false, errors.New("consumer handler is nil")
	}
	record, err := g.processedRecord(consumed)
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	applied := false
	err = g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Duplicate 是幂等正常分支，不让 GORM 默认 logger 把它打印为系统错误。
		markerTx := tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(logger.Silent)})
		if err := markerTx.Create(record).Error; err != nil {
			if isDuplicateKey(err) {
				return nil
			}
			return fmt.Errorf("写入 processed event %s/%s: %w", g.consumerName, consumed.EventID, err)
		}
		if err := handler(tx); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (g *ConsumerGuard) processedRecord(consumed ConsumedEvent) (*ProcessedEvent, error) {
	consumed.EventID = strings.TrimSpace(consumed.EventID)
	consumed.EventType = strings.TrimSpace(consumed.EventType)
	consumed.AggregateType = strings.TrimSpace(consumed.AggregateType)
	consumed.AggregateID = strings.TrimSpace(consumed.AggregateID)
	if consumed.EventID == "" || len(consumed.EventID) > 64 {
		return nil, errors.New("consumed event_id 必填且长度不能超过 64")
	}
	if !eventNamePattern.MatchString(consumed.EventType) {
		return nil, errors.New("consumed event_type 格式非法")
	}
	if (consumed.AggregateType == "") != (consumed.AggregateID == "") {
		return nil, errors.New("aggregate_type 与 aggregate_id 必须同时提供或同时省略")
	}
	if len(consumed.AggregateType) > 64 || (consumed.AggregateType != "" && !eventNamePattern.MatchString(consumed.AggregateType)) {
		return nil, errors.New("consumed aggregate_type 格式非法")
	}
	if len(consumed.AggregateID) > 128 {
		return nil, errors.New("consumed aggregate_id 长度不能超过 128")
	}

	record := &ProcessedEvent{
		ConsumerName: g.consumerName,
		EventID:      consumed.EventID,
		EventType:    consumed.EventType,
	}
	if consumed.AggregateType != "" {
		record.AggregateType = &consumed.AggregateType
		record.AggregateID = &consumed.AggregateID
	}
	return record, nil
}

func isDuplicateKey(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}
