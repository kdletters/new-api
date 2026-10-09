package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Per-message effort (beta) is a directive-only system message: empty content
// plus output_config.effort. If the field is dropped while new-api re-marshals
// the request, upstream rejects the leftover empty system message with
// "messages.N: system content must contain at least one block".
func TestConvertClaudeRequestPreservesPerMessageEffort(t *testing.T) {
	const body = `{"model":"claude-opus-5-5","max_tokens":4096,"messages":[{"role":"user","content":"Plan a migration"},{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":"Summarize it"}]}`
	var request dto.ClaudeRequest
	require.NoError(t, common.Unmarshal([]byte(body), &request))

	copied, err := common.DeepCopy(&request)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}
	converted, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, copied)
	require.NoError(t, err)
	outbound, err := common.Marshal(converted)
	require.NoError(t, err)

	var parsed struct {
		Messages []struct {
			Role         string         `json:"role"`
			Content      any            `json:"content"`
			OutputConfig map[string]any `json:"output_config"`
		} `json:"messages"`
	}
	require.NoError(t, common.Unmarshal(outbound, &parsed))
	require.Len(t, parsed.Messages, 3)
	assert.Equal(t, "system", parsed.Messages[1].Role)
	assert.Equal(t, []any{}, parsed.Messages[1].Content)
	assert.Equal(t, "high", parsed.Messages[1].OutputConfig["effort"])
}
