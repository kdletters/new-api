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

func recordSuccessfulRelayDimensions(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage perfmetrics.DimensionUsage) {
	if ctx == nil || relayInfo == nil {
		return
	}
	infoCopy := *relayInfo
	channelName := ctx.GetString("channel_name")
	userName := ctx.GetString("username")
	tokenName := ctx.GetString("token_name")
	gopool.Go(func() {
		perfmetrics.RecordRelayDimensionSuccess(&infoCopy, channelName, userName, tokenName, usage)
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
