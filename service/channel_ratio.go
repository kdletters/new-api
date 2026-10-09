package service

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ChannelRatioFromContext returns the price multiplier of the channel recorded
// in the request context. It yields 1 while no channel has been selected, so
// callers can keep pricing requests before channel selection.
func ChannelRatioFromContext(c *gin.Context) float64 {
	if c == nil {
		return 1
	}
	settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if !ok {
		return 1
	}
	return settings.EffectiveChannelRatio()
}

// ChannelRatioOf returns the price multiplier configured on a channel.
func ChannelRatioOf(channel *model.Channel) float64 {
	if channel == nil {
		return 1
	}
	return channel.GetSetting().EffectiveChannelRatio()
}

// ApplyChannelRatio prices a request for the selected channel: the channel's
// multiplier is folded into the effective group ratio, the billable amounts are
// scaled once, and an existing reservation is raised when the channel costs more
// than the initial group-only estimate. It is safe to call once per channel
// attempt because the effective ratio is always recomputed from the group-only
// base, so retries that switch channels never multiply cumulatively.
func ApplyChannelRatio(c *gin.Context, relayInfo *relaycommon.RelayInfo, ratio float64) *types.NewAPIError {
	if relayInfo == nil {
		return nil
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
		common.SysError("invalid channel ratio, falling back to 1")
		ratio = 1
	}

	previous := relayInfo.PriceData.GroupRatioInfo.ChannelRatio
	if previous <= 0 {
		previous = 1
	}
	if ratio == 1 && previous == 1 {
		// No channel multiplier applies, so pricing and reservations stay exactly
		// as the group-only path left them.
		return nil
	}
	relayInfo.PriceData.GroupRatioInfo.ApplyChannelRatio(ratio)
	if _, err := RefreshTieredBillingSnapshot(relayInfo); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeModelPriceError, 400, types.ErrOptionWithSkipRetry())
	}

	delta := ratio / previous
	if delta != 1 {
		scale := func(quota int) int {
			scaled, clamp := common.QuotaFromFloatChecked(float64(quota) * delta)
			noteQuotaClamp(relayInfo, clamp)
			return scaled
		}
		relayInfo.PriceData.QuotaToPreConsume = scale(relayInfo.PriceData.QuotaToPreConsume)
		relayInfo.PriceData.Quota = scale(relayInfo.PriceData.Quota)
	}

	if relayInfo.Billing == nil {
		return nil
	}
	target := relayInfo.PriceData.QuotaToPreConsume
	if target <= 0 {
		target = relayInfo.PriceData.Quota
	}
	if target <= 0 {
		return nil
	}
	if err := relayInfo.Billing.Reserve(target); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	relayInfo.FinalPreConsumedQuota = relayInfo.Billing.GetPreConsumedQuota()
	return nil
}
