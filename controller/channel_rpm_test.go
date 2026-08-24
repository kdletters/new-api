package controller

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
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

func TestRelayRetriesDeprioritizedChannelAfterUpstream429(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}))

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	common.MemoryCacheEnabled = false
	common.RetryTimes = 1
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
	})

	var firstAttempts atomic.Int32
	var secondAttempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/first/v1/chat/completions":
			firstAttempts.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`))
		case "/second/v1/chat/completions":
			secondAttempts.Add(1)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-retry","object":"chat.completion","created":1,"model":"gpt-3.5-turbo","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	user := &model.User{
		Id:       901,
		Username: "retry-user",
		Password: "password",
		Quota:    common.GetTrustQuota() + int(common.QuotaPerUnit),
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	token := &model.Token{
		Id:             901,
		UserId:         user.Id,
		Key:            "retry-token",
		Status:         common.TokenStatusEnabled,
		Name:           "retry-token",
		UnlimitedQuota: true,
		Group:          "default",
	}
	require.NoError(t, db.Create(token).Error)

	priority := int64(10)
	weight := uint(100)
	firstBaseURL := upstream.URL + "/first"
	secondBaseURL := upstream.URL + "/second"
	channels := []*model.Channel{
		{Id: 901, Type: constant.ChannelTypeOpenAI, Key: "first-key", Status: common.ChannelStatusEnabled, Name: "first-channel", Weight: &weight, Models: "gpt-3.5-turbo", Group: "default", Priority: &priority, BaseURL: &firstBaseURL},
		{Id: 902, Type: constant.ChannelTypeOpenAI, Key: "second-key", Status: common.ChannelStatusEnabled, Name: "second-channel", Weight: &weight, Models: "gpt-3.5-turbo", Group: "default", Priority: &priority, BaseURL: &secondBaseURL},
	}
	for _, channel := range channels {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group:     "default",
			Model:     "gpt-3.5-turbo",
			ChannelId: channel.Id,
			Enabled:   true,
			Priority:  &priority,
			Weight:    weight,
		}).Error)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hello"}]}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now())
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-3.5-turbo")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserId, user.Id)
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
	common.SetContextKey(ctx, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(ctx, constant.ContextKeyTokenKey, token.Key)
	common.SetContextKey(ctx, constant.ContextKeyTokenUnlimited, true)
	ctx.Set("username", user.Username)
	ctx.Set("token_name", token.Name)
	require.Nil(t, middleware.SetupContextForSelectedChannel(ctx, channels[0], "gpt-3.5-turbo"))

	Relay(ctx, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, int32(1), firstAttempts.Load())
	assert.Equal(t, int32(1), secondAttempts.Load(), "the replacement channel must receive the retry")
	assert.Equal(t, []string{"901", "902"}, ctx.GetStringSlice("use_channel"))
}
