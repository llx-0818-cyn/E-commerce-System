package cacheinvalidation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	domainevent "feedsystem_video_go/internal/event"
)

const EventType = "cache.invalidate.requested"

type Entity string

const (
	ContentEntity     Entity = "content"
	ShopSummary       Entity = "shop_summary"
	ProductDetail     Entity = "product_detail"
	ProductSKU        Entity = "product_sku"
	ProductInventory  Entity = "product_inventory"
	ProductPriceRange Entity = "product_price_range"
)

type Target struct {
	Entity Entity `json:"entity"`
	ID     uint64 `json:"id"`
}

type Payload struct {
	Targets []Target `json:"targets"`
}

type Invalidator interface {
	Invalidate(context.Context, []Target) error
}

type InvalidatorFunc func(context.Context, []Target) error

func (f InvalidatorFunc) Invalidate(ctx context.Context, targets []Target) error {
	return f(ctx, targets)
}

type Composite []Invalidator

func (c Composite) Invalidate(ctx context.Context, targets []Target) error {
	var result error
	for _, invalidator := range c {
		if invalidator != nil {
			result = errors.Join(result, invalidator.Invalidate(ctx, targets))
		}
	}
	return result
}

func AppendFallback(ctx context.Context, events *domainevent.Repository, targets []Target) error {
	if events == nil {
		return errors.New("cache invalidation event repository is nil")
	}
	normalized, err := normalizeTargets(targets)
	if err != nil {
		return err
	}
	_, err = events.AppendStandalone(ctx, domainevent.DomainEvent{
		AggregateType: "cache_invalidation",
		AggregateID:   aggregateID(normalized),
		EventType:     EventType,
		Payload:       Payload{Targets: normalized},
	})
	return err
}

type Processor struct {
	invalidator Invalidator
}

func NewProcessor(invalidator Invalidator) (*Processor, error) {
	if invalidator == nil {
		return nil, errors.New("cache invalidation processor invalidator is nil")
	}
	return &Processor{invalidator: invalidator}, nil
}

func (p *Processor) Process(ctx context.Context, eventType string, payloadJSON []byte) error {
	if p == nil || p.invalidator == nil {
		return errors.New("cache invalidation processor is not initialized")
	}
	if eventType != EventType {
		return fmt.Errorf("unsupported cache invalidation event %q", eventType)
	}
	var payload Payload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return fmt.Errorf("decode cache invalidation payload: %w", err)
	}
	targets, err := normalizeTargets(payload.Targets)
	if err != nil {
		return err
	}
	return p.invalidator.Invalidate(ctx, targets)
}

func normalizeTargets(targets []Target) ([]Target, error) {
	if len(targets) == 0 || len(targets) > 1000 {
		return nil, errors.New("cache invalidation requires 1..1000 targets")
	}
	result := make([]Target, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.ID == 0 || !validEntity(target.Entity) {
			return nil, errors.New("cache invalidation target is invalid")
		}
		key := string(target.Entity) + ":" + strconv.FormatUint(target.ID, 10)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Entity == result[j].Entity {
			return result[i].ID < result[j].ID
		}
		return result[i].Entity < result[j].Entity
	})
	return result, nil
}

func validEntity(entity Entity) bool {
	switch entity {
	case ContentEntity, ShopSummary, ProductDetail, ProductSKU, ProductInventory, ProductPriceRange:
		return true
	default:
		return false
	}
}

func aggregateID(targets []Target) string {
	var raw strings.Builder
	for _, target := range targets {
		raw.WriteString(string(target.Entity))
		raw.WriteByte(':')
		raw.WriteString(strconv.FormatUint(target.ID, 10))
		raw.WriteByte(',')
	}
	sum := sha256.Sum256([]byte(raw.String()))
	return hex.EncodeToString(sum[:16])
}
