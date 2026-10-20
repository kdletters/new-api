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

func estimatedUsageForSemantic(t *testing.T, semantic string) *dto.Usage {
	t.Helper()
	var billingUsage *dto.BillingUsage
	switch semantic {
	case dto.BillingUsageSemanticAnthropic:
		billingUsage = dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 573708})
	case dto.BillingUsageSemanticOpenAI:
		billingUsage = dto.NewOpenAIResponsesBillingUsage(&dto.Usage{PromptTokens: 573708, TotalTokens: 573708})
	case dto.BillingUsageSemanticGemini:
		billingUsage = dto.NewEstimatedGeminiChatBillingUsage(&dto.Usage{PromptTokens: 573708, TotalTokens: 573708})
	default:
		t.Fatalf("unsupported semantic %q", semantic)
	}
	require.NotNil(t, billingUsage)
	billingUsage.Estimated = true
	return &dto.Usage{
		PromptTokens:     573708,
		CompletionTokens: 0,
		TotalTokens:      573708,
		InputTokens:      573708,
		UsageSemantic:    semantic,
		UsageSource:      billingUsage.Source,
		BillingUsage:     billingUsage,
	}
}

func newReconcileContext(t *testing.T, baseURL string, upstreamRequestID string, receivedResponseCount int) (*gin.Context, *relaycommon.RelayInfo) {
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
		ReceivedResponseCount: receivedResponseCount,
		StreamStatus:          streamStatus,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: baseURL,
			ApiKey:         "sk-upstream",
		},
	}
	return c, info
}

func upstreamUsageLogServer(t *testing.T, upstreamRequestID string, promptTokens int, completionTokens int, other string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/log/token", r.URL.Path)
		assert.Equal(t, upstreamRequestID, r.URL.Query().Get("request_id"))
		assert.Equal(t, upstreamRequestID, r.URL.Query().Get("upstream_request_id"))
		assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(w, `{
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
					"request_id": %q,
					"prompt_tokens": %d,
					"completion_tokens": %d,
					"other": %q
				}
			]
		}`, upstreamRequestID, promptTokens, completionTokens, other)
		assert.NoError(t, err)
	}))
}

