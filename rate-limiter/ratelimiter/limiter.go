package ratelimiter

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

type SlidingWindowCounter struct {
	client     *redis.Client
	limit      int
	windowSize time.Duration
}

func NewSlidingWindowCounter(client *redis.Client, limit int, windowSize time.Duration) *SlidingWindowCounter {
	return &SlidingWindowCounter{
		client:     client,
		limit:      limit,
		windowSize: windowSize,
	}
}

type Result struct {
	Allowed       bool
	CurrentCount  int
	Limit         int
	Remaining     int
	RetryAfterMs  int64
	WeightedCount float64
}

var luaScript = redis.NewScript(`

      local prev_key =KEYS[1]
	  local curr_key = KEYS[2]
	  local limit =tonumber(ARGV[1])
	  local window_ms = tonumber(ARGV[2])
	  local elapsed_ms= tonumber(ARGV[3])
	

	--Step 1: Get previous window count (0 if expired/missing)
	  local prev_count=tonumber(redis.call('GET',prev_key)or"0")

    -- Step 2: Get current window count

	 local curr_count=tonumber(redis.call('GET',curr_key)or "0")

	--   Step 3: Sliding window weighted estimate
	--   overlap_fraction = how much of the previous window still "overlaps"
	--   Example: if we're 300ms into a 1000ms window,
	--   overlap = (1000 - 300) / 1000 = 0.7
	--   so 70% of previous window's requests still count
 
	local overlap_fraction=math.max(0,(window_ms-elapsed_ms)/window_ms)
	local weighted= (prev_count*overlap_fraction) +curr_count

	--Step 4: Decide

	if weighted >= limit then
	     -- DENIED — don't increment (we only count successful requests)
		 return {0,curr_count,tostring(weighted)}
	end

	-- Step 5: ALLOWED — atomically increment + set TTL

	curr_count=redis.call('INCR',curr_key)

	-- TTL = 2x window so the "previous" window data survives long enough
	-- for the next window's overlap calculation

	redis.call('PEXPIRE',curr_key,window_ms*2)

	weighted=(prev_count*overlap_fraction)+curr_count

    return {1,curr_count,tostring(weighted)}

`)

func (s *SlidingWindowCounter) Allow(ctx context.Context, key string) (*Result, error) {
	now := time.Now()

	windowMs := s.windowSize.Milliseconds()

	currentWindow := now.UnixMilli() / windowMs * windowMs
	previousWindow := currentWindow - windowMs

	elapsedMs := now.UnixMilli() - currentWindow

	currKey := fmt.Sprintf("ratelimit:%s:%d", key, currentWindow)

	prevKey := fmt.Sprintf("ratelimit:%s:%d", key, previousWindow)

	raw, err := luaScript.Run(ctx, s.client, []string{prevKey, currKey}, s.limit, windowMs, elapsedMs).Slice()

	if err != nil {
		return nil, fmt.Errorf("redis lua script failed: %w", err)
	}

	allowedVal, _ := raw[0].(int64)
	currentCount64, _ := raw[1].(int64)
	allowed := allowedVal == 1
	currentCount := int(currentCount64)

	var weighted float64
	if ws, ok := raw[2].(string); ok {
		fmt.Sscanf(ws, "%f", &weighted)
	}
	remaining := s.limit - int(math.Ceil(weighted))
	if remaining < 0 {
		remaining = 0
	}

	retryAfter := windowMs - elapsedMs

	return &Result{
		Allowed:       allowed,
		CurrentCount:  currentCount,
		Limit:         s.limit,
		Remaining:     remaining,
		RetryAfterMs:  retryAfter,
		WeightedCount: weighted,
	}, nil

}
