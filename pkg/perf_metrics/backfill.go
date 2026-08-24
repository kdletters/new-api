package perfmetrics

import (
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	defaultDimensionBackfillHours = 24
	dimensionBackfillVersionKey   = "PerfDimensionMetricsBackfillVersion"
	dimensionBackfillVersion      = "v1"
)

type dimensionBackfillStreamStatus struct {
	Status    string `json:"status"`
	EndReason string `json:"end_reason"`
}

type dimensionBackfillLogOther struct {
	CacheTokens int64                         `json:"cache_tokens"`
	Stream      dimensionBackfillStreamStatus `json:"stream_status"`
}

type dimensionBackfillRequest struct {
	userId       int
	userName     string
	tokenId      int
	tokenName    string
	finalTs      int64
	finalLogId   int
	hasConsume   bool
	success      bool
	inputTokens  int64
	cachedTokens int64
}

func runDimensionMetricsBackfill(hours int, now time.Time) error {
	common.OptionMapRWMutex.RLock()
	completed := common.OptionMap[dimensionBackfillVersionKey] == dimensionBackfillVersion
	common.OptionMapRWMutex.RUnlock()
	if completed {
		return nil
	}
	if err := backfillCompletedDimensionMetrics(hours, now); err != nil {
		return err
	}
	return model.UpdateOptionsBulk(map[string]string{
		dimensionBackfillVersionKey: dimensionBackfillVersion,
	})
}

func backfillCompletedDimensionMetrics(hours int, now time.Time) error {
	if hours <= 0 {
		hours = defaultDimensionBackfillHours
	}
	currentBucket := bucketStart(now.Unix())
	startBucket := currentBucket - int64(hours)*int64(time.Hour/time.Second)
	if startBucket >= currentBucket {
		return nil
	}

	requests := make(map[string]dimensionBackfillRequest)
	metrics := make(map[dimensionBucketKey]*model.PerfDimensionMetric)
	err := model.ScanPerfDimensionBackfillLogs(startBucket, now.Unix()+1, func(logs []model.Log) error {
		for i := range logs {
			log := logs[i]
			if strings.TrimSpace(log.RequestId) == "" {
				continue
			}
			other := dimensionBackfillLogOther{}
			if log.Other != "" {
				_ = common.Unmarshal([]byte(log.Other), &other)
			}

			if log.Type == model.LogTypeError {
				if log.CreatedAt < currentBucket {
					addDimensionBackfillMetric(metrics, DimensionChannel, log.ChannelId, log.ChannelName, log.CreatedAt, false, 0, 0)
				}
				request := requests[log.RequestId]
				if !request.hasConsume && (log.CreatedAt > request.finalTs || (log.CreatedAt == request.finalTs && log.Id > request.finalLogId)) {
					request.userId = log.UserId
					request.userName = log.Username
					request.tokenId = log.TokenId
					request.tokenName = log.TokenName
					request.finalTs = log.CreatedAt
					request.finalLogId = log.Id
				}
				requests[log.RequestId] = request
				continue
			}

			if log.Type != model.LogTypeConsume {
				continue
			}
			consumeSuccess := !strings.EqualFold(other.Stream.Status, "error")
			channelSuccess := consumeSuccess || strings.EqualFold(other.Stream.EndReason, "client_gone")
			inputTokens := int64(0)
			cachedTokens := int64(0)
			if consumeSuccess && log.PromptTokens > 0 {
				inputTokens = int64(log.PromptTokens)
				if other.CacheTokens > 0 {
					cachedTokens = other.CacheTokens
				}
			}
			if log.CreatedAt < currentBucket {
				addDimensionBackfillMetric(metrics, DimensionChannel, log.ChannelId, log.ChannelName, log.CreatedAt, channelSuccess, inputTokens, cachedTokens)
			}

			request := requests[log.RequestId]
			if !request.hasConsume || log.CreatedAt > request.finalTs || (log.CreatedAt == request.finalTs && log.Id > request.finalLogId) {
				request = dimensionBackfillRequest{
					userId: log.UserId, userName: log.Username,
					tokenId: log.TokenId, tokenName: log.TokenName,
					finalTs: log.CreatedAt, finalLogId: log.Id,
					hasConsume: true, success: consumeSuccess,
					inputTokens: inputTokens, cachedTokens: cachedTokens,
				}
			}
			requests[log.RequestId] = request
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, request := range requests {
		if request.finalTs < startBucket || request.finalTs >= currentBucket {
			continue
		}
		addDimensionBackfillMetric(metrics, DimensionUser, request.userId, request.userName, request.finalTs, request.success, request.inputTokens, request.cachedTokens)
		if request.tokenId == 0 && strings.TrimSpace(request.tokenName) == "" {
			request.tokenName = InternalTokenName
		}
		addDimensionBackfillMetric(metrics, DimensionToken, request.tokenId, request.tokenName, request.finalTs, request.success, request.inputTokens, request.cachedTokens)
	}

	rows := make([]model.PerfDimensionMetric, 0, len(metrics))
	for _, metric := range metrics {
		rows = append(rows, *metric)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BucketTs != rows[j].BucketTs {
			return rows[i].BucketTs < rows[j].BucketTs
		}
		if rows[i].Dimension != rows[j].Dimension {
			return rows[i].Dimension < rows[j].Dimension
		}
		return rows[i].EntityId < rows[j].EntityId
	})
	return model.ReplacePerfDimensionMetrics(startBucket, currentBucket, rows)
}

func addDimensionBackfillMetric(metrics map[dimensionBucketKey]*model.PerfDimensionMetric, dimension string, entityId int, entityName string, ts int64, success bool, inputTokens int64, cachedTokens int64) {
	if !IsValidDimension(dimension) || entityId < 0 || (dimension != DimensionToken && entityId == 0) {
		return
	}
	key := dimensionBucketKey{dimension: dimension, entityId: entityId, bucketTs: bucketStart(ts)}
	metric := metrics[key]
	if metric == nil {
		metric = &model.PerfDimensionMetric{Dimension: dimension, EntityId: entityId, BucketTs: key.bucketTs}
		metrics[key] = metric
	}
	if strings.TrimSpace(entityName) != "" {
		metric.EntityName = entityName
	}
	metric.RequestCount++
	if success {
		metric.SuccessCount++
	}
	if inputTokens <= 0 {
		return
	}
	metric.CacheEligibleCount++
	metric.InputTokens += inputTokens
	if cachedTokens > 0 {
		metric.CacheHitCount++
		metric.CachedTokens += cachedTokens
	}
}