func TestReconcileEstimatedTextUsageMatchesUpstreamTokenLogBySemantic(t *testing.T) {
	originalDelays := upstreamUsageReconcileDelays
	upstreamUsageReconcileDelays = []time.Duration{0}
	t.Cleanup(func() {
		upstreamUsageReconcileDelays = originalDelays
	})

	tests := []struct {
		name              string
		semantic          string
		received          int
		promptTokens      int
		completionTokens  int
		cacheTokens       int
		upstreamOther     string
		expectedSource    string
		expectClaudeUsage bool
		expectOpenAIUsage bool
		expectGeminiUsage bool
	}{
		{
			name:              "anthropic zero frame",
			semantic:          dto.BillingUsageSemanticAnthropic,
			received:          0,
			promptTokens:      121,
			completionTokens:  7,
			cacheTokens:       544333,
			upstreamOther:     `{"cache_tokens":544333,"usage_semantic":"anthropic"}`,
			expectedSource:    dto.BillingUsageSourceClaudeMessages,
			expectClaudeUsage: true,
		},
		{
			name:              "anthropic partial stream",
			semantic:          dto.BillingUsageSemanticAnthropic,
			received:          2731,
			promptTokens:      121,
			completionTokens:  7,
			cacheTokens:       544333,
			upstreamOther:     `{"cache_tokens":544333,"usage_semantic":"anthropic"}`,
			expectedSource:    dto.BillingUsageSourceClaudeMessages,
			expectClaudeUsage: true,
		},
		{
			name:              "openai responses partial stream",
			semantic:          dto.BillingUsageSemanticOpenAI,
			received:          2731,
			promptTokens:      1000,
			completionTokens:  50,
			cacheTokens:       1234,
			upstreamOther:     `{"cache_tokens":1234,"usage_semantic":"openai"}`,
			expectedSource:    dto.BillingUsageSourceOAIResponses,
			expectOpenAIUsage: true,
		},
		{
			name:              "gemini zero frame",
			semantic:          dto.BillingUsageSemanticGemini,
			received:          0,
			promptTokens:      800,
			completionTokens:  40,
			cacheTokens:       222,
			upstreamOther:     `{"cache_tokens":222,"usage_semantic":"gemini"}`,
			expectedSource:    dto.BillingUsageSourceGeminiChat,
			expectGeminiUsage: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const upstreamRequestID = "upstream-request-match"
			server := upstreamUsageLogServer(t, upstreamRequestID, test.promptTokens, test.completionTokens, test.upstreamOther)
			defer server.Close()

			c, info := newReconcileContext(t, server.URL, upstreamRequestID, test.received)
			reconciled := ReconcileEstimatedTextUsage(c, info, estimatedUsageForSemantic(t, test.semantic))

			require.NotNil(t, reconciled)
			require.NotNil(t, reconciled.BillingUsage)
			assert.False(t, reconciled.BillingUsage.Estimated)
			assert.Equal(t, test.semantic, reconciled.UsageSemantic)
			assert.Equal(t, test.semantic, reconciled.BillingUsage.Semantic)
			assert.Equal(t, test.expectedSource, reconciled.UsageSource)
			assert.Equal(t, test.promptTokens, reconciled.PromptTokens)
			assert.Equal(t, test.completionTokens, reconciled.CompletionTokens)
			assert.Equal(t, test.cacheTokens, reconciled.PromptTokensDetails.CachedTokens)
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
			assert.Equal(t, "matched", info.UpstreamUsageReconciliation)

			if test.expectClaudeUsage {
				require.NotNil(t, reconciled.BillingUsage.ClaudeUsage)
				assert.Equal(t, test.promptTokens, reconciled.BillingUsage.ClaudeUsage.InputTokens)
				assert.Equal(t, test.completionTokens, reconciled.BillingUsage.ClaudeUsage.OutputTokens)
				assert.Equal(t, test.cacheTokens, reconciled.BillingUsage.ClaudeUsage.CacheReadInputTokens)
			}
			if test.expectOpenAIUsage {
				require.NotNil(t, reconciled.BillingUsage.OpenAIUsage)
				assert.Equal(t, test.promptTokens, reconciled.BillingUsage.OpenAIUsage.PromptTokens)
				assert.Equal(t, test.completionTokens, reconciled.BillingUsage.OpenAIUsage.CompletionTokens)
				assert.Equal(t, test.cacheTokens, reconciled.BillingUsage.OpenAIUsage.PromptTokensDetails.CachedTokens)
			}
			if test.expectGeminiUsage {
				require.NotNil(t, reconciled.BillingUsage.GeminiUsageMetadata)
				assert.Equal(t, test.promptTokens, reconciled.BillingUsage.GeminiUsageMetadata.PromptTokenCount)
				assert.Equal(t, test.completionTokens, reconciled.BillingUsage.GeminiUsageMetadata.CandidatesTokenCount)
				assert.Equal(t, test.cacheTokens, reconciled.BillingUsage.GeminiUsageMetadata.CachedContentTokenCount)
			}
		})
	}
}

func TestReconcileEstimatedTextUsageKeepsUsageWhenUpstreamLogIsMissing(t *testing.T) {
	originalDelays := upstreamUsageReconcileDelays
	upstreamUsageReconcileDelays = []time.Duration{0}
	t.Cleanup(func() {
		upstreamUsageReconcileDelays = originalDelays
	})

	const upstreamRequestID = "upstream-request-missing"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/log/token", r.URL.Path)
		assert.Equal(t, upstreamRequestID, r.URL.Query().Get("request_id"))
		assert.Equal(t, upstreamRequestID, r.URL.Query().Get("upstream_request_id"))
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

	c, info := newReconcileContext(t, server.URL, upstreamRequestID, 100)
	estimatedUsage := estimatedUsageForSemantic(t, dto.BillingUsageSemanticAnthropic)

	reconciled := ReconcileEstimatedTextUsage(c, info, estimatedUsage)

	require.NotNil(t, reconciled)
	assert.Equal(t, estimatedUsage, reconciled)
	require.NotNil(t, reconciled.BillingUsage)
	assert.True(t, reconciled.BillingUsage.Estimated)
	assert.Equal(t, "not_found", info.UpstreamUsageReconciliation)
}

