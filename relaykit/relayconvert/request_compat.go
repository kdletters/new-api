package relayconvert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/convdiag"
	sharedclaude "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/claude"
	sharedgemini "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/gemini"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/sjson"
)

func ClaudeMessagesRequestToOpenAIChat(claudeRequest dto.ClaudeRequest, info convmeta.Meta) (*dto.GeneralOpenAIRequest, error) {
	return convertCompatRequest[dto.GeneralOpenAIRequest](context.Background(), info, types.RelayFormatOpenAI, &claudeRequest)
}

func OpenAIChatRequestToClaudeMessages(c context.Context, info convmeta.Meta, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	return convertCompatRequest[dto.ClaudeRequest](c, info, types.RelayFormatClaude, &textRequest)
}

func GeminiGenerateContentRequestToOpenAIChat(geminiRequest *dto.GeminiChatRequest, info convmeta.Meta) (*dto.GeneralOpenAIRequest, error) {
	return convertCompatRequest[dto.GeneralOpenAIRequest](context.Background(), info, types.RelayFormatOpenAI, geminiRequest)
}

func OpenAIChatRequestToGeminiGenerateContent(c context.Context, textRequest dto.GeneralOpenAIRequest, info convmeta.Meta) (*dto.GeminiChatRequest, error) {
	return convertCompatRequest[dto.GeminiChatRequest](c, info, types.RelayFormatGemini, &textRequest)
}

func ApplyGeminiThinkingConfigChecked(geminiRequest *dto.GeminiChatRequest, info convmeta.Meta, oaiRequest ...dto.GeneralOpenAIRequest) error {
	ctx, collector := convdiag.WithCollector(context.Background())
	err := reasoning.AsClientError(sharedgemini.ApplyThinkingConfig(ctx, geminiRequest, info, oaiRequest...))
	if recorder, ok := info.(interface {
		RecordConversionDiagnostics(context.Context, []types.ConversionDiagnostic)
	}); ok {
		diagnostics := collector.Diagnostics()
		for i := range diagnostics {
			if diagnostics[i].To == "" {
				diagnostics[i].To = types.RelayFormatGemini
			}
		}
		recorder.RecordConversionDiagnostics(ctx, diagnostics)
	}
	return err
}

func ApplyClaudeThinkingModel(claudeRequest *dto.ClaudeRequest, info convmeta.Meta) error {
	ctx, collector := convdiag.WithCollector(context.Background())
	err := reasoning.AsClientError(sharedclaude.ApplyReasoning(ctx, claudeRequest, info, reasoning.Intent{}, false))
	if recorder, ok := info.(interface {
		RecordConversionDiagnostics(context.Context, []types.ConversionDiagnostic)
	}); ok {
		recorder.RecordConversionDiagnostics(ctx, collector.Diagnostics())
	}
	return err
}

// NormalizeClaudeToolInputSchemas flattens top-level oneOf/anyOf/allOf unions
// of every client function tool carried by a Claude Messages request. It
// mutates the request in place and returns one warning diagnostic per changed
// tool. Hosted tools such as web_search, tools without an input schema, and
// non-object input schemas are left untouched.
func NormalizeClaudeToolInputSchemas(req *dto.ClaudeRequest) []types.ConversionDiagnostic {
	if req == nil {
		return nil
	}
	rawTools, ok := req.Tools.(json.RawMessage)
	if !ok {
		var err error
		rawTools, err = kitutil.Marshal(req.Tools)
		if err != nil {
			return nil
		}
	}
	normalized, diagnostics := normalizeClaudeToolInputSchemasRaw(rawTools)
	if len(diagnostics) == 0 {
		return diagnostics
	}
	req.Tools = normalized
	return diagnostics
}

func normalizeClaudeToolInputSchemasRaw(rawTools json.RawMessage) (json.RawMessage, []types.ConversionDiagnostic) {
	var tools []json.RawMessage
	if err := kitutil.Unmarshal(rawTools, &tools); err != nil {
		return rawTools, nil
	}
	var diagnostics []types.ConversionDiagnostic
	changed := false
	for index := range tools {
		var tool struct {
			Name        string          `json:"name"`
			Type        string          `json:"type"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		if err := kitutil.Unmarshal(tools[index], &tool); err != nil || len(tool.InputSchema) == 0 {
			continue
		}
		if strings.HasPrefix(tool.Type, "web_search") {
			continue
		}
		var schema map[string]any
		if err := kitutil.Unmarshal(tool.InputSchema, &schema); err != nil {
			continue
		}
		typeName, isString := schema["type"].(string)
		if isString && !strings.EqualFold(strings.TrimSpace(typeName), "object") {
			continue
		}
		if !sharedclaude.FlattenInputSchemaUnions(schema) {
			continue
		}
		flattened, err := kitutil.Marshal(schema)
		if err != nil {
			continue
		}
		updated, err := sjson.SetRawBytes(tools[index], "input_schema", flattened)
		if err != nil {
			continue
		}
		tools[index] = updated
		changed = true
		diagnostics = append(diagnostics, types.ConversionDiagnostic{
			Code:     "claude_tool_schema_union_flattened",
			Path:     fmt.Sprintf("tools[%d].input_schema", index),
			Message:  fmt.Sprintf("tool %q input_schema used a top-level oneOf/anyOf/allOf union; flattened because Claude rejects top-level unions", tool.Name),
			Severity: types.ConversionDiagnosticWarning,
		})
	}
	if !changed {
		return rawTools, diagnostics
	}
	normalized, err := kitutil.Marshal(tools)
	if err != nil {
		return rawTools, nil
	}
	return normalized, diagnostics
}

func ChatCompletionsRequestToResponsesRequest(req *dto.GeneralOpenAIRequest) (*dto.OpenAIResponsesRequest, error) {
	return convertCompatRequest[dto.OpenAIResponsesRequest](context.Background(), nil, types.RelayFormatOpenAIResponses, req)
}

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	return convertCompatRequest[dto.GeneralOpenAIRequest](context.Background(), nil, types.RelayFormatOpenAI, req)
}

func OpenAIResponsesRequestToClaudeMessages(c context.Context, info convmeta.Meta, req *dto.OpenAIResponsesRequest) (*dto.ClaudeRequest, error) {
	return convertCompatRequest[dto.ClaudeRequest](c, info, types.RelayFormatClaude, req)
}

func OpenAIResponsesRequestToGeminiChat(c context.Context, req *dto.OpenAIResponsesRequest, info convmeta.Meta) (*dto.GeminiChatRequest, error) {
	return convertCompatRequest[dto.GeminiChatRequest](c, info, types.RelayFormatGemini, req)
}

func convertCompatRequest[T any](c context.Context, info convmeta.Meta, target types.RelayFormat, request any) (*T, error) {
	result, err := ConvertRequest(c, info, target, request)
	if err != nil {
		return nil, err
	}
	converted, ok := result.Value.(*T)
	if !ok {
		return nil, fmt.Errorf("expected %s request, got %T", target, result.Value)
	}
	return converted, nil
}
