package promotion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	domainevent "feedsystem_video_go/internal/event"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SeckillOrderRequestedEvent = "seckill.order.requested"

type ReservationCode string

const (
	ReservationAccepted      ReservationCode = "accepted"
	ReservationNotPreheated  ReservationCode = "not_preheated"
	ReservationDisabled      ReservationCode = "disabled"
	ReservationNotStarted    ReservationCode = "not_started"
	ReservationEnded         ReservationCode = "ended"
	ReservationSoldOut       ReservationCode = "sold_out"
	ReservationLimitExceeded ReservationCode = "limit_exceeded"
	ReservationOrderCreated  ReservationCode = "order_created"
	ReservationFailed        ReservationCode = "failed"
	ReservationPaid          ReservationCode = "paid"
	ReservationCanceled      ReservationCode = "canceled"
)

var ErrReservationNotFound = errors.New("flash sale reservation not found")

type ReservationKeys struct {
	Metadata     string
	Stock        string
	Buyers       string
	Requests     string
	ResultPrefix string
}

type ReserveCommand struct {
	BuyerID          uint64
	IdempotencyKey   string
	Quantity         uint
	RequestID        string
	ReservationToken string
	Now              time.Time
	TTL              time.Duration
}

type ReservationResult struct {
	Code             ReservationCode `json:"code"`
	RequestID        string          `json:"request_id,omitempty"`
	ReservationToken string          `json:"reservation_token,omitempty"`
	Quantity         uint            `json:"quantity,omitempty"`
	RemainingStock   int64           `json:"remaining_stock,omitempty"`
	ReservedByBuyer  int64           `json:"reserved_by_buyer,omitempty"`
	Duplicate        bool            `json:"duplicate,omitempty"`
	BuyerID          uint64          `json:"buyer_id,omitempty"`
	OrderID          uint64          `json:"order_id,omitempty"`
	OrderNo          string          `json:"order_no,omitempty"`
	PaymentExpiresAt *time.Time      `json:"payment_expires_at,omitempty"`
}

type LuaReservationStore struct {
	cache *rediscache.Client
}

func NewLuaReservationStore(cache *rediscache.Client) (*LuaReservationStore, error) {
	if cache == nil {
		return nil, ErrPreheatUnavailable
	}
	return &LuaReservationStore{cache: cache}, nil
}

func (s *LuaReservationStore) Reserve(ctx context.Context, keys ReservationKeys, command ReserveCommand) (*ReservationResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.ReservationToken = strings.TrimSpace(command.ReservationToken)
	if s == nil || s.cache == nil || keys.Metadata == "" || keys.Stock == "" || keys.Buyers == "" || keys.Requests == "" || keys.ResultPrefix == "" ||
		command.BuyerID == 0 || command.IdempotencyKey == "" || len(command.IdempotencyKey) > 128 ||
		command.Quantity == 0 || command.Quantity > 999 || command.RequestID == "" || command.ReservationToken == "" ||
		command.Now.IsZero() || command.TTL <= 0 {
		return nil, ErrCampaignValidation
	}
	requestField := strconv.FormatUint(command.BuyerID, 10) + ":" + command.IdempotencyKey
	value, err := s.cache.EvalString(ctx, reserveCampaignScript, []string{keys.Metadata, keys.Stock, keys.Buyers, keys.Requests, keys.ResultPrefix + command.RequestID},
		command.Now.UTC().UnixMilli(), command.BuyerID, requestField, command.Quantity,
		command.RequestID, command.ReservationToken, command.TTL.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPreheatUnavailable, err)
	}
	var result ReservationResult
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("decode flash sale reservation result: %w", err)
	}
	return &result, nil
}

