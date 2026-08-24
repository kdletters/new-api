package perfmetrics

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDimensionMetricsTest(t *testing.T) {
	t.Helper()
	originalDB := model.DB
	originalRedisEnabled := common.RedisEnabled
	originalRDB := common.RDB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PerfDimensionMetric{}))
	model.DB = db
	common.RedisEnabled = false
	common.RDB = nil
	dimensionHotBuckets = sync.Map{}

	t.Cleanup(func() {
		dimensionHotBuckets = sync.Map{}
		model.DB = originalDB
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRDB
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
}

func findDimensionItem(t *testing.T, result DimensionQueryResult, id int) DimensionItem {
	t.Helper()
	for _, item := range result.Items {
		if item.Id == id {
			return item
		}
	}
	t.Fatalf("dimension item %d not found", id)
	return DimensionItem{}
}

func TestDimensionMetricsUseAttemptAndFinalRequestSemantics(t *testing.T) {
	setupDimensionMetricsTest(t)
	info := &relaycommon.RelayInfo{
		UserId:  21,
		TokenId: 31,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 11,
		},
	}

	RecordRelayDimensionSuccess(info, "channel-a", "alice", "key-label", DimensionUsage{
		CacheEligible: true,
		InputTokens:   100,
		CachedTokens:  25,
	})
	RecordChannelAttemptFailure(11, "channel-a")
	RecordFinalRequestFailure(21, "alice", 31, "key-label")

	channelResult, err := QueryDimensions(DimensionChannel, 24)
	require.NoError(t, err)
	channel := findDimensionItem(t, channelResult, 11)
	assert.EqualValues(t, 2, channel.RequestCount)
	assert.EqualValues(t, 1, channel.SuccessCount)
	assert.Equal(t, 50.0, channel.SuccessRate)
	assert.Equal(t, 100.0, channel.CacheHitRate)
	assert.Equal(t, 25.0, channel.CachedTokenRate)

	userResult, err := QueryDimensions(DimensionUser, 24)
	require.NoError(t, err)
	user := findDimensionItem(t, userResult, 21)
	assert.EqualValues(t, 2, user.RequestCount)
	assert.EqualValues(t, 1, user.SuccessCount)
	assert.Equal(t, 50.0, user.SuccessRate)

	tokenResult, err := QueryDimensions(DimensionToken, 24)
	require.NoError(t, err)
	token := findDimensionItem(t, tokenResult, 31)
	assert.Equal(t, "key-label", token.Name)
	assert.EqualValues(t, 2, token.RequestCount)
	assert.EqualValues(t, 1, token.SuccessCount)
}

func TestDimensionMetricsKeepInternalTokenWithoutRawKey(t *testing.T) {
	setupDimensionMetricsTest(t)
	info := &relaycommon.RelayInfo{
		UserId:  22,
		TokenId: 0,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 12,
		},
	}

	RecordRelayDimensionSuccess(info, "channel-b", "bob", "", DimensionUsage{})

	result, err := QueryDimensions(DimensionToken, 24)
	require.NoError(t, err)
	token := findDimensionItem(t, result, 0)
	assert.Equal(t, InternalTokenName, token.Name)
}
