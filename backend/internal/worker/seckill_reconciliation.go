package worker

import (
	"context"
	"errors"
	"time"
)

type FlashSaleReconciler interface {
	ReconcileCampaignCycle(context.Context, int) (int, error)
}

func RunFlashSaleReconciliation(ctx context.Context, reconciler FlashSaleReconciler, interval time.Duration, limit int, report func(error)) error {
	if reconciler == nil || interval <= 0 || limit < 1 || limit > 1000 {
		return errors.New("flash sale reconciliation configuration is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := reconciler.ReconcileCampaignCycle(ctx, limit); err != nil && report != nil {
				report(err)
			}
		}
	}
}
