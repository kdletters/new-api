package perfmetrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/go-redis/redis/v8"
)

const (
	DimensionChannel = "channel"
	DimensionUser    = "user"
	DimensionToken   = "token"

	InternalTokenName = "内部调用"
	maxDimensionHours = 24 * 30
)

type DimensionUsage struct {
	CacheEligible bool
	InputTokens   int64
	CachedTokens  int64
}

type DimensionItem struct {
	Id                 int     `json:"id"`
	Name               string  `json:"name"`
	RequestCount       int64   `json:"request_count"`
	SuccessCount       int64   `json:"success_count"`
	FailureCount       int64   `json:"failure_count"`
	SuccessRate        float64 `json:"success_rate"`
	CacheEligibleCount int64   `json:"cache_eligible_count"`
	CacheHitCount      int64   `json:"cache_hit_count"`
	CacheHitRate       float64 `json:"cache_hit_rate"`
	InputTokens        int64   `json:"input_tokens"`
	CachedTokens       int64   `json:"cached_tokens"`
	CachedTokenRate    float64 `json:"cached_token_rate"`
}

type DimensionQueryResult struct {
	Dimension string          `json:"dimension"`
	Hours     int             `json:"hours"`
	Items     []DimensionItem `json:"items"`
}

type dimensionSample struct {
	dimension     string
	entityId      int
	entityName    string
	success       bool
	cacheEligible bool
	inputTokens   int64
	cachedTokens  int64
}

type dimensionBucketKey struct {
	dimension string
	entityId  int
	bucketTs  int64
}

type dimensionCounters struct {
	requestCount       int64
	successCount       int64
	cacheEligibleCount int64
	cacheHitCount      int64
	inputTokens        int64
	cachedTokens       int64
}

type atomicDimensionBucket struct {
	nameMu             sync.RWMutex
	name               string
	requestCount       atomic.Int64
	successCount       atomic.Int64
	cacheEligibleCount atomic.Int64
	cacheHitCount      atomic.Int64
	inputTokens        atomic.Int64
	cachedTokens       atomic.Int64
}

var dimensionHotBuckets sync.Map

func IsValidDimension(dimension string) bool {
	switch dimension {
	case DimensionChannel, DimensionUser, DimensionToken:
		return true
	default:
		return false
	}
}

func RecordRelayDimensionSuccess(info *relaycommon.RelayInfo, channelName string, userName string, tokenName string, usage DimensionUsage) {
	if info == nil {
		return
	}
	recordDimension(dimensionSample{
		dimension: DimensionChannel, entityId: info.ChannelId, entityName: channelName,
		success: true, cacheEligible: usage.CacheEligible, inputTokens: usage.InputTokens, cachedTokens: usage.CachedTokens,
	})
	recordDimension(dimensionSample{
		dimension: DimensionUser, entityId: info.UserId, entityName: userName,
		success: true, cacheEligible: usage.CacheEligible, inputTokens: usage.InputTokens, cachedTokens: usage.CachedTokens,
	})
	if info.TokenId == 0 && strings.TrimSpace(tokenName) == "" {
		tokenName = InternalTokenName
	}
	recordDimension(dimensionSample{
		dimension: DimensionToken, entityId: info.TokenId, entityName: tokenName,
		success: true, cacheEligible: usage.CacheEligible, inputTokens: usage.InputTokens, cachedTokens: usage.CachedTokens,
	})
}

// RecordChannelAttemptFailure records one failed upstream attempt. A retried
// request may therefore increment multiple channels, which is intentional for
// channel-quality reporting.
func RecordChannelAttemptFailure(channelId int, channelName string) {
	recordDimension(dimensionSample{
		dimension:  DimensionChannel,
		entityId:   channelId,
		entityName: channelName,
	})
}

// RecordFinalRequestFailure records only the final request outcome for user and
// API-token dimensions. Intermediate retry failures must not call this function.
func RecordFinalRequestFailure(userId int, userName string, tokenId int, tokenName string) {
	recordDimension(dimensionSample{dimension: DimensionUser, entityId: userId, entityName: userName})
	if tokenId == 0 && strings.TrimSpace(tokenName) == "" {
		tokenName = InternalTokenName
	}
	recordDimension(dimensionSample{dimension: DimensionToken, entityId: tokenId, entityName: tokenName})
}

func recordDimension(sample dimensionSample) {
	if !perf_metrics_setting.GetSetting().Enabled || !IsValidDimension(sample.dimension) {
		return
	}
	if sample.entityId < 0 || (sample.dimension != DimensionToken && sample.entityId == 0) {
		return
	}
	if sample.inputTokens < 0 {
		sample.inputTokens = 0
	}
	if sample.cachedTokens < 0 {
		sample.cachedTokens = 0
	}
	if sample.cachedTokens > 0 {
		sample.cacheEligible = true
	}
	if !sample.cacheEligible {
		sample.inputTokens = 0
		sample.cachedTokens = 0
	}

	key := dimensionBucketKey{
		dimension: sample.dimension,
		entityId:  sample.entityId,
		bucketTs:  bucketStart(time.Now().Unix()),
	}
	actual, _ := dimensionHotBuckets.LoadOrStore(key, &atomicDimensionBucket{})
	actual.(*atomicDimensionBucket).add(sample)
	recordDimensionRedis(key, sample)
}

