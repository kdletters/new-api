package perfmetrics

import (
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
)

func flushLoop() {
	for {
		interval := perf_metrics_setting.GetFlushIntervalMinutes()
		time.Sleep(time.Duration(interval) * time.Minute)
		setting := perf_metrics_setting.GetSetting()
		if !setting.Enabled {
			continue
		}
		flushCompletedBuckets()
		flushCompletedDimensionBuckets()
		cleanupExpiredMetrics(setting.RetentionDays)
	}
}

func flushCompletedDimensionBuckets() {
	currentBucket := bucketStart(time.Now().Unix())
	dimensionHotBuckets.Range(func(rawKey, rawValue any) bool {
		key := rawKey.(dimensionBucketKey)
		if key.bucketTs >= currentBucket {
			return true
		}

		bucket := rawValue.(*atomicDimensionBucket)
		name, drained := bucket.drain()
		if drained.requestCount == 0 {
			deleteOldEmptyDimensionBucket(key, rawKey)
			return true
		}

		err := model.UpsertPerfDimensionMetric(&model.PerfDimensionMetric{
			Dimension:          key.dimension,
			EntityId:           key.entityId,
			EntityName:         name,
			BucketTs:           key.bucketTs,
			RequestCount:       drained.requestCount,
			SuccessCount:       drained.successCount,
			CacheEligibleCount: drained.cacheEligibleCount,
			CacheHitCount:      drained.cacheHitCount,
			InputTokens:        drained.inputTokens,
			CachedTokens:       drained.cachedTokens,
		})
		if err != nil {
			bucket.addCounters(name, drained)
			common.SysError(fmt.Sprintf("failed to flush perf dimension metric dimension=%s entity=%d bucket=%d: %s", key.dimension, key.entityId, key.bucketTs, err.Error()))
			return true
		}

		deleteOldEmptyDimensionBucket(key, rawKey)
		return true
	})
}

func flushCompletedBuckets() {
	currentBucket := bucketStart(time.Now().Unix())
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.bucketTs >= currentBucket {
			return true
		}

		bucket := value.(*atomicBucket)
		drained := bucket.drain()
		if drained.requestCount == 0 {
			deleteOldEmptyBucket(k, key)
			return true
		}

		err := model.UpsertPerfMetric(&model.PerfMetric{
			ModelName:      k.model,
			Group:          k.group,
			BucketTs:       k.bucketTs,
			RequestCount:   drained.requestCount,
			SuccessCount:   drained.successCount,
			TotalLatencyMs: drained.totalLatencyMs,
			TtftSumMs:      drained.ttftSumMs,
			TtftCount:      drained.ttftCount,
			OutputTokens:   drained.outputTokens,
			GenerationMs:   drained.generationMs,
		})
		if err != nil {
			bucket.addCounters(drained)
			common.SysError(fmt.Sprintf("failed to flush perf metric bucket model=%s group=%s bucket=%d: %s", k.model, k.group, k.bucketTs, err.Error()))
			return true
		}

		deleteOldEmptyBucket(k, key)
		return true
	})
}

func deleteOldEmptyBucket(k bucketKey, rawKey any) {
	if k.bucketTs < bucketStart(time.Now().Add(-24*time.Hour).Unix()) {
		hotBuckets.Delete(rawKey)
	}
}

func deleteOldEmptyDimensionBucket(key dimensionBucketKey, rawKey any) {
	if key.bucketTs < bucketStart(time.Now().Add(-24*time.Hour).Unix()) {
		dimensionHotBuckets.Delete(rawKey)
	}
}

func cleanupExpiredMetrics(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	if err := model.DeletePerfMetricsBefore(cutoff); err != nil {
		common.SysError("failed to cleanup expired perf metrics: " + err.Error())
	}
	if err := model.DeletePerfDimensionMetricsBefore(cutoff); err != nil {
		common.SysError("failed to cleanup expired perf dimension metrics: " + err.Error())
	}
}

func redisCounters(values map[string]string) counters {
	return counters{
		requestCount:   parseRedisInt(values["req"]),
		successCount:   parseRedisInt(values["ok"]),
		totalLatencyMs: parseRedisInt(values["lat"]),
		ttftSumMs:      parseRedisInt(values["ttft"]),
		ttftCount:      parseRedisInt(values["ttft_n"]),
		outputTokens:   parseRedisInt(values["out"]),
		generationMs:   parseRedisInt(values["gen_ms"]),
	}
}

func parseRedisInt(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}
