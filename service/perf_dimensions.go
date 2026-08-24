package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

const contextKeyPerfDimensionUsage = "perf_dimension_usage"

func recordSuccessfulRelayDimensions(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage perfmetrics.DimensionUsage) {
	if ctx == nil || relayInfo == nil {
		return
	}
	infoCopy := *relayInfo
	channelName := ctx.GetString("channel_name")
	ctx.Set(contextKeyPerfDimensionUsage, usage)
	channelSuccess := IsFinalRelayChannelAttemptSuccessful(relayInfo)
	gopool.Go(func() {
		if channelSuccess {
			perfmetrics.RecordChannelAttemptSuccess(&infoCopy, channelName, usage)
			return
		}
		perfmetrics.RecordChannelAttemptFailure(infoCopy.ChannelId, channelName)
	})
}

func IsFinalRelayChannelAttemptSuccessful(relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil || !relayInfo.IsStream || relayInfo.StreamStatus == nil {
		return true
	}
	if relayInfo.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		return true
	}
	return relayInfo.StreamStatus.IsNormalEnd() && !relayInfo.StreamStatus.HasErrors()
}

func IsFinalRelayRequestSuccessful(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) bool {
	if ctx == nil {
		return false
	}
	if ctx.Request != nil && ctx.Request.Context().Err() != nil {
		return false
	}
	if relayInfo == nil || !relayInfo.IsStream || relayInfo.StreamStatus == nil {
		return true
	}
	if relayInfo.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		return false
	}
	return IsFinalRelayChannelAttemptSuccessful(relayInfo)
}

func RecordFinalRelayDimensionOutcome(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, success bool) {
	if ctx == nil {
		return
	}
	userId := ctx.GetInt("id")
	tokenId := ctx.GetInt("token_id")
	if relayInfo != nil {
		userId = relayInfo.UserId
		tokenId = relayInfo.TokenId
	}
	if userId <= 0 {
		return
	}
	userName := ctx.GetString("username")
	tokenName := ctx.GetString("token_name")
	usage, _ := ctx.Get(contextKeyPerfDimensionUsage)
	dimensionUsage, _ := usage.(perfmetrics.DimensionUsage)
	gopool.Go(func() {
		if success {
			perfmetrics.RecordFinalRequestSuccess(userId, userName, tokenId, tokenName, dimensionUsage)
			return
		}
		perfmetrics.RecordFinalRequestFailure(userId, userName, tokenId, tokenName)
	})
}

func textDimensionUsage(ctx *gin.Context, originalUsage *dto.Usage, effectiveUsage *dto.Usage, isAnthropic bool) perfmetrics.DimensionUsage {
	if ctx == nil || originalUsage == nil || effectiveUsage == nil ||
		commonUsageIsEstimated(originalUsage) ||
		common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens) {
		return perfmetrics.DimensionUsage{}
	}

	inputTokens := effectiveUsage.InputTokens
	if inputTokens <= 0 {
		inputTokens = effectiveUsage.PromptTokens
		if isAnthropic {
			inputTokens += effectiveUsage.PromptTokensDetails.CachedTokens
			inputTokens += effectiveUsage.PromptTokensDetails.CacheCreationTokensTotal()
		}
	}
	if inputTokens <= 0 {
		return perfmetrics.DimensionUsage{}
	}
	return perfmetrics.DimensionUsage{
		CacheEligible: true,
		InputTokens:   int64(inputTokens),
		CachedTokens:  int64(effectiveUsage.PromptTokensDetails.CachedTokens),
	}
}

func commonUsageIsEstimated(usage *dto.Usage) bool {
	return usage != nil && usage.BillingUsage != nil && usage.BillingUsage.Estimated
}
