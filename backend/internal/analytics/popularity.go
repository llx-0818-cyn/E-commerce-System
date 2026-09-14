package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"feedsystem_video_go/internal/config"
)

const (
	ContentLikedEvent          = "content.liked"
	ContentFavoritedEvent      = "content.favorited"
	ContentCommentCreatedEvent = "content.comment.created"
)

type SignalCounts struct {
	Likes         uint64
	Favorites     uint64
	Comments      uint64
	ShopVisits    uint64
	ProductClicks uint64
	PaidOrders    uint64
}

type ScoreBreakdown struct {
	OrganicContribution float64
	PaidContribution    float64
	DecayFactor         float64
	Total               float64
}

type PopularityCalculator struct {
	config config.PopularityConfig
}

func NewPopularityCalculator(value config.PopularityConfig) (*PopularityCalculator, error) {
	weights := []float64{
		value.LikeWeight, value.FavoriteWeight, value.CommentWeight,
		value.ShopVisitWeight, value.ProductClickWeight, value.PaidOrderWeight,
		value.PaidOrderContributionCap,
	}
	for _, weight := range weights {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return nil, errors.New("popularity weights must be finite and non-negative")
		}
	}
	if value.DecayHalfLife.Value() <= 0 || value.PaidOrderContributionCap < value.PaidOrderWeight {
		return nil, errors.New("popularity decay/cap config is invalid")
	}
	return &PopularityCalculator{config: value}, nil
}

func (c *PopularityCalculator) Calculate(counts SignalCounts, age time.Duration) ScoreBreakdown {
	if age < 0 {
		age = 0
	}
	organic := float64(counts.Likes)*c.config.LikeWeight +
		float64(counts.Favorites)*c.config.FavoriteWeight +
		float64(counts.Comments)*c.config.CommentWeight +
		float64(counts.ShopVisits)*c.config.ShopVisitWeight +
		float64(counts.ProductClicks)*c.config.ProductClickWeight
	paid := 0.0
	// 成交不能独立决定排序；只有存在至少一个非成交信号时才贡献，且总贡献封顶。
	if organic > 0 {
		paid = math.Min(float64(counts.PaidOrders)*c.config.PaidOrderWeight, c.config.PaidOrderContributionCap)
	}
	decay := math.Pow(0.5, age.Seconds()/c.config.DecayHalfLife.Value().Seconds())
	return ScoreBreakdown{
		OrganicContribution: organic, PaidContribution: paid, DecayFactor: decay,
		Total: (organic + paid) * decay,
	}
}

func (c *PopularityCalculator) OrganicWeight(eventType string) float64 {
	switch eventType {
	case ContentLikedEvent:
		return c.config.LikeWeight
	case ContentFavoritedEvent:
		return c.config.FavoriteWeight
	case ContentCommentCreatedEvent:
		return c.config.CommentWeight
	case ShopVisitedEvent:
		return c.config.ShopVisitWeight
	case ProductClickedEvent:
		return c.config.ProductClickWeight
	default:
		return 0
	}
}

func (c *PopularityCalculator) PaidWeight() float64 { return c.config.PaidOrderWeight }

type PopularityWindowWriter interface {
	RecordSignal(context.Context, uint64, float64, time.Time) error
	RecordPaidSignal(context.Context, uint64, float64, time.Time) error
}

type PopularityAggregator struct {
	calculator *PopularityCalculator
	writer     PopularityWindowWriter
}

func NewPopularityAggregator(calculator *PopularityCalculator, writer PopularityWindowWriter) (*PopularityAggregator, error) {
	if calculator == nil || writer == nil {
		return nil, errors.New("popularity aggregator dependencies are required")
	}
	return &PopularityAggregator{calculator: calculator, writer: writer}, nil
}

func (a *PopularityAggregator) Apply(ctx context.Context, value BehaviorEvent) error {
	if value.PostID == 0 {
		return nil // 无来源 Post 的行为仍保留统计事件，但不反哺具体内容热度。
	}
	if value.OccurredAt.IsZero() {
		return ErrBehaviorValidation
	}
	if value.EventType == OrderPaidEvent {
		if weight := a.calculator.PaidWeight(); weight > 0 {
			return a.writer.RecordPaidSignal(ctx, value.PostID, weight, value.OccurredAt)
		}
		return nil
	}
	if weight := a.calculator.OrganicWeight(value.EventType); weight > 0 {
		return a.writer.RecordSignal(ctx, value.PostID, weight, value.OccurredAt)
	}
	return nil
}

func (a *PopularityAggregator) HandleContentEvent(ctx context.Context, eventType string, payload []byte, occurredAt time.Time) error {
	weight := a.calculator.OrganicWeight(eventType)
	if weight == 0 {
		return nil
	}
	var value struct {
		PostID uint64 `json:"post_id"`
	}
	if err := json.Unmarshal(payload, &value); err != nil || value.PostID == 0 {
		return ErrBehaviorValidation
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return a.writer.RecordSignal(ctx, value.PostID, weight, occurredAt)
}
