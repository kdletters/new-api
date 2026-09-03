package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldDisableChannelRecognizesCodexUsageExhaustion(t *testing.T) {
	original := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = original })

	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{
			name: "usage limit error code",
			err:  types.NewOpenAIError(errors.New("The usage limit has been reached"), types.ErrorCode("usage_limit_reached"), http.StatusTooManyRequests),
			want: true,
		},
		{
			name: "ordinary rate limit remains retryable",
			err:  types.NewOpenAIError(errors.New("rate limit exceeded"), types.ErrorCode("rate_limit_exceeded"), http.StatusTooManyRequests),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ShouldDisableChannel(tt.err))
		})
	}

	assert.True(t, ShouldDisableChannel(types.NewOpenAIError(errors.New("You've hit your usage limit."), types.ErrorCode("unknown_error"), http.StatusOK)))
}
