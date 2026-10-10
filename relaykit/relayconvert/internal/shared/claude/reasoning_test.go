package claude

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/convdiag"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyNativeThinking(t *testing.T, req *dto.ClaudeRequest) (*convmeta.Values, *convdiag.Collector) {
	t.Helper()
	info := &convmeta.Values{OriginModelName: req.Model, UpstreamModelName: req.Model}
	ctx, collector := convdiag.WithCollector(context.Background())
	require.NoError(t, ApplyReasoning(ctx, req, info, reasoning.Intent{}, false))
	return info, collector
}

func thinkingDiagnosticCodes(collector *convdiag.Collector) []string {
	diagnostics := collector.Diagnostics()
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

func TestApplyReasoningNativeDisabledOnOpus55UsesAdaptive(t *testing.T) {
	maxTokens := uint(4096)
	req := &dto.ClaudeRequest{
		Model:     "claude-opus-5-5",
		MaxTokens: &maxTokens,
		Thinking:  &dto.Thinking{Type: "disabled"},
	}

	info, collector := applyNativeThinking(t, req)

	require.NotNil(t, req.Thinking)
	assert.Equal(t, "adaptive", req.Thinking.Type)
	assert.Nil(t, req.Thinking.BudgetTokens)
	assert.JSONEq(t, `{"effort":"low"}`, string(req.OutputConfig))
	assert.Contains(t, thinkingDiagnosticCodes(collector), "claude_thinking_disable_unsupported")
	assert.Equal(t, "low", info.GetReasoningEffort())
}

func TestApplyReasoningNativeDisabledOnSonnet55UsesBetweenTools(t *testing.T) {
	maxTokens := uint(4096)
	req := &dto.ClaudeRequest{
		Model:     "claude-sonnet-5-5",
		MaxTokens: &maxTokens,
		Thinking:  &dto.Thinking{Type: "disabled"},
	}

	info, collector := applyNativeThinking(t, req)

	require.NotNil(t, req.Thinking)
	assert.Equal(t, "between_tools", req.Thinking.Type)
	assert.Nil(t, req.Thinking.BudgetTokens)
	assert.Nil(t, req.OutputConfig)
	assert.Contains(t, thinkingDiagnosticCodes(collector), "claude_thinking_disable_as_between_tools")
	assert.Equal(t, "none", info.GetReasoningEffort())
}

func TestApplyReasoningNativeManualThinkingOnOpus48PassesThrough(t *testing.T) {
	maxTokens := uint(20000)
	budget := 10000
	req := &dto.ClaudeRequest{
		Model:        "claude-opus-4-8",
		MaxTokens:    &maxTokens,
		Thinking:     &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		OutputConfig: []byte(`{"effort":"high"}`),
	}

	info, collector := applyNativeThinking(t, req)

	require.NotNil(t, req.Thinking)
	assert.Equal(t, "enabled", req.Thinking.Type)
	require.NotNil(t, req.Thinking.BudgetTokens)
	assert.Equal(t, budget, *req.Thinking.BudgetTokens)
	assert.Equal(t, `{"effort":"high"}`, string(req.OutputConfig))
	assert.Empty(t, thinkingDiagnosticCodes(collector))
	assert.Equal(t, "high", info.GetReasoningEffort())
}

func TestApplyReasoningNativeDisabledOnOpus5PassesThrough(t *testing.T) {
	maxTokens := uint(4096)
	req := &dto.ClaudeRequest{
		Model:     "claude-opus-5",
		MaxTokens: &maxTokens,
		Thinking:  &dto.Thinking{Type: "disabled"},
	}

	info, collector := applyNativeThinking(t, req)

	require.NotNil(t, req.Thinking)
	assert.Equal(t, "disabled", req.Thinking.Type)
	assert.Nil(t, req.OutputConfig)
	assert.Empty(t, thinkingDiagnosticCodes(collector))
	assert.Equal(t, "none", info.GetReasoningEffort())
}
