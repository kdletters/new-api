package perfmetrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDimensionBackfillTest(t *testing.T, migrateLogs bool) (*gorm.DB, *gorm.DB) {
	t.Helper()
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	common.OptionMapRWMutex.Lock()
	originalOptionMapWasNil := common.OptionMap == nil
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	originalVersion, hadOriginalVersion := common.OptionMap[dimensionBackfillVersionKey]
	delete(common.OptionMap, dimensionBackfillVersionKey)
	common.OptionMapRWMutex.Unlock()
	mainDB, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_main?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	logDB, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_log?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, mainDB.AutoMigrate(&model.PerfDimensionMetric{}, &model.Option{}))
	if migrateLogs {
		require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	}
	model.DB = mainDB
	model.LOG_DB = logDB
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.OptionMapRWMutex.Lock()
		if originalOptionMapWasNil {
			common.OptionMap = nil
		} else if hadOriginalVersion {
			common.OptionMap[dimensionBackfillVersionKey] = originalVersion
		} else {
			delete(common.OptionMap, dimensionBackfillVersionKey)
		}
		common.OptionMapRWMutex.Unlock()
		if sqlDB, dbErr := mainDB.DB(); dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
		if sqlDB, dbErr := logDB.DB(); dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return mainDB, logDB
}

func dimensionBackfillOther(t *testing.T, status string, endReason string, cacheTokens int64) string {
	t.Helper()
	value, err := common.Marshal(map[string]any{
		"cache_tokens": cacheTokens,
		"stream_status": map[string]any{
			"status":     status,
			"end_reason": endReason,
		},
	})
	require.NoError(t, err)
	return string(value)
}

func loadDimensionBackfillRows(t *testing.T, db *gorm.DB) map[dimensionBucketKey]model.PerfDimensionMetric {
	t.Helper()
	var rows []model.PerfDimensionMetric
	require.NoError(t, db.Order("bucket_ts, dimension, entity_id").Find(&rows).Error)
	result := make(map[dimensionBucketKey]model.PerfDimensionMetric, len(rows))
	for _, row := range rows {
		row.Id = 0
		result[dimensionBucketKey{dimension: row.Dimension, entityId: row.EntityId, bucketTs: row.BucketTs}] = row
	}
	return result
}

func TestBackfillCompletedDimensionMetricsReconstructsOutcomesAndIsIdempotent(t *testing.T) {
	mainDB, logDB := setupDimensionBackfillTest(t, true)
	now := time.Unix(13*3600+15*60, 0)
	completedBucket := bucketStart(now.Unix()) - 3600
	currentBucket := bucketStart(now.Unix())

	require.NoError(t, mainDB.Create(&model.PerfDimensionMetric{
		Dimension: DimensionUser, EntityId: 10, EntityName: "stale", BucketTs: completedBucket,
		RequestCount: 99, SuccessCount: 99,
	}).Error)
	require.NoError(t, mainDB.Create(&model.PerfDimensionMetric{
		Dimension: DimensionUser, EntityId: 10, EntityName: "current", BucketTs: currentBucket,
		RequestCount: 7, SuccessCount: 7,
	}).Error)

	baseLog := model.Log{UserId: 10, Username: "alice", TokenId: 20, TokenName: "key-a", CreatedAt: completedBucket + 60}
	logs := []model.Log{
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: completedBucket + 60, Type: model.LogTypeError, ChannelId: 1, RequestId: "retry-success", Other: `{}`},
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: completedBucket + 61, Type: model.LogTypeConsume, ChannelId: 2, RequestId: "retry-success", PromptTokens: 100, Other: dimensionBackfillOther(t, "ok", "eof", 40)},
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: completedBucket + 120, Type: model.LogTypeError, ChannelId: 1, RequestId: "error-only", Other: `{}`},
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: completedBucket + 121, Type: model.LogTypeError, ChannelId: 1, RequestId: "error-only", Other: `{}`},
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: completedBucket + 180, Type: model.LogTypeConsume, ChannelId: 3, RequestId: "scanner-error", PromptTokens: 80, Other: dimensionBackfillOther(t, "error", "scanner_error", 50)},
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: completedBucket + 240, Type: model.LogTypeConsume, ChannelId: 4, RequestId: "client-gone", PromptTokens: 70, Other: dimensionBackfillOther(t, "error", "client_gone", 60)},
		{UserId: baseLog.UserId, Username: baseLog.Username, TokenId: baseLog.TokenId, TokenName: baseLog.TokenName, CreatedAt: currentBucket + 60, Type: model.LogTypeConsume, ChannelId: 5, RequestId: "current", PromptTokens: 50, Other: dimensionBackfillOther(t, "ok", "eof", 20)},
	}
	require.NoError(t, logDB.Create(&logs).Error)

	require.NoError(t, backfillCompletedDimensionMetrics(24, now))
	first := loadDimensionBackfillRows(t, mainDB)

	user := first[dimensionBucketKey{dimension: DimensionUser, entityId: 10, bucketTs: completedBucket}]
	assert.EqualValues(t, 4, user.RequestCount)
	assert.EqualValues(t, 1, user.SuccessCount)
	assert.EqualValues(t, 1, user.CacheEligibleCount)
	assert.EqualValues(t, 1, user.CacheHitCount)
	assert.EqualValues(t, 100, user.InputTokens)
	assert.EqualValues(t, 40, user.CachedTokens)

	token := first[dimensionBucketKey{dimension: DimensionToken, entityId: 20, bucketTs: completedBucket}]
	assert.EqualValues(t, user.RequestCount, token.RequestCount)
	assert.EqualValues(t, user.SuccessCount, token.SuccessCount)
	assert.EqualValues(t, user.CachedTokens, token.CachedTokens)

	channelOne := first[dimensionBucketKey{dimension: DimensionChannel, entityId: 1, bucketTs: completedBucket}]
	assert.EqualValues(t, 3, channelOne.RequestCount)
	assert.Zero(t, channelOne.SuccessCount)
	channelTwo := first[dimensionBucketKey{dimension: DimensionChannel, entityId: 2, bucketTs: completedBucket}]
	assert.EqualValues(t, 1, channelTwo.RequestCount)
	assert.EqualValues(t, 1, channelTwo.SuccessCount)
	assert.EqualValues(t, 100, channelTwo.InputTokens)
	assert.EqualValues(t, 40, channelTwo.CachedTokens)
	channelScanner := first[dimensionBucketKey{dimension: DimensionChannel, entityId: 3, bucketTs: completedBucket}]
	assert.EqualValues(t, 1, channelScanner.RequestCount)
	assert.Zero(t, channelScanner.SuccessCount)
	assert.Zero(t, channelScanner.CacheEligibleCount)
	channelClientGone := first[dimensionBucketKey{dimension: DimensionChannel, entityId: 4, bucketTs: completedBucket}]
	assert.EqualValues(t, 1, channelClientGone.RequestCount)
	assert.EqualValues(t, 1, channelClientGone.SuccessCount)
	assert.Zero(t, channelClientGone.CacheEligibleCount)

	current := first[dimensionBucketKey{dimension: DimensionUser, entityId: 10, bucketTs: currentBucket}]
	assert.EqualValues(t, 7, current.RequestCount)
	assert.EqualValues(t, 7, current.SuccessCount)
	_, currentChannelWritten := first[dimensionBucketKey{dimension: DimensionChannel, entityId: 5, bucketTs: currentBucket}]
	assert.False(t, currentChannelWritten)

	require.NoError(t, backfillCompletedDimensionMetrics(24, now))
	second := loadDimensionBackfillRows(t, mainDB)
	assert.Equal(t, first, second)
}

