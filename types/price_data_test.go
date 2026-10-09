package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGroupRatioInfoApplyChannelRatio(t *testing.T) {
	info := GroupRatioInfo{GroupRatio: 0.8, BaseGroupRatio: 0.8, ChannelRatio: 1}

	info.ApplyChannelRatio(1.5)

	assert.InDelta(t, 1.2, info.GroupRatio, 1e-9, "effective ratio must be group ratio times channel ratio")
	assert.InDelta(t, 0.8, info.BaseGroupRatio, 1e-9)
	assert.InDelta(t, 1.5, info.ChannelRatio, 1e-9)
	assert.True(t, info.HasChannelRatio())
	assert.InDelta(t, 0.8, info.GroupOnlyRatio(), 1e-9, "group-only charges ignore the channel multiplier")
}

func TestGroupRatioInfoRecomputesFromGroupBaseOnChannelRetry(t *testing.T) {
	info := GroupRatioInfo{GroupRatio: 0.8, BaseGroupRatio: 0.8, ChannelRatio: 1}

	info.ApplyChannelRatio(2)
	info.ApplyChannelRatio(1.25)

	assert.InDelta(t, 1.0, info.GroupRatio, 1e-9, "a retry must reprice from the group base, not multiply cumulatively")
	assert.InDelta(t, 0.8, info.BaseGroupRatio, 1e-9)

	info.ApplyChannelRatio(1)

	assert.InDelta(t, 0.8, info.GroupRatio, 1e-9, "a channel without a multiplier restores the group-only price")
	assert.False(t, info.HasChannelRatio())
}

func TestGroupRatioInfoRejectsInvalidChannelRatio(t *testing.T) {
	info := GroupRatioInfo{GroupRatio: 0.5, BaseGroupRatio: 0.5, ChannelRatio: 1}

	info.ApplyChannelRatio(0)

	assert.InDelta(t, 0.5, info.GroupRatio, 1e-9, "invalid multipliers fall back to the neutral value")
	assert.InDelta(t, 1, info.ChannelRatio, 1e-9)
}
