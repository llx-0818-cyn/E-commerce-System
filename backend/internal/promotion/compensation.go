package promotion

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type CompensationCode string

const (
	CompensationApplied   CompensationCode = "compensated"
	CompensationDuplicate CompensationCode = "duplicate"
	CompensationNotFound  CompensationCode = "not_found"
	CompensationMismatch  CompensationCode = "mismatch"
)

type CompensationCommand struct {
	BuyerID          uint64
	IdempotencyKey   string
	RequestID        string
	ReservationToken string
	Quantity         uint
	Reason           string
	ResultCode       ReservationCode
	TTL              time.Duration
}

type CompensationResult struct {
	Code            CompensationCode `json:"code"`
	RequestID       string           `json:"request_id,omitempty"`
	RemainingStock  int64            `json:"remaining_stock,omitempty"`
	ReservedByBuyer int64            `json:"reserved_by_buyer,omitempty"`
}

func (s *LuaReservationStore) Compensate(ctx context.Context, keys ReservationKeys, command CompensationCommand) (*CompensationResult, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.ReservationToken = strings.TrimSpace(command.ReservationToken)
	command.Reason = strings.TrimSpace(command.Reason)
	if command.ResultCode == "" {
		command.ResultCode = ReservationFailed
	}
	if s == nil || s.cache == nil || keys.Metadata == "" || keys.Stock == "" || keys.Buyers == "" || keys.Requests == "" || keys.ResultPrefix == "" ||
		command.BuyerID == 0 || command.IdempotencyKey == "" || command.RequestID == "" || command.ReservationToken == "" ||
		command.Quantity == 0 || command.Reason == "" || command.TTL <= 0 {
		return nil, ErrCampaignValidation
	}
	requestField := strconv.FormatUint(command.BuyerID, 10) + ":" + command.IdempotencyKey
	value, err := s.cache.EvalString(ctx, compensateCampaignScript,
		[]string{keys.Metadata, keys.Stock, keys.Buyers, keys.Requests, keys.ResultPrefix + command.RequestID},
		command.BuyerID, requestField, command.Quantity, command.RequestID,
		command.ReservationToken, command.Reason, command.TTL.Milliseconds(), string(command.ResultCode))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPreheatUnavailable, err)
	}
	var result CompensationResult
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("decode flash sale compensation result: %w", err)
	}
	return &result, nil
}

const compensateCampaignScript = `
local metadata_raw = redis.call('GET', KEYS[1])
local request_raw = redis.call('HGET', KEYS[4], ARGV[2])
if not metadata_raw or not request_raw then
  return cjson.encode({code='not_found', request_id=ARGV[4]})
end
local metadata = cjson.decode(metadata_raw)
local request = cjson.decode(request_raw)
if request['request_id'] ~= ARGV[4] or request['reservation_token'] ~= ARGV[5] then
  return cjson.encode({code='mismatch', request_id=ARGV[4]})
end
if request['compensated'] then
  return cjson.encode({
    code='duplicate', request_id=ARGV[4],
    remaining_stock=tonumber(redis.call('GET', KEYS[2]) or '0'),
    reserved_by_buyer=tonumber(redis.call('HGET', KEYS[3], tostring(ARGV[1])) or '0')
  })
end
if request['code'] ~= 'accepted' then
  return cjson.encode({code='mismatch', request_id=ARGV[4]})
end

local quantity = tonumber(ARGV[3])
local stock = tonumber(redis.call('GET', KEYS[2]) or '0')
local total = tonumber(metadata['total_stock'])
local restored = math.min(stock + quantity, total)
redis.call('SET', KEYS[2], restored, 'PX', ARGV[7])

local buyer_field = tostring(ARGV[1])
local reserved = tonumber(redis.call('HGET', KEYS[3], buyer_field) or '0') - quantity
if reserved <= 0 then
  reserved = 0
  redis.call('HDEL', KEYS[3], buyer_field)
else
  redis.call('HSET', KEYS[3], buyer_field, reserved)
  redis.call('PEXPIRE', KEYS[3], ARGV[7])
end

request['code'] = ARGV[8]
request['compensated'] = true
request['failure_reason'] = ARGV[6]
request['remaining_stock'] = restored
request['reserved_by_buyer'] = reserved
local encoded = cjson.encode(request)
redis.call('HSET', KEYS[4], ARGV[2], encoded)
redis.call('PEXPIRE', KEYS[4], ARGV[7])
redis.call('SET', KEYS[5], encoded, 'PX', ARGV[7])
return cjson.encode({
  code='compensated', request_id=ARGV[4],
  remaining_stock=restored, reserved_by_buyer=reserved
})
`