func (s *Service) ReserveCampaign(ctx context.Context, campaignID uint64, buyerID uint64, idempotencyKey string, quantity uint) (*ReservationResult, error) {
	if campaignID == 0 || buyerID == 0 || strings.TrimSpace(idempotencyKey) == "" || quantity == 0 {
		return nil, ErrCampaignValidation
	}
	if s.cache == nil {
		return nil, ErrPreheatUnavailable
	}
	campaign, err := s.GetCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	if campaign.Status != CampaignStatusEnabled {
		return &ReservationResult{Code: ReservationDisabled}, nil
	}
	randomID, err := randomReservationID(16)
	if err != nil {
		return nil, err
	}
	requestID := fmt.Sprintf("c%d-v%d-%s", campaign.ID, campaign.CampaignVersion, randomID)
	token, err := randomReservationID(32)
	if err != nil {
		return nil, err
	}
	store, _ := NewLuaReservationStore(s.cache)
	now := s.now().UTC()
	ttl := campaign.EndsAt.Sub(now) + time.Hour
	if ttl <= 0 {
		ttl = time.Hour
	}
	result, err := store.Reserve(ctx, ReservationKeys{
		Metadata:     CampaignMetadataKey(s.cache, campaign.ID, campaign.CampaignVersion),
		Stock:        CampaignStockKey(s.cache, campaign.ID, campaign.CampaignVersion),
		Buyers:       CampaignBuyersKey(s.cache, campaign.ID, campaign.CampaignVersion),
		Requests:     CampaignRequestsKey(s.cache, campaign.ID, campaign.CampaignVersion),
		ResultPrefix: CampaignRequestResultPrefix(s.cache, campaign.ID, campaign.CampaignVersion),
	}, ReserveCommand{
		BuyerID: buyerID, IdempotencyKey: idempotencyKey, Quantity: quantity,
		RequestID: requestID, ReservationToken: token, Now: now, TTL: ttl,
	})
	if err != nil || result.Code != ReservationAccepted {
		return result, err
	}
	if err := s.persistAcceptedReservation(ctx, campaign, buyerID, strings.TrimSpace(idempotencyKey), result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) persistAcceptedReservation(ctx context.Context, campaign *FlashSaleCampaign, buyerID uint64, idempotencyKey string, result *ReservationResult) error {
	if campaign == nil || result == nil || result.Code != ReservationAccepted {
		return ErrCampaignValidation
	}
	return s.events.Transact(ctx, func(tx *gorm.DB, outbox *domainevent.Writer) error {
		acceptedAt := s.now().UTC()
		request := &FlashSaleRequest{
			RequestID: result.RequestID, CampaignID: campaign.ID, BuyerID: buyerID,
			IdempotencyKey: idempotencyKey, ReservationToken: result.ReservationToken,
			Quantity: result.Quantity, Status: "accepted", AcceptedAt: acceptedAt,
		}
		insert := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(request)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			var existing FlashSaleRequest
			if err := tx.WithContext(ctx).Where("campaign_id=? AND buyer_id=? AND idempotency_key=?", campaign.ID, buyerID, idempotencyKey).First(&existing).Error; err != nil {
				return err
			}
			if existing.RequestID != result.RequestID || existing.ReservationToken != result.ReservationToken || existing.Quantity != result.Quantity {
				return ErrCampaignValidation
			}
			return nil
		}
		_, err := outbox.Append(ctx, domainevent.DomainEvent{
			AggregateType: "flash_sale_request", AggregateID: result.RequestID,
			EventType: SeckillOrderRequestedEvent,
			Payload: map[string]any{
				"request_id": result.RequestID, "reservation_token": result.ReservationToken,
				"campaign_id": campaign.ID, "campaign_version": campaign.CampaignVersion,
				"buyer_id": buyerID, "sku_id": campaign.SKUID, "quantity": result.Quantity,
				"price_cent": campaign.PriceCent, "accepted_at": acceptedAt.Format(time.RFC3339Nano),
			},
		})
		return err
	})
}

