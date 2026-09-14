package promotion

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm/clause"
)

type ReconciliationResult struct {
	CampaignID           uint64 `json:"campaign_id"`
	ExpectedRemaining    int64  `json:"expected_remaining"`
	ActualRemaining      int64  `json:"actual_remaining"`
	ExpectedReservations int64  `json:"expected_reservations"`
	ActualReservations   int64  `json:"actual_reservations"`
	Anomalies            int    `json:"anomalies"`
	Repaired             bool   `json:"repaired"`
}

type reconciliationRequestRow struct {
	RequestID   string  `gorm:"column:request_id"`
	BuyerID     uint64  `gorm:"column:buyer_id"`
	Quantity    uint    `gorm:"column:quantity"`
	Status      string  `gorm:"column:status"`
	OrderID     *uint64 `gorm:"column:order_id"`
	OrderStatus *string `gorm:"column:order_status"`
}

func (s *Service) ReconcileCampaign(ctx context.Context, campaignID uint64) (*ReconciliationResult, error) {
	if campaignID == 0 || s.cache == nil {
		return nil, ErrCampaignValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	campaign, err := s.GetCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	stockKey := CampaignStockKey(s.cache, campaign.ID, campaign.CampaignVersion)
	stockBytes, err := s.cache.GetBytes(ctx, stockKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPreheatUnavailable, err)
	}
	actualRemaining, err := strconv.ParseInt(string(stockBytes), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("decode campaign stock: %w", err)
	}
	redisBuyers, err := s.cache.HGetAll(ctx, CampaignBuyersKey(s.cache, campaign.ID, campaign.CampaignVersion))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPreheatUnavailable, err)
	}
	var rows []reconciliationRequestRow
	if err := s.repository.db.WithContext(ctx).Table("flash_sale_requests AS request").
		Select("request.request_id,request.buyer_id,request.quantity,request.status,request.order_id,orders.status AS order_status").
		Joins("LEFT JOIN orders ON orders.id=request.order_id").Where("request.campaign_id=?", campaign.ID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	dbBuyers := make(map[uint64]int64)
	expectedReservations := int64(0)
	for _, row := range rows {
		if requestConsumesCampaignStock(row.Status) {
			expectedReservations += int64(row.Quantity)
			dbBuyers[row.BuyerID] += int64(row.Quantity)
		}
	}
	expectedRemaining := campaign.TotalStock - expectedReservations
	result := &ReconciliationResult{
		CampaignID: campaign.ID, ExpectedRemaining: expectedRemaining, ActualRemaining: actualRemaining,
		ExpectedReservations: expectedReservations,
	}
	redisBuyerValues := make(map[uint64]int64)
	for rawBuyer, rawQuantity := range redisBuyers {
		buyerID, buyerErr := strconv.ParseUint(rawBuyer, 10, 64)
		quantity, quantityErr := strconv.ParseInt(rawQuantity, 10, 64)
		if buyerErr != nil || quantityErr != nil || quantity < 0 {
			result.Anomalies++
			continue
		}
		redisBuyerValues[buyerID] = quantity
		result.ActualReservations += quantity
	}
	buyersMatch := equalReservationMaps(dbBuyers, redisBuyerValues)
	if !buyersMatch {
		result.Anomalies++
		if err := s.upsertReconciliationAnomaly(ctx, campaign.ID, fmt.Sprintf("reservation:v%d", campaign.CampaignVersion),
			"reservation_orphan", nil, nil, &expectedReservations, &result.ActualReservations,
			"Redis buyer reservations do not match active request quantities", map[string]any{"database": dbBuyers, "redis": redisBuyerValues}, false); err != nil {
			return nil, err
		}
	} else if err := s.resolveReconciliationAnomaly(ctx, campaign.ID, fmt.Sprintf("reservation:v%d", campaign.CampaignVersion), "Redis buyer reservations match database requests"); err != nil {
		return nil, err
	}
	if expectedRemaining < 0 || actualRemaining != expectedRemaining {
		result.Anomalies++
		canRepair := expectedRemaining >= 0 && expectedRemaining <= campaign.TotalStock && buyersMatch &&
			(campaign.Status != CampaignStatusEnabled || !campaign.EndsAt.After(s.now().UTC()))
		if canRepair {
			ttl := time.Hour
			if campaign.EndsAt.After(s.now().UTC()) {
				ttl += campaign.EndsAt.Sub(s.now().UTC())
			}
			if err := s.cache.SetBytes(ctx, stockKey, []byte(strconv.FormatInt(expectedRemaining, 10)), ttl); err != nil {
				return nil, err
			}
			result.ActualRemaining = expectedRemaining
			result.Repaired = true
		}
		if err := s.upsertReconciliationAnomaly(ctx, campaign.ID, fmt.Sprintf("stock:v%d", campaign.CampaignVersion),
			"stock_mismatch", nil, nil, &expectedRemaining, &actualRemaining,
			"Redis remaining stock does not match campaign requests", map[string]any{"version": campaign.CampaignVersion}, canRepair); err != nil {
			return nil, err
		}
	} else if err := s.resolveReconciliationAnomaly(ctx, campaign.ID, fmt.Sprintf("stock:v%d", campaign.CampaignVersion), "Redis remaining stock matches database requests"); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if requestOrderStateMatches(row) {
			if err := s.resolveReconciliationAnomaly(ctx, campaign.ID, "request:"+row.RequestID, "request and order states match"); err != nil {
				return nil, err
			}
			continue
		}
		result.Anomalies++
		if err := s.upsertReconciliationAnomaly(ctx, campaign.ID, "request:"+row.RequestID,
			"request_state_mismatch", &row.RequestID, row.OrderID, nil, nil,
			"flash sale request status does not match linked order", map[string]any{"request_status": row.Status, "order_status": row.OrderStatus}, false); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Service) resolveReconciliationAnomaly(ctx context.Context, campaignID uint64, key, resolution string) error {
	now := s.now().UTC()
	return s.repository.db.WithContext(ctx).Model(&ReconciliationAnomaly{}).
		Where("campaign_id=? AND anomaly_key=? AND status='open'", campaignID, key).
		Updates(map[string]any{"status": "resolved", "resolved_at": now, "resolution": resolution}).Error
}