func TestReconcileEstimatedTextUsageTreatsUpstream499AsNotBilled(t *testing.T) {
	originalDelays := upstreamUsageReconcileDelays
	upstreamUsageReconcileDelays = []time.Duration{0}
	t.Cleanup(func() {
		upstreamUsageReconcileDelays = originalDelays
	})

	const upstreamRequestID = "upstream-request-client-cancelled"
	tests := []struct {
		name    string
		content string
		other   string
	}{
		{name: "content status code", content: "status_code=499, client disconnected"},
		{name: "other status code", other: `{"status_code":499,"error_code":"client_cancelled"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/log/token", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprintf(w, `{
					"success": true,
					"message": "",
					"data": [{
						"id": 84,
						"type": 5,
						"request_id": %q,
						"content": %q,
						"prompt_tokens": 0,
						"completion_tokens": 0,
						"quota": 0,
						"is_stream": true,
						"other": %q
					}]
				}`, upstreamRequestID, test.content, test.other)
				assert.NoError(t, err)
			}))
			defer server.Close()

			c, info := newReconcileContext(t, server.URL, upstreamRequestID, 0)
			reconciled := ReconcileEstimatedTextUsage(c, info, estimatedUsageForSemantic(t, dto.BillingUsageSemanticAnthropic))

			require.NotNil(t, reconciled)
			assert.Nil(t, reconciled.BillingUsage)
			assert.Zero(t, reconciled.PromptTokens)
			assert.Zero(t, reconciled.CompletionTokens)
			assert.Zero(t, reconciled.TotalTokens)
			assert.Equal(t, dto.BillingUsageSemanticAnthropic, reconciled.UsageSemantic)
			assert.Equal(t, dto.BillingUsageSourceClaudeMessages, reconciled.UsageSource)
			assert.Equal(t, upstreamUsageReconciliationNotBilled, info.UpstreamUsageReconciliation)
			assert.Equal(t, int64(84), info.UpstreamUsageLogID)
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
		})
	}
}

func TestReconcileEstimatedTextUsagePrefersConsumeLogOver499(t *testing.T) {
	originalDelays := upstreamUsageReconcileDelays
	upstreamUsageReconcileDelays = []time.Duration{0}
	t.Cleanup(func() {
		upstreamUsageReconcileDelays = originalDelays
	})

	const upstreamRequestID = "upstream-request-preferred-consume"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(w, `{
			"success": true,
			"message": "",
			"data": [
				{"id": 84, "type": 5, "request_id": %q, "content": "status_code=499", "quota": 0},
				{"id": 85, "type": 2, "request_id": %q, "prompt_tokens": 121, "completion_tokens": 7, "quota": 5000, "other": "{\"cache_tokens\":0}"}
			]
		}`, upstreamRequestID, upstreamRequestID)
		assert.NoError(t, err)
	}))
	defer server.Close()

	c, info := newReconcileContext(t, server.URL, upstreamRequestID, 0)
	reconciled := ReconcileEstimatedTextUsage(c, info, estimatedUsageForSemantic(t, dto.BillingUsageSemanticAnthropic))

	require.NotNil(t, reconciled)
	require.NotNil(t, reconciled.BillingUsage)
	assert.False(t, reconciled.BillingUsage.Estimated)
	assert.Equal(t, 121, reconciled.PromptTokens)
	assert.Equal(t, 7, reconciled.CompletionTokens)
	assert.Equal(t, "matched", info.UpstreamUsageReconciliation)
}
