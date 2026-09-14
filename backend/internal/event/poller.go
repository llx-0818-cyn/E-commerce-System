package event

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	OutboxStatusPublishing = "publishing"
	OutboxStatusPublished  = "published"
)

// Publisher 只在 Broker 已确认接收后返回 nil；不确定结果必须返回错误以便重投。
type Publisher interface {
	Publish(ctx context.Context, event OutboxEvent) error
}

type PollerConfig struct {
	Name               string
	BatchSize          int
	LeaseDuration      time.Duration
	BaseBackoff        time.Duration
	MaxBackoff         time.Duration
	PublishedRetention time.Duration
	CleanupBatchSize   int
	IdleInterval       time.Duration
}

func DefaultPollerConfig(name string) PollerConfig {
	return PollerConfig{
		Name:               name,
		BatchSize:          100,
		LeaseDuration:      30 * time.Second,
		BaseBackoff:        time.Second,
		MaxBackoff:         5 * time.Minute,
		PublishedRetention: 7 * 24 * time.Hour,
		CleanupBatchSize:   500,
		IdleInterval:       time.Second,
	}
}

func (c PollerConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 128 {
		return errors.New("poller name 必填且长度不能超过 128")
	}
	if c.BatchSize < 1 || c.BatchSize > 1000 {
		return errors.New("poller batch size 必须在 1..1000 之间")
	}
	if c.LeaseDuration <= 0 {
		return errors.New("poller lease duration 必须大于 0")
	}
	if c.BaseBackoff <= 0 || c.MaxBackoff < c.BaseBackoff {
		return errors.New("poller backoff 必须为正数且 max 不小于 base")
	}
	if c.PublishedRetention <= 0 || c.CleanupBatchSize < 1 || c.CleanupBatchSize > 10_000 {
		return errors.New("poller retention/cleanup batch 配置非法")
	}
	if c.IdleInterval <= 0 {
		return errors.New("poller idle interval 必须大于 0")
	}
	return nil
}

type PollResult struct {
	Claimed   int
	Published int
	Retried   int
}

type Poller struct {
	db        *gorm.DB
	publisher Publisher
	config    PollerConfig
	now       func() time.Time
}

func NewPoller(db *gorm.DB, publisher Publisher, config PollerConfig) (*Poller, error) {
	if db == nil {
		return nil, errors.New("outbox poller database is nil")
	}
	if publisher == nil {
		return nil, errors.New("outbox poller publisher is nil")
	}
	config.Name = strings.TrimSpace(config.Name)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Poller{db: db, publisher: publisher, config: config, now: func() time.Time { return time.Now().UTC() }}, nil
}

