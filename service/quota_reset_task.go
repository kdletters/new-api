package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	quotaResetTickInterval      = 1 * time.Minute
	quotaResetBatchSize         = 300
	subscriptionCleanupInterval = 30 * time.Minute
)

var (
	quotaResetOnce          sync.Once
	quotaResetRunning       atomic.Bool
	subscriptionCleanupLast atomic.Int64
)

func StartQuotaResetTask() {
	quotaResetOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText("quota reset task started: tick=%s", quotaResetTickInterval))
			ticker := time.NewTicker(quotaResetTickInterval)
			defer ticker.Stop()

			runQuotaResetOnce()
			for range ticker.C {
				runQuotaResetOnce()
			}
		})
	})
}

func runQuotaResetOnce() {
	if !quotaResetRunning.CompareAndSwap(false, true) {
		return
	}
	defer quotaResetRunning.Store(false)

	ctx := context.Background()
	totalReset := 0
	totalTokenReset := 0
	totalExpired := 0
	for {
		n, err := model.ExpireDueSubscriptions(quotaResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, common.LogText("subscription expire task failed: %v", err))
			break
		}
		if n == 0 {
			break
		}
		totalExpired += n
		if n < quotaResetBatchSize {
			break
		}
	}
	for {
		n, err := model.ResetDueSubscriptions(quotaResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, common.LogText("subscription quota reset task failed: %v", err))
			break
		}
		if n == 0 {
			break
		}
		totalReset += n
		if n < quotaResetBatchSize {
			break
		}
	}
	for {
		n, err := model.ResetDueTokens(quotaResetBatchSize)
		if err != nil {
			logger.LogWarn(ctx, common.LogText("token quota reset task failed: %v", err))
			break
		}
		if n == 0 {
			break
		}
		totalTokenReset += n
		if n < quotaResetBatchSize {
			break
		}
	}
	lastCleanup := time.Unix(subscriptionCleanupLast.Load(), 0)
	if time.Since(lastCleanup) >= subscriptionCleanupInterval {
		if _, err := model.CleanupSubscriptionPreConsumeRecords(7 * 24 * 3600); err == nil {
			subscriptionCleanupLast.Store(time.Now().Unix())
		}
	}
	if common.DebugEnabled && (totalReset > 0 || totalTokenReset > 0 || totalExpired > 0) {
		logger.LogDebug(ctx, "quota reset maintenance: subscription_reset_count=%d, token_reset_count=%d, expired_subscription_count=%d", totalReset, totalTokenReset, totalExpired)
	}
}
