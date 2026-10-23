package relayconvert

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rawToolSchema(t *testing.T, schema map[string]any) json.RawMessage {
	t.Helper()
	encoded, err := kitutil.Marshal(schema)
	require.NoError(t, err)
	return encoded
}

func TestConvertRequestDefaultPolicyAllowsGeminiCodeExecution(t *testing.T) {
	t.Parallel()

	tools, err := kitutil.Marshal([]map[string]any{{"codeExecution": map[string]any{}}})
	require.NoError(t, err)
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "run this"}}},
		},
		Tools: tools,
	}

	result, err := ConvertRequest(nil, nil, types.RelayFormatOpenAI, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.IsType(t, &dto.GeneralOpenAIRequest{}, result.Value)
	assert.True(t, hasConversionDiagnosticCode(result.Diagnostics, "unsupported_hosted_tool"))
}

func TestConvertResponseStrictPolicyStillSucceedsOnContinuationLoss(t *testing.T) {
	t.Parallel()

	text := "hello"
	resp := &dto.ClaudeResponse{
		Id:         "msg_1",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-test",
		StopReason: "pause_turn",
		Content: []dto.ClaudeMediaMessage{
			{Type: "redacted_thinking", Data: "secret"},
			{Type: "text", Text: &text},
		},
	}
	info := &convmeta.Values{
		Options: &convmeta.Options{ToolLossPolicy: types.ConversionLossPolicyStrict},
	}

	result, err := ConvertResponse(nil, info, types.RelayFormatOpenAI, resp)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.IsType(t, &dto.OpenAITextResponse{}, result.Value)
	assert.True(t, hasConversionDiagnosticCode(result.Diagnostics, "continuation_state_lost"))
}

func TestConvertRequestSafePolicyReturnsConversionLossError(t *testing.T) {
	t.Parallel()

	tools, err := kitutil.Marshal([]map[string]any{{"codeExecution": map[string]any{}}})
	require.NoError(t, err)
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "run this"}}},
		},
		Tools: tools,
	}
	info := &convmeta.Values{
		Options: &convmeta.Options{ToolLossPolicy: types.ConversionLossPolicySafe},
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, req)
	require.Error(t, err)
	var loss *types.ConversionLossError
	require.ErrorAs(t, err, &loss)
	require.NotEmpty(t, loss.Diagnostics)
	require.NotNil(t, result)
	assert.True(t, hasConversionDiagnosticCode(loss.Diagnostics, "unsupported_hosted_tool"))
	assert.True(t, hasConversionDiagnosticCode(result.Diagnostics, "unsupported_hosted_tool"))
}

func hasConversionDiagnosticCode(diagnostics []types.ConversionDiagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func TestNormalizeClaudeToolInputSchemas(t *testing.T) {
	t.Parallel()

	unionSchema := func() map[string]any {
		return map[string]any{
			"type": "object",
			"anyOf": []any{
				map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}},
			},
		}
	}
	flatSchema := func() map[string]any {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{"a": map[string]any{"type": "string"}},
		}
	}

	tests := []struct {
		name     string
		tools    any
		wantName string
	}{
		{
			name:     "converted pointer tool",
			tools:    []any{&dto.Tool{Name: "converted", InputSchema: rawToolSchema(t, unionSchema())}},
			wantName: "converted",
		},
		{
			name:     "converted value tool",
			tools:    []any{dto.Tool{Name: "value", InputSchema: rawToolSchema(t, unionSchema())}},
			wantName: "value",
		},
		{
			name:     "typed pointer tool slice",
			tools:    []*dto.Tool{{Name: "typed", InputSchema: rawToolSchema(t, unionSchema())}},
			wantName: "typed",
		},
		{
			name:     "value tool slice",
			tools:    []dto.Tool{{Name: "value-slice", InputSchema: rawToolSchema(t, unionSchema())}},
			wantName: "value-slice",
		},
		{
			name:     "map-shaped native tool",
			tools:    []any{map[string]any{"name": "native", "input_schema": unionSchema()}},
			wantName: "native",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &dto.ClaudeRequest{Tools: tt.tools}
			diagnostics := NormalizeClaudeToolInputSchemas(req)
			require.Len(t, diagnostics, 1)
			assert.Equal(t, "claude_tool_schema_union_flattened", diagnostics[0].Code)
			assert.Equal(t, "tools[0].input_schema", diagnostics[0].Path)
			assert.Equal(t, types.ConversionDiagnosticWarning, diagnostics[0].Severity)
			assert.Contains(t, diagnostics[0].Message, tt.wantName)
			assert.Contains(t, diagnostics[0].Message, "flattened because Claude rejects top-level unions")
			assert.Equal(t, flatSchema(), toolSchemaByName(t, req.Tools, tt.wantName))
		})
	}
}

func TestNormalizeClaudeToolInputSchemasSkipsUnchangedTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tools any
	}{
		{
			name: "web search tools",
			tools: []any{
				map[string]any{
					"type":         "web_search_20250305",
					"name":         "web_search",
					"input_schema": map[string]any{"type": "object", "anyOf": []any{}},
				},
				&dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search"},
			},
		},
		{
			name:  "tool without an input schema",
			tools: []any{map[string]any{"name": "plain"}},
		},
		{
			name: "non-object input schema",
			tools: []any{
				map[string]any{
					"name":         "scalar",
					"input_schema": map[string]any{"type": "string", "anyOf": []any{map[string]any{"type": "string"}}},
				},
			},
		},
		{
			name:  "union-free schema",
			tools: []any{&dto.Tool{Name: "flat", InputSchema: rawToolSchema(t, map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}})}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &dto.ClaudeRequest{Tools: tt.tools}
			assert.Empty(t, NormalizeClaudeToolInputSchemas(req))
		})
	}
}

func toolSchemaByName(t *testing.T, tools any, name string) map[string]any {
	t.Helper()

	switch typed := tools.(type) {
	case json.RawMessage:
		var rawTools []json.RawMessage
		require.NoError(t, kitutil.Unmarshal(typed, &rawTools))
		for _, rawTool := range rawTools {
			var tool struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"input_schema"`
			}
			require.NoError(t, kitutil.Unmarshal(rawTool, &tool))
			if tool.Name != name {
				continue
			}
			var schema map[string]any
			require.NoError(t, kitutil.Unmarshal(tool.InputSchema, &schema))
			return schema
		}
	case []any:
		for _, item := range typed {
			switch tool := item.(type) {
			case *dto.Tool:
				if tool.Name == name {
					var schema map[string]any
					require.NoError(t, kitutil.Unmarshal(tool.InputSchema, &schema))
					return schema
				}
			case dto.Tool:
				if tool.Name == name {
					var schema map[string]any
					require.NoError(t, kitutil.Unmarshal(tool.InputSchema, &schema))
					return schema
				}
			case map[string]any:
				if toolName, _ := tool["name"].(string); toolName == name {
					schema, _ := tool["input_schema"].(map[string]any)
					return schema
				}
			}
		}
	case []*dto.Tool:
		for _, tool := range typed {
			if tool != nil && tool.Name == name {
				var schema map[string]any
				require.NoError(t, kitutil.Unmarshal(tool.InputSchema, &schema))
				return schema
			}
		}
	case []dto.Tool:
		for i := range typed {
			if typed[i].Name == name {
				var schema map[string]any
				require.NoError(t, kitutil.Unmarshal(typed[i].InputSchema, &schema))
				return schema
			}
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