func (b *atomicDimensionBucket) add(sample dimensionSample) {
	if name := strings.TrimSpace(sample.entityName); name != "" {
		b.nameMu.Lock()
		b.name = name
		b.nameMu.Unlock()
	}
	b.requestCount.Add(1)
	if sample.success {
		b.successCount.Add(1)
	}
	if sample.cacheEligible {
		b.cacheEligibleCount.Add(1)
		if sample.cachedTokens > 0 {
			b.cacheHitCount.Add(1)
		}
		b.inputTokens.Add(sample.inputTokens)
		b.cachedTokens.Add(sample.cachedTokens)
	}
}

func (b *atomicDimensionBucket) snapshot() (string, dimensionCounters) {
	b.nameMu.RLock()
	name := b.name
	b.nameMu.RUnlock()
	return name, dimensionCounters{
		requestCount:       b.requestCount.Load(),
		successCount:       b.successCount.Load(),
		cacheEligibleCount: b.cacheEligibleCount.Load(),
		cacheHitCount:      b.cacheHitCount.Load(),
		inputTokens:        b.inputTokens.Load(),
		cachedTokens:       b.cachedTokens.Load(),
	}
}

func (b *atomicDimensionBucket) drain() (string, dimensionCounters) {
	b.nameMu.RLock()
	name := b.name
	b.nameMu.RUnlock()
	return name, dimensionCounters{
		requestCount:       b.requestCount.Swap(0),
		successCount:       b.successCount.Swap(0),
		cacheEligibleCount: b.cacheEligibleCount.Swap(0),
		cacheHitCount:      b.cacheHitCount.Swap(0),
		inputTokens:        b.inputTokens.Swap(0),
		cachedTokens:       b.cachedTokens.Swap(0),
	}
}

func (b *atomicDimensionBucket) addCounters(name string, value dimensionCounters) {
	if name = strings.TrimSpace(name); name != "" {
		b.nameMu.Lock()
		b.name = name
		b.nameMu.Unlock()
	}
	b.requestCount.Add(value.requestCount)
	b.successCount.Add(value.successCount)
	b.cacheEligibleCount.Add(value.cacheEligibleCount)
	b.cacheHitCount.Add(value.cacheHitCount)
	b.inputTokens.Add(value.inputTokens)
	b.cachedTokens.Add(value.cachedTokens)
}

type dimensionAggregate struct {
	name     string
	latestTs int64
	counters dimensionCounters
}

func QueryDimensions(dimension string, hours int) (DimensionQueryResult, error) {
	dimension = strings.TrimSpace(dimension)
	hours = normalizeDimensionHours(hours)
	result := DimensionQueryResult{Dimension: dimension, Hours: hours, Items: []DimensionItem{}}
	if !IsValidDimension(dimension) {
		return result, fmt.Errorf("invalid dimension: %s", dimension)
	}

	endTs := time.Now().Unix()
	startTs := endTs - int64(hours)*3600
	rows, err := model.GetPerfDimensionMetrics(dimension, startTs, endTs)
	if err != nil {
		return result, err
	}

	aggregates := make(map[int]dimensionAggregate)
	for _, row := range rows {
		mergeDimensionAggregate(aggregates, row.EntityId, row.EntityName, row.BucketTs, dimensionCounters{
			requestCount:       row.RequestCount,
			successCount:       row.SuccessCount,
			cacheEligibleCount: row.CacheEligibleCount,
			cacheHitCount:      row.CacheHitCount,
			inputTokens:        row.InputTokens,
			cachedTokens:       row.CachedTokens,
		})
	}

	currentBucket := bucketStart(endTs)
	redisMerged := mergeRedisDimensionActiveBucket(aggregates, dimension, currentBucket)
	dimensionHotBuckets.Range(func(rawKey, rawValue any) bool {
		key := rawKey.(dimensionBucketKey)
		if key.dimension != dimension || key.bucketTs < startTs || key.bucketTs > endTs {
			return true
		}
		if redisMerged && key.bucketTs == currentBucket {
			return true
		}
		name, value := rawValue.(*atomicDimensionBucket).snapshot()
		mergeDimensionAggregate(aggregates, key.entityId, name, key.bucketTs, value)
		return true
	})

	result.Items = make([]DimensionItem, 0, len(aggregates))
	for id, aggregate := range aggregates {
		value := aggregate.counters
		if value.requestCount <= 0 {
			continue
		}
		failureCount := value.requestCount - value.successCount
		if failureCount < 0 {
			failureCount = 0
		}
		name := aggregate.name
		if dimension == DimensionToken && id == 0 && name == "" {
			name = InternalTokenName
		}
		result.Items = append(result.Items, DimensionItem{
			Id:                 id,
			Name:               name,
			RequestCount:       value.requestCount,
			SuccessCount:       value.successCount,
			FailureCount:       failureCount,
			SuccessRate:        percent(value.successCount, value.requestCount),
			CacheEligibleCount: value.cacheEligibleCount,
			CacheHitCount:      value.cacheHitCount,
			CacheHitRate:       percent(value.cacheHitCount, value.cacheEligibleCount),
			InputTokens:        value.inputTokens,
			CachedTokens:       value.cachedTokens,
			CachedTokenRate:    percent(value.cachedTokens, value.inputTokens),
		})
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].RequestCount == result.Items[j].RequestCount {
			return result.Items[i].Id < result.Items[j].Id
		}
		return result.Items[i].RequestCount > result.Items[j].RequestCount
	})
	return result, nil
}

