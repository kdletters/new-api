package relayconvert

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/internal/convdiag"
	sharedclaude "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/claude"
	sharedgemini "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/gemini"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
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

// claudeToolInputSchema is one tool input schema found in a Claude request,
// tagged with the tool type so hosted tools can be skipped without depending on
// the concrete tool slice type.
type claudeToolInputSchema struct {
	name        string
	typeName    string
	inputSchema map[string]any
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
	var diagnostics []types.ConversionDiagnostic
	for index, tool := range claudeRequestToolInputSchemas(req.Tools) {
		schema := tool.inputSchema
		if schema == nil || len(schema) == 0 {
			continue
		}
		if strings.HasPrefix(tool.typeName, "web_search") {
			continue
		}
		typeName, isString := schema["type"].(string)
		if isString && !strings.EqualFold(strings.TrimSpace(typeName), "object") {
			continue
		}
		if !sharedclaude.FlattenInputSchemaUnions(schema) {
			continue
		}
		diagnostics = append(diagnostics, types.ConversionDiagnostic{
			Code:     "claude_tool_schema_union_flattened",
			Path:     fmt.Sprintf("tools[%d].input_schema", index),
			Message:  fmt.Sprintf("tool %q input_schema used a top-level oneOf/anyOf/allOf union; flattened because Claude rejects top-level unions", tool.name),
			Severity: types.ConversionDiagnosticWarning,
		})
	}
	return diagnostics
}

func claudeRequestToolInputSchemas(tools any) []claudeToolInputSchema {
	if tools == nil {
		return nil
	}
	var entries []any
	switch value := tools.(type) {
	case []any:
		entries = value
	case []map[string]any:
		for _, tool := range value {
			if tool != nil {
				entries = append(entries, tool)
			}
		}
	case []*dto.Tool:
		entries = make([]any, 0, len(value))
		for _, tool := range value {
			if tool != nil {
				entries = append(entries, tool)
			}
		}
	case []dto.Tool:
		entries = make([]any, 0, len(value))
		for i := range value {
			entries = append(entries, &value[i])
		}
	case []*dto.ClaudeWebSearchTool:
		entries = make([]any, 0, len(value))
		for _, tool := range value {
			if tool != nil {
				entries = append(entries, tool)
			}
		}
	case []dto.ClaudeWebSearchTool:
		entries = make([]any, 0, len(value))
		for i := range value {
			entries = append(entries, &value[i])
		}
	default:
		return nil
	}

	schemas := make([]claudeToolInputSchema, 0, len(entries))
	for _, entry := range entries {
		switch tool := entry.(type) {
		case *dto.Tool:
			if tool != nil {
				schemas = append(schemas, claudeToolInputSchema{name: tool.Name, inputSchema: tool.InputSchema})
			}
		case dto.Tool:
			schemas = append(schemas, claudeToolInputSchema{name: tool.Name, inputSchema: tool.InputSchema})
		case *dto.ClaudeWebSearchTool:
			if tool != nil {
				schemas = append(schemas, claudeToolInputSchema{name: tool.Name, typeName: tool.Type})
			}
		case dto.ClaudeWebSearchTool:
			schemas = append(schemas, claudeToolInputSchema{name: tool.Name, typeName: tool.Type})
		case map[string]any:
			name, _ := tool["name"].(string)
			typeName, _ := tool["type"].(string)
			schemas = append(schemas, claudeToolInputSchema{
				name:        name,
				typeName:    typeName,
				inputSchema: stringKeyedMap(tool["input_schema"]),
			})
		}
	}
	return schemas
}

// stringKeyedMap defensively accepts the map types a JSON-decoded schema may
// have; a nil result means "no object schema here".
func stringKeyedMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			out[key] = nested
		}
		return out
	default:
		return nil
	}
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
