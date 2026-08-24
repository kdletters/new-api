package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/go-redis/redis/v8"
)

const (
	channelRPMRedisNamespace = "channel_rpm:v1"
	channelRPMWindow         = time.Minute
)

const channelRPMRedisScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  ttl = tonumber(ARGV[2])
end
if count > tonumber(ARGV[1]) then
  return {0, ttl}
end
return {1, ttl}
`

type channelRPMWindowState struct {
	startedAt time.Time
	count     int
}

type memoryChannelRPMLimiter struct {
	mu      sync.Mutex
	windows map[int]channelRPMWindowState
}

var channelRPMFallbackLimiter memoryChannelRPMLimiter

// TakeChannelRPM reserves one request in the selected channel's one-minute
// window. Redis is authoritative when available so all instances share the
// same count. A Redis failure falls back to an independent, concurrency-safe
// in-process window instead of failing open.
func TakeChannelRPM(ctx context.Context, channelID int, rpm int) (bool, time.Duration) {
	if rpm <= 0 {
		return true, 0
	}

	if common.RedisEnabled && common.RDB != nil {
		allowed, retryAfter, err := takeRedisChannelRPM(ctx, common.RDB, channelID, rpm)
		if err == nil {
			return allowed, retryAfter
		}
		logger.LogWarn(ctx, fmt.Sprintf("channel RPM Redis check failed for channel #%d; using in-process fallback: %v", channelID, err))
	} else if common.RedisEnabled {
		logger.LogWarn(ctx, fmt.Sprintf("channel RPM Redis client is unavailable for channel #%d; using in-process fallback", channelID))
	}

	return channelRPMFallbackLimiter.take(time.Now(), channelID, rpm)
}

func takeRedisChannelRPM(ctx context.Context, client *redis.Client, channelID int, rpm int) (bool, time.Duration, error) {
	if client == nil {
		return false, 0, errors.New("Redis client is nil")
	}
	if channelID <= 0 {
		return false, 0, errors.New("channel ID must be positive")
	}
	if rpm <= 0 {
		return true, 0, nil
	}

	key := fmt.Sprintf("%s:%d", channelRPMRedisNamespace, channelID)
	values, err := client.Eval(ctx, channelRPMRedisScript, []string{key}, rpm, channelRPMWindow.Milliseconds()).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(values) != 2 {
		return false, 0, fmt.Errorf("unexpected channel RPM Redis reply length %d", len(values))
	}
	allowedValue, ok := values[0].(int64)
	if !ok {
		return false, 0, fmt.Errorf("unexpected channel RPM Redis allowed reply type %T", values[0])
	}
	ttlMilliseconds, ok := values[1].(int64)
	if !ok {
		return false, 0, fmt.Errorf("unexpected channel RPM Redis TTL reply type %T", values[1])
	}
	if ttlMilliseconds < 1 {
		ttlMilliseconds = 1
	}
	return allowedValue == 1, time.Duration(ttlMilliseconds) * time.Millisecond, nil
}

func (limiter *memoryChannelRPMLimiter) take(now time.Time, channelID int, rpm int) (bool, time.Duration) {
	if rpm <= 0 {
		return true, 0
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.windows == nil {
		limiter.windows = make(map[int]channelRPMWindowState)
	}

	state, exists := limiter.windows[channelID]
	if !exists || now.Sub(state.startedAt) >= channelRPMWindow || now.Before(state.startedAt) {
		limiter.windows[channelID] = channelRPMWindowState{startedAt: now, count: 1}
		return true, 0
	}
	if state.count >= rpm {
		return false, channelRPMWindow - now.Sub(state.startedAt)
	}
	state.count++
	limiter.windows[channelID] = state
	return true, 0
}