func normalizeDimensionHours(hours int) int {
	if hours <= 0 {
		return 24
	}
	if hours > maxDimensionHours {
		return maxDimensionHours
	}
	return hours
}

func mergeDimensionAggregate(aggregates map[int]dimensionAggregate, id int, name string, bucketTs int64, value dimensionCounters) {
	if value.requestCount == 0 {
		return
	}
	current := aggregates[id]
	if strings.TrimSpace(name) != "" && bucketTs >= current.latestTs {
		current.name = name
		current.latestTs = bucketTs
	}
	current.counters.requestCount += value.requestCount
	current.counters.successCount += value.successCount
	current.counters.cacheEligibleCount += value.cacheEligibleCount
	current.counters.cacheHitCount += value.cacheHitCount
	current.counters.inputTokens += value.inputTokens
	current.counters.cachedTokens += value.cachedTokens
	aggregates[id] = current
}

func percent(numerator int64, denominator int64) float64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator) * 100
	return math.Round(value*100) / 100
}

func recordDimensionRedis(key dimensionBucketKey, sample dimensionSample) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	redisKey := dimensionRedisBucketKey(key)
	registryKey := dimensionRedisRegistryKey(key.dimension, key.bucketTs)
	ttl := time.Duration(perf_metrics_setting.GetBucketSeconds()*2) * time.Second
	if ttl < 2*time.Hour {
		ttl = 2 * time.Hour
	}
	pipe := common.RDB.TxPipeline()
	pipe.SAdd(ctx, registryKey, key.entityId)
	pipe.Expire(ctx, registryKey, ttl)
	if name := strings.TrimSpace(sample.entityName); name != "" {
		pipe.HSet(ctx, redisKey, "name", name)
	}
	pipe.HIncrBy(ctx, redisKey, "req", 1)
	if sample.success {
		pipe.HIncrBy(ctx, redisKey, "ok", 1)
	}
	if sample.cacheEligible {
		pipe.HIncrBy(ctx, redisKey, "cache_n", 1)
		if sample.cachedTokens > 0 {
			pipe.HIncrBy(ctx, redisKey, "cache_hit", 1)
		}
		pipe.HIncrBy(ctx, redisKey, "input", sample.inputTokens)
		pipe.HIncrBy(ctx, redisKey, "cached", sample.cachedTokens)
	}
	pipe.Expire(ctx, redisKey, ttl)
	_, _ = pipe.Exec(ctx)
}

func mergeRedisDimensionActiveBucket(aggregates map[int]dimensionAggregate, dimension string, bucketTs int64) bool {
	if !common.RedisEnabled || common.RDB == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ids, err := common.RDB.SMembers(ctx, dimensionRedisRegistryKey(dimension, bucketTs)).Result()
	if err != nil || len(ids) == 0 {
		return false
	}
	pipe := common.RDB.Pipeline()
	commands := make(map[int]*redis.StringStringMapCmd, len(ids))
	for _, rawId := range ids {
		id, parseErr := strconv.Atoi(rawId)
		if parseErr != nil {
			continue
		}
		commands[id] = pipe.HGetAll(ctx, dimensionRedisBucketKey(dimensionBucketKey{
			dimension: dimension,
			entityId:  id,
			bucketTs:  bucketTs,
		}))
	}
	if _, err = pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false
	}
	for id, command := range commands {
		values, commandErr := command.Result()
		if commandErr != nil || len(values) == 0 {
			continue
		}
		mergeDimensionAggregate(aggregates, id, values["name"], bucketTs, dimensionCounters{
			requestCount:       parseRedisInt(values["req"]),
			successCount:       parseRedisInt(values["ok"]),
			cacheEligibleCount: parseRedisInt(values["cache_n"]),
			cacheHitCount:      parseRedisInt(values["cache_hit"]),
			inputTokens:        parseRedisInt(values["input"]),
			cachedTokens:       parseRedisInt(values["cached"]),
		})
	}
	return true
}

func dimensionRedisBucketKey(key dimensionBucketKey) string {
	return fmt.Sprintf("perf:dim:%s:%d:%d", key.dimension, key.entityId, key.bucketTs)
}

func dimensionRedisRegistryKey(dimension string, bucketTs int64) string {
	return fmt.Sprintf("perf:dim:keys:%s:%d", dimension, bucketTs)
}