func (s *Service) GetReservationStatus(ctx context.Context, buyerID uint64, requestID string) (*ReservationResult, error) {
	requestID = strings.TrimSpace(requestID)
	if buyerID == 0 || requestID == "" || s.cache == nil {
		return nil, ErrCampaignValidation
	}
	campaignID, version, err := parseReservationRequestID(requestID)
	if err != nil {
		return nil, ErrReservationNotFound
	}
	value, err := s.cache.GetBytes(ctx, CampaignRequestResultKey(s.cache, campaignID, version, requestID))
	if rediscache.IsMiss(err) {
		return nil, ErrReservationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPreheatUnavailable, err)
	}
	var result ReservationResult
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, fmt.Errorf("decode flash sale request status: %w", err)
	}
	if result.BuyerID != buyerID {
		return nil, ErrReservationNotFound
	}
	return &result, nil
}

func CampaignRequestResultKey(cache *rediscache.Client, campaignID, version uint64, requestID string) string {
	return CampaignRequestResultPrefix(cache, campaignID, version) + requestID
}

func CampaignRequestResultPrefix(cache *rediscache.Client, campaignID, version uint64) string {
	return cache.Key("flashsale:campaign:%d:v%d:result:", campaignID, version)
}

func parseReservationRequestID(value string) (uint64, uint64, error) {
	parts := strings.SplitN(value, "-", 3)
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "c") || !strings.HasPrefix(parts[1], "v") || parts[2] == "" {
		return 0, 0, ErrReservationNotFound
	}
	campaignID, err := strconv.ParseUint(strings.TrimPrefix(parts[0], "c"), 10, 64)
	if err != nil || campaignID == 0 {
		return 0, 0, ErrReservationNotFound
	}
	version, err := strconv.ParseUint(strings.TrimPrefix(parts[1], "v"), 10, 64)
	if err != nil || version == 0 {
		return 0, 0, ErrReservationNotFound
	}
	return campaignID, version, nil
}

func randomReservationID(bytes int) (string, error) {
	if bytes <= 0 {
		return "", errors.New("reservation random byte count must be positive")
	}
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

const reserveCampaignScript = `
local metadata_raw = redis.call('GET', KEYS[1])
local existing = redis.call('HGET', KEYS[4], ARGV[3])
if existing then
  local previous = cjson.decode(existing)
  previous['duplicate'] = true
  return cjson.encode(previous)
end

local function persist(result)
  local encoded = cjson.encode(result)
  redis.call('HSET', KEYS[4], ARGV[3], encoded)
  redis.call('PEXPIRE', KEYS[4], ARGV[7])
  if result['request_id'] then
    redis.call('SET', KEYS[5], encoded, 'PX', ARGV[7])
  end
  return encoded
end

if not metadata_raw then
  return cjson.encode({code='not_preheated'})
end
local ok, metadata = pcall(cjson.decode, metadata_raw)
if not ok or metadata['status'] ~= 'enabled' then
  return persist({code='disabled'})
end

local now_ms = tonumber(ARGV[1])
if now_ms < tonumber(metadata['starts_at_ms']) then
  return persist({code='not_started'})
end
if now_ms >= tonumber(metadata['ends_at_ms']) then
  return persist({code='ended'})
end

local quantity = tonumber(ARGV[4])
local buyer_field = tostring(ARGV[2])
local buyer_reserved = tonumber(redis.call('HGET', KEYS[3], buyer_field) or '0')
if buyer_reserved + quantity > tonumber(metadata['per_user_limit']) then
  return persist({code='limit_exceeded', reserved_by_buyer=buyer_reserved})
end

local stock = tonumber(redis.call('GET', KEYS[2]) or '-1')
if stock < quantity then
  return persist({code='sold_out', remaining_stock=math.max(stock, 0)})
end

local remaining = redis.call('DECRBY', KEYS[2], quantity)
local reserved = redis.call('HINCRBY', KEYS[3], buyer_field, quantity)
redis.call('PEXPIRE', KEYS[2], ARGV[7])
redis.call('PEXPIRE', KEYS[3], ARGV[7])
return persist({
  code='accepted', request_id=ARGV[5], reservation_token=ARGV[6],
  buyer_id=tonumber(ARGV[2]), quantity=quantity,
  remaining_stock=remaining, reserved_by_buyer=reserved
})
`