func TestBackfillCompletedDimensionMetricsKeepsExistingDataWhenLogQueryFails(t *testing.T) {
	mainDB, _ := setupDimensionBackfillTest(t, false)
	now := time.Unix(13*3600+15*60, 0)
	completedBucket := bucketStart(now.Unix()) - 3600
	require.NoError(t, mainDB.Create(&model.PerfDimensionMetric{
		Dimension: DimensionUser, EntityId: 10, BucketTs: completedBucket,
		RequestCount: 5, SuccessCount: 4,
	}).Error)

	err := runDimensionMetricsBackfill(24, now)
	require.Error(t, err)
	rows := loadDimensionBackfillRows(t, mainDB)
	metric := rows[dimensionBucketKey{dimension: DimensionUser, entityId: 10, bucketTs: completedBucket}]
	assert.EqualValues(t, 5, metric.RequestCount)
	assert.EqualValues(t, 4, metric.SuccessCount)
	common.OptionMapRWMutex.RLock()
	assert.NotEqual(t, dimensionBackfillVersion, common.OptionMap[dimensionBackfillVersionKey])
	common.OptionMapRWMutex.RUnlock()

	model.LOG_DB = nil
	err = runDimensionMetricsBackfill(24, now)
	require.Error(t, err)
	rows = loadDimensionBackfillRows(t, mainDB)
	metric = rows[dimensionBucketKey{dimension: DimensionUser, entityId: 10, bucketTs: completedBucket}]
	assert.EqualValues(t, 5, metric.RequestCount)
	assert.EqualValues(t, 4, metric.SuccessCount)
}

func TestRunDimensionMetricsBackfillPersistsVersionAndSkipsLaterRuns(t *testing.T) {
	mainDB, logDB := setupDimensionBackfillTest(t, true)
	now := time.Unix(13*3600+15*60, 0)
	completedBucket := bucketStart(now.Unix()) - 3600
	require.NoError(t, logDB.Create(&model.Log{
		UserId: 10, Username: "alice", TokenId: 20, TokenName: "key-a",
		CreatedAt: completedBucket + 60, Type: model.LogTypeConsume, ChannelId: 2,
		RequestId: "initial", PromptTokens: 100, Other: dimensionBackfillOther(t, "ok", "eof", 40),
	}).Error)

	require.NoError(t, runDimensionMetricsBackfill(24, now))
	var option model.Option
	require.NoError(t, mainDB.Where("key = ?", dimensionBackfillVersionKey).First(&option).Error)
	assert.Equal(t, dimensionBackfillVersion, option.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, dimensionBackfillVersion, common.OptionMap[dimensionBackfillVersionKey])
	common.OptionMapRWMutex.RUnlock()

	require.NoError(t, mainDB.Model(&model.PerfDimensionMetric{}).
		Where("dimension = ? AND entity_id = ? AND bucket_ts = ?", DimensionUser, 10, completedBucket).
		Updates(map[string]any{"request_count": 9, "success_count": 8}).Error)
	require.NoError(t, runDimensionMetricsBackfill(24, now))
	rows := loadDimensionBackfillRows(t, mainDB)
	user := rows[dimensionBucketKey{dimension: DimensionUser, entityId: 10, bucketTs: completedBucket}]
	assert.EqualValues(t, 9, user.RequestCount)
	assert.EqualValues(t, 8, user.SuccessCount)
}
