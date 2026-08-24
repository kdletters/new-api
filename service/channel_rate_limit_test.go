package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisChannelRPMIsSharedByAllCallers(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: server.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		require.NoError(t, clientA.Close())
		require.NoError(t, clientB.Close())
	})

	allowed, _, err := takeRedisChannelRPM(context.Background(), clientA, 42, 2)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, _, err = takeRedisChannelRPM(context.Background(), clientB, 42, 2)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, retryAfter, err := takeRedisChannelRPM(context.Background(), clientA, 42, 2)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Positive(t, retryAfter)

	otherChannelAllowed, _, err := takeRedisChannelRPM(context.Background(), clientB, 43, 2)
	require.NoError(t, err)
	assert.True(t, otherChannelAllowed)
}

func TestMemoryChannelRPMResetsAfterWindow(t *testing.T) {
	limiter := &memoryChannelRPMLimiter{}
	now := time.Unix(1_700_000_000, 0)

	allowed, _ := limiter.take(now, 7, 1)
	assert.True(t, allowed)
	allowed, retryAfter := limiter.take(now.Add(10*time.Second), 7, 1)
	assert.False(t, allowed)
	assert.Equal(t, 50*time.Second, retryAfter)
	allowed, _ = limiter.take(now.Add(time.Minute), 7, 1)
	assert.True(t, allowed)
}

func TestMemoryChannelRPMIsConcurrencySafe(t *testing.T) {
	limiter := &memoryChannelRPMLimiter{}
	now := time.Unix(1_700_000_000, 0)
	var allowedCount atomic.Int64
	var wg sync.WaitGroup

	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _ := limiter.take(now, 9, 5)
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(5), allowedCount.Load())
}

func TestChannelRPMZeroMeansUnlimited(t *testing.T) {
	limiter := &memoryChannelRPMLimiter{}
	for range 10 {
		allowed, retryAfter := limiter.take(time.Now(), 11, 0)
		assert.True(t, allowed)
		assert.Zero(t, retryAfter)
	}
}
