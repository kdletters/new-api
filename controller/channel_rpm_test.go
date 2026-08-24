package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChannelCarriesInitialChannelRPMSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("channel_id", 71)
	ctx.Set("channel_type", constant.ChannelTypeOpenAI)
	ctx.Set("channel_name", "rpm-channel")
	ctx.Set("auto_ban", true)
	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{RPM: 77})

	channel, newAPIError := getChannel(ctx, &relaycommon.RelayInfo{}, &service.RetryParam{})
	require.Nil(t, newAPIError)
	require.NotNil(t, channel)
	assert.Equal(t, 71, channel.Id)
	assert.Equal(t, 77, channel.GetRPM())
}

func TestPrepareChannelRetryExcludesFailedChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	retryParam := &service.RetryParam{}
	upstream429 := types.NewErrorWithStatusCode(
		errors.New("upstream rate limited"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusTooManyRequests,
	)

	retry := prepareChannelRetry(ctx, upstream429, 1, retryParam, 91)
	require.True(t, retry)
	_, deprioritized := retryParam.DeprioritizedChannelIDs[91]
	assert.True(t, deprioritized, "a retryable upstream failure must deprioritize the failed channel for this request")
}

func TestPrepareChannelRetryKeepsSpecificChannelPinned(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("specific_channel_id", "91")
	retryParam := &service.RetryParam{}
	channelError := types.NewError(errors.New("invalid channel key"), types.ErrorCodeChannelInvalidKey)

	retry := prepareChannelRetry(ctx, channelError, 1, retryParam, 91)
	assert.False(t, retry)
	assert.Empty(t, retryParam.DeprioritizedChannelIDs)
}

func TestGetChannelRetrySelectsUnusedChannel(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCacheEnabled })

	priority := int64(10)
	weight := uint(100)
	for _, channelID := range []int{801, 802} {
		channel := &model.Channel{
			Id:       channelID,
			Type:     constant.ChannelTypeOpenAI,
			Key:      "test-key",
			Status:   common.ChannelStatusEnabled,
			Name:     "retry-channel",
			Weight:   &weight,
			Models:   "retry-model",
			Group:    "default",
			Priority: &priority,
		}
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group:     "default",
			Model:     "retry-model",
			ChannelId: channelID,
			Enabled:   true,
			Priority:  &priority,
			Weight:    weight,
		}).Error)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: "retry-model",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	retryIndex := 1
	retryParam := &service.RetryParam{
		Ctx:                     ctx,
		TokenGroup:              "default",
		ModelName:               "retry-model",
		RequestPath:             "/v1/chat/completions",
		Retry:                   &retryIndex,
		DeprioritizedChannelIDs: map[int]struct{}{801: {}},
	}

	channel, newAPIError := getChannel(ctx, info, retryParam)
	require.Nil(t, newAPIError)
	require.NotNil(t, channel)
	assert.Equal(t, 802, channel.Id)
}
