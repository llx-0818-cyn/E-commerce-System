package analytics

import (
	"context"
	"math"
	"testing"
	"time"

	"feedsystem_video_go/internal/config"
)

func TestPopularityCalculatorDeterministicDecayPaidCapAndNoPaidOnlyRanking(t *testing.T) {
	calculator, err := NewPopularityCalculator(config.PopularityConfig{
		LikeWeight: 1, FavoriteWeight: 2, CommentWeight: 3,
		ShopVisitWeight: 1.5, ProductClickWeight: 2,
		PaidOrderWeight: 8, PaidOrderContributionCap: 24,
		DecayHalfLife: config.Duration(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("NewPopularityCalculator() error = %v", err)
	}
	breakdown := calculator.Calculate(SignalCounts{
		Likes: 2, Favorites: 1, Comments: 1, ShopVisits: 2, ProductClicks: 1, PaidOrders: 10,
	}, 24*time.Hour)
	assertClose(t, breakdown.OrganicContribution, 12)
	assertClose(t, breakdown.PaidContribution, 24)
	assertClose(t, breakdown.DecayFactor, 0.5)
	assertClose(t, breakdown.Total, 18)

	paidOnly := calculator.Calculate(SignalCounts{PaidOrders: 100}, 0)
	assertClose(t, paidOnly.OrganicContribution, 0)
	assertClose(t, paidOnly.PaidContribution, 0)
	assertClose(t, paidOnly.Total, 0)

	negativeAge := calculator.Calculate(SignalCounts{Likes: 1}, -time.Hour)
	assertClose(t, negativeAge.DecayFactor, 1)
	assertClose(t, negativeAge.Total, 1)
}

func TestPopularityAggregatorMapsConfiguredInteractionAndCommerceSignals(t *testing.T) {
	calculator, err := NewPopularityCalculator(config.PopularityConfig{
		LikeWeight: 1, FavoriteWeight: 2, CommentWeight: 3,
		ShopVisitWeight: 1.5, ProductClickWeight: 2,
		PaidOrderWeight: 8, PaidOrderContributionCap: 24,
		DecayHalfLife: config.Duration(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("NewPopularityCalculator() error = %v", err)
	}
	writer := &recordingPopularityWriter{}
	aggregator, err := NewPopularityAggregator(calculator, writer)
	if err != nil {
		t.Fatalf("NewPopularityAggregator() error = %v", err)
	}
	at := time.Date(2026, time.August, 24, 14, 0, 0, 0, time.UTC)
	if err := aggregator.HandleContentEvent(context.Background(), ContentFavoritedEvent, []byte(`{"post_id":42}`), at); err != nil {
		t.Fatalf("HandleContentEvent(favorite) error = %v", err)
	}
	if err := aggregator.Apply(context.Background(), BehaviorEvent{EventType: ShopVisitedEvent, PostID: 42, ShopID: 9, OccurredAt: at}); err != nil {
		t.Fatalf("Apply(shop visit) error = %v", err)
	}
	if err := aggregator.Apply(context.Background(), BehaviorEvent{EventType: OrderPaidEvent, PostID: 42, OrderID: 7, OccurredAt: at}); err != nil {
		t.Fatalf("Apply(order paid) error = %v", err)
	}
	if err := aggregator.Apply(context.Background(), BehaviorEvent{EventType: ProductClickedEvent, ProductID: 8, OccurredAt: at}); err != nil {
		t.Fatalf("Apply(unattributed product click) error = %v", err)
	}
	if len(writer.organic) != 2 || len(writer.paid) != 1 {
		t.Fatalf("writer calls organic=%#v paid=%#v", writer.organic, writer.paid)
	}
	assertClose(t, writer.organic[0].score, 2)
	assertClose(t, writer.organic[1].score, 1.5)
	assertClose(t, writer.paid[0].score, 8)
}

type recordedPopularitySignal struct {
	postID uint64
	score  float64
	at     time.Time
}

type recordingPopularityWriter struct {
	organic []recordedPopularitySignal
	paid    []recordedPopularitySignal
}

func (w *recordingPopularityWriter) RecordSignal(_ context.Context, postID uint64, score float64, at time.Time) error {
	w.organic = append(w.organic, recordedPopularitySignal{postID: postID, score: score, at: at})
	return nil
}

func (w *recordingPopularityWriter) RecordPaidSignal(_ context.Context, postID uint64, score float64, at time.Time) error {
	w.paid = append(w.paid, recordedPopularitySignal{postID: postID, score: score, at: at})
	return nil
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("value = %.12f, want %.12f", got, want)
	}
}