// PollOnce 抢占一个批次并逐条发布。单条发布失败会写回 pending 与退避时间，
// 不会阻断同批其他事件；数据库状态更新失败会返回错误并依靠租约超时重投。
func (p *Poller) PollOnce(ctx context.Context) (PollResult, error) {
	if p == nil || p.db == nil || p.publisher == nil {
		return PollResult{}, errors.New("outbox poller is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := p.now().UTC()
	events, err := p.claimBatch(ctx, now)
	if err != nil {
		return PollResult{}, err
	}
	result := PollResult{Claimed: len(events)}
	for _, item := range events {
		if err := p.publisher.Publish(ctx, item); err != nil {
			if rescheduleErr := p.reschedule(ctx, item, p.now().UTC(), err); rescheduleErr != nil {
				return result, errors.Join(err, rescheduleErr)
			}
			result.Retried++
			continue
		}
		if err := p.markPublished(ctx, item, p.now().UTC()); err != nil {
			// Broker 可能已经 ACK，但数据库标记失败。保留 publishing + lease，
			// 由后续 Poller 重投，接受重复而不静默丢失。
			return result, err
		}
		result.Published++
	}
	return result, nil
}

// Run 持续轮询；上下文取消时正常返回。
func (p *Poller) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("outbox poller run context is nil")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}

		result, err := p.PollOnce(ctx)
		if err != nil {
			return err
		}
		wait := p.config.IdleInterval
		if result.Claimed > 0 {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// CleanupPublished 按保留期有界删除已确认事件，保留 pending/publishing/failed 记录。
func (p *Poller) CleanupPublished(ctx context.Context) (int64, error) {
	if p == nil || p.db == nil {
		return 0, errors.New("outbox poller is not initialized")
	}
	cutoff := p.now().UTC().Add(-p.config.PublishedRetention)
	result := p.db.WithContext(ctx).Exec(
		"DELETE FROM outbox_events WHERE status = ? AND published_at < ? ORDER BY published_at, id LIMIT ?",
		OutboxStatusPublished,
		cutoff,
		p.config.CleanupBatchSize,
	)
	if result.Error != nil {
		return 0, fmt.Errorf("cleanup published outbox events: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (p *Poller) claimBatch(ctx context.Context, now time.Time) ([]OutboxEvent, error) {
	var claimed []OutboxEvent
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []OutboxEvent
		if err := tx.Raw(`
SELECT
    id, event_id, aggregate_type, aggregate_id, event_type, payload_json,
    status, attempts, next_attempt_at, last_error, claimed_by, claimed_until,
    created_at, updated_at, published_at
FROM outbox_events
WHERE
    (status = ? AND next_attempt_at <= ?)
    OR (status = ? AND claimed_until <= ?)
ORDER BY next_attempt_at, id
LIMIT ?
FOR UPDATE SKIP LOCKED`,
			OutboxStatusPending,
			now,
			OutboxStatusPublishing,
			now,
			p.config.BatchSize,
		).Scan(&candidates).Error; err != nil {
			return fmt.Errorf("select outbox claim candidates: %w", err)
		}
		if len(candidates) == 0 {
			claimed = []OutboxEvent{}
			return nil
		}

		ids := make([]uint64, len(candidates))
		claimUntil := now.Add(p.config.LeaseDuration)
		for index := range candidates {
			ids[index] = candidates[index].ID
		}
		result := tx.Model(&OutboxEvent{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":        OutboxStatusPublishing,
				"claimed_by":    p.config.Name,
				"claimed_until": claimUntil,
				"attempts":      gorm.Expr("attempts + 1"),
			})
		if result.Error != nil {
			return fmt.Errorf("claim outbox batch: %w", result.Error)
		}
		if result.RowsAffected != int64(len(candidates)) {
			return fmt.Errorf("claim outbox batch affected %d rows, want %d", result.RowsAffected, len(candidates))
		}

		owner := p.config.Name
		for index := range candidates {
			candidates[index].Status = OutboxStatusPublishing
			candidates[index].Attempts++
			candidates[index].ClaimedBy = &owner
			candidates[index].ClaimedUntil = &claimUntil
		}
		claimed = candidates
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (p *Poller) markPublished(ctx context.Context, item OutboxEvent, now time.Time) error {
	result := p.db.WithContext(ctx).Model(&OutboxEvent{}).
		Where("id = ? AND status = ? AND claimed_by = ?", item.ID, OutboxStatusPublishing, p.config.Name).
		Updates(map[string]any{
			"status":        OutboxStatusPublished,
			"published_at":  now,
			"claimed_by":    nil,
			"claimed_until": nil,
			"last_error":    nil,
		})
	if result.Error != nil {
		return fmt.Errorf("mark outbox event %s published: %w", item.EventID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("mark outbox event %s published lost claim ownership", item.EventID)
	}
	return nil
}

func (p *Poller) reschedule(ctx context.Context, item OutboxEvent, now time.Time, publishErr error) error {
	nextAttempt := now.Add(backoffForAttempt(item.Attempts, p.config.BaseBackoff, p.config.MaxBackoff))
	lastError := truncateError(publishErr, 4096)
	result := p.db.WithContext(ctx).Model(&OutboxEvent{}).
		Where("id = ? AND status = ? AND claimed_by = ?", item.ID, OutboxStatusPublishing, p.config.Name).
		Updates(map[string]any{
			"status":          OutboxStatusPending,
			"next_attempt_at": nextAttempt,
			"last_error":      lastError,
			"claimed_by":      nil,
			"claimed_until":   nil,
		})
	if result.Error != nil {
		return fmt.Errorf("reschedule outbox event %s: %w", item.EventID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("reschedule outbox event %s lost claim ownership", item.EventID)
	}
	return nil
}

func backoffForAttempt(attempt uint, base, maximum time.Duration) time.Duration {
	if attempt <= 1 {
		return base
	}
	backoff := base
	for current := uint(1); current < attempt; current++ {
		if backoff >= maximum/2 {
			return maximum
		}
		backoff *= 2
	}
	if backoff > maximum {
		return maximum
	}
	return backoff
}

func truncateError(err error, maxRunes int) string {
	if err == nil {
		return ""
	}
	runes := []rune(err.Error())
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}