func (s *Service) ReconcileCampaignCycle(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrCampaignValidation
	}
	var campaignIDs []uint64
	if err := s.repository.db.WithContext(ctx).Model(&FlashSaleCampaign{}).Select("id").
		Where("status IN ?", []CampaignStatus{CampaignStatusEnabled, CampaignStatusEnded, CampaignStatusDisabled}).
		Order("updated_at ASC,id ASC").Limit(limit).Scan(&campaignIDs).Error; err != nil {
		return 0, err
	}
	for _, campaignID := range campaignIDs {
		if _, err := s.ReconcileCampaign(ctx, campaignID); err != nil {
			return 0, err
		}
	}
	return len(campaignIDs), nil
}

func requestConsumesCampaignStock(status string) bool {
	switch status {
	case "accepted", "processing", "order_created", "paid", "failed":
		return true
	default:
		return false
	}
}

func equalReservationMaps(left, right map[uint64]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func requestOrderStateMatches(row reconciliationRequestRow) bool {
	if row.OrderID == nil {
		return row.Status == "accepted" || row.Status == "processing" || row.Status == "failed" || row.Status == "compensated"
	}
	if row.OrderStatus == nil {
		return false
	}
	switch row.Status {
	case "order_created":
		return *row.OrderStatus == "pending_payment"
	case "paid":
		return *row.OrderStatus == "paid" || *row.OrderStatus == "shipped" || *row.OrderStatus == "completed"
	case "canceled":
		return *row.OrderStatus == "canceled"
	default:
		return false
	}
}

func (s *Service) upsertReconciliationAnomaly(ctx context.Context, campaignID uint64, key, anomalyType string, requestID *string, orderID *uint64, expected, actual *int64, detail string, evidence any, resolved bool) error {
	data, _ := json.Marshal(evidence)
	value := ReconciliationAnomaly{
		CampaignID: campaignID, AnomalyKey: key, AnomalyType: anomalyType,
		RequestID: requestID, OrderID: orderID, ExpectedValue: expected, ActualValue: actual,
		Detail: detail, EvidenceJSON: string(data), Status: "open",
	}
	updates := map[string]any{
		"anomaly_type": anomalyType, "request_id": requestID, "order_id": orderID,
		"expected_value": expected, "actual_value": actual, "detail": detail,
		"evidence_json": string(data), "status": "open", "resolved_at": nil, "resolution": nil,
	}
	if resolved {
		now := s.now().UTC()
		resolution := "automatically repaired Redis remaining stock"
		value.Status, value.ResolvedAt, value.Resolution = "resolved", &now, &resolution
		updates["status"], updates["resolved_at"], updates["resolution"] = "resolved", now, resolution
	}
	return s.repository.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "campaign_id"}, {Name: "anomaly_key"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&value).Error
}
