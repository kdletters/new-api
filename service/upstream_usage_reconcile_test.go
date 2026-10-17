package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEstimatedClaudeUsage() *dto.Usage {
	billingUsage := dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
		InputTokens: 573708,
	})
	billingUsage.Estimated = true
	return &dto.Usage{
		PromptTokens:     573708,
		CompletionTokens: 0,
		TotalTokens:      573708,
		InputTokens:      573708,
		UsageSemantic:    dto.BillingUsageSemanticAnthropic,
		UsageSource:      dto.BillingUsageSourceClaudeMessages,
		BillingUsage:     billingUsage,
	}
}

func newClaudeReconcileContext(t *testing.T, baseURL string, upstreamRequestID string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(common.UpstreamRequestIdKey, upstreamRequestID)
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)

	streamStatus := relaycommon.NewStreamStatus()
	streamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
	info := &relaycommon.RelayInfo{
		IsStream:              true,
		ReceivedResponseCount: 0,
		StreamStatus:          streamStatus,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: baseURL,
			ApiKey:         "sk-upstream",
		},
	}
	return c, info
}

func TestReconcileEstimatedClaudeStreamUsageMatchesUpstreamTokenLog(t *testing.T) {
	originalDelays := upstreamUsageReconcileDelays
	upstreamUsageReconcileDelays = []time.Duration{0}
	t.Cleanup(func() {
		upstreamUsageReconcileDelays = originalDelays
	})

	const upstreamRequestID = "upstream-request-match"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/log/token", r.URL.Path)
		assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{
			"success": true,
			"message": "",
			"data": [
				{
					"type": 2,
					"request_id": "upstream-request-other",
					"prompt_tokens": 999999,
					"completion_tokens": 99,
					"other": "{\"cache_tokens\":999999}"
				},
				{
					"type": 2,
					"request_id": "upstream-request-match",
					"prompt_tokens": 121,
					"completion_tokens": 7,
					"other": "{\"cache_tokens\":544333}"
				}
			]
		}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	for _, receivedResponseCount := range []int{0, 2731} {
		t.Run(fmt.Sprintf("received_%d", receivedResponseCount), func(t *testing.T) {
			c, info := newClaudeReconcileContext(t, server.URL, upstreamRequestID)
			info.ReceivedResponseCount = receivedResponseCount
			estimatedUsage := newEstimatedClaudeUsage()

			reconciled := ReconcileEstimatedClaudeStreamUsage(c, info, estimatedUsage)

			require.NotNil(t, reconciled)
			require.NotNil(t, reconciled.BillingUsage)
			assert.Equal(t, dto.BillingUsageSemanticAnthropic, reconciled.BillingUsage.Semantic)
			assert.False(t, reconciled.BillingUsage.Estimated)
			require.NotNil(t, reconciled.BillingUsage.ClaudeUsage)
			assert.Equal(t, 121, reconciled.BillingUsage.ClaudeUsage.InputTokens)
			assert.Equal(t, 7, reconciled.BillingUsage.ClaudeUsage.OutputTokens)
			assert.Equal(t, 544333, reconciled.BillingUsage.ClaudeUsage.CacheReadInputTokens)
			assert.Equal(t, 121, reconciled.PromptTokens)
			assert.Equal(t, 7, reconciled.CompletionTokens)
			assert.Equal(t, 544333, reconciled.PromptTokensDetails.CachedTokens)
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
			assert.Equal(t, "matched", info.UpstreamUsageReconciliation)
		})
	}
}

func TestReconcileEstimatedClaudeStreamUsageKeepsUsageWhenUpstreamLogIsMissing(t *testing.T) {
	originalDelays := upstreamUsageReconcileDelays
	upstreamUsageReconcileDelays = []time.Duration{0}
	t.Cleanup(func() {
		upstreamUsageReconcileDelays = originalDelays
	})

	const upstreamRequestID = "upstream-request-missing"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/log/token", r.URL.Path)
		assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{
			"success": true,
			"message": "",
			"data": [
				{
					"type": 2,
					"request_id": "upstream-request-other",
					"prompt_tokens": 121,
					"completion_tokens": 7,
					"other": "{\"cache_tokens\":544333}"
				}
			]
		}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	c, info := newClaudeReconcileContext(t, server.URL, upstreamRequestID)
	estimatedUsage := newEstimatedClaudeUsage()

	reconciled := ReconcileEstimatedClaudeStreamUsage(c, info, estimatedUsage)

	require.NotNil(t, reconciled)
	assert.Equal(t, estimatedUsage, reconciled)
	require.NotNil(t, reconciled.BillingUsage)
	assert.True(t, reconciled.BillingUsage.Estimated)
	assert.Equal(t, "not_found", info.UpstreamUsageReconciliation)
}
