package aws

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A per-message effort directive is a system message with empty content plus
// output_config.effort. If formatRequest drops output_config, Bedrock rejects
// the leftover empty system message with "system content must contain at least
// one block".
func TestFormatRequestPreservesPerMessageOutputConfig(t *testing.T) {
	const body = `{"model":"anthropic.claude-opus-5-5-v1:0","max_tokens":4096,"messages":[{"role":"user","content":"Plan a migration"},{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":"Summarize it"}]}`

	request, err := formatRequest(strings.NewReader(body), http.Header{})
	require.NoError(t, err)
	require.NotNil(t, request)

	marshaled, err := common.Marshal(request)
	require.NoError(t, err)

	assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(marshaled, "anthropic_version").String())
	// The client model selects the Bedrock model (InvokeModel ModelId) instead
	// of riding along in the body; the pass-through branch strips it as well.
	assert.False(t, gjson.GetBytes(marshaled, "model").Exists())
	require.Len(t, gjson.GetBytes(marshaled, "messages").Array(), 3)

	assert.Equal(t, "user", gjson.GetBytes(marshaled, "messages.0.role").String())
	assert.Equal(t, "Plan a migration", gjson.GetBytes(marshaled, "messages.0.content").String())
	assert.Equal(t, "user", gjson.GetBytes(marshaled, "messages.2.role").String())
	assert.Equal(t, "Summarize it", gjson.GetBytes(marshaled, "messages.2.content").String())

	systemMessage := gjson.GetBytes(marshaled, "messages.1")
	assert.Equal(t, "system", systemMessage.Get("role").String())
	// Content must stay an empty array rather than turning into null or
	// disappearing, or the effort-only system message becomes invalid.
	assert.True(t, systemMessage.Get("content").IsArray())
	assert.Equal(t, "[]", systemMessage.Get("content").Raw)
	assert.Equal(t, "high", systemMessage.Get("output_config.effort").String())
}
