package aws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type AwsClaudeRequest struct {
	// AnthropicVersion should be "bedrock-2023-05-31"
	AnthropicVersion  string              `json:"anthropic_version"`
	AnthropicBeta     json.RawMessage     `json:"anthropic_beta,omitempty"`
	System            any                 `json:"system,omitempty"`
	Messages          []dto.ClaudeMessage `json:"messages"`
	MaxTokens         *uint               `json:"max_tokens,omitempty"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	TopK              *int                `json:"top_k,omitempty"`
	StopSequences     []string            `json:"stop_sequences,omitempty"`
	Tools             any                 `json:"tools,omitempty"`
	ToolChoice        any                 `json:"tool_choice,omitempty"`
	ContextManagement json.RawMessage     `json:"context_management,omitempty"`
	Thinking          *dto.Thinking       `json:"thinking,omitempty"`
	OutputConfig      json.RawMessage     `json:"output_config,omitempty"`
	//Metadata         json.RawMessage     `json:"metadata,omitempty"`
}

func formatRequest(requestBody io.Reader, requestHeader http.Header, awsModelID string) (*AwsClaudeRequest, error) {
	var awsClaudeRequest AwsClaudeRequest
	err := common.DecodeJson(requestBody, &awsClaudeRequest)
	if err != nil {
		return nil, err
	}
	awsClaudeRequest.AnthropicVersion = "bedrock-2023-05-31"
	normalizeAwsClaudeRequest(&awsClaudeRequest, awsModelID)

	// check header anthropic-beta
	var betas []string
	for beta := range strings.SplitSeq(requestHeader.Get("anthropic-beta"), ",") {
		if beta = strings.TrimSpace(beta); beta != "" {
			betas = append(betas, beta)
		}
	}
	if len(betas) > 0 {
		betaJson, err := common.Marshal(betas)
		if err != nil {
			return nil, err
		}
		awsClaudeRequest.AnthropicBeta = betaJson
	}
	logger.LogJson(context.Background(), "json", awsClaudeRequest)
	return &awsClaudeRequest, nil
}

// Bedrock InvokeModel validates the native Claude schema more strictly than
// Anthropic-compatible gateways. Normalize the small set of client-side
// extensions that otherwise fail validation before the model runs.
func normalizeAwsClaudeRequest(request *AwsClaudeRequest, awsModelID string) {
	request.System = normalizeAwsClaudeSystem(request.System)
	for i := range request.Messages {
		request.Messages[i].Content = normalizeAwsClaudeMessageContent(request.Messages[i].Content)
	}
	if awsClaudeRejectsToolStrict(awsModelID) {
		request.Tools = stripAwsClaudeToolStrict(request.Tools)
	}
}

func normalizeAwsClaudePayloadMap(data map[string]json.RawMessage, awsModelID string) error {
	if raw, ok := data["system"]; ok {
		var system any
		if err := common.Unmarshal(raw, &system); err != nil {
			return err
		}
		normalized, err := common.Marshal(normalizeAwsClaudeSystem(system))
		if err != nil {
			return err
		}
		data["system"] = normalized
	}
	if raw, ok := data["messages"]; ok {
		var messages []any
		if err := common.Unmarshal(raw, &messages); err != nil {
			return err
		}
		for _, rawMessage := range messages {
			message, ok := rawMessage.(map[string]any)
			if !ok {
				continue
			}
			if content, exists := message["content"]; exists {
				message["content"] = normalizeAwsClaudeMessageContent(content)
			}
		}
		normalized, err := common.Marshal(messages)
		if err != nil {
			return err
		}
		data["messages"] = normalized
	}
	if awsClaudeRejectsToolStrict(awsModelID) {
		if raw, ok := data["tools"]; ok {
			var tools any
			if err := common.Unmarshal(raw, &tools); err != nil {
				return err
			}
			normalized, err := common.Marshal(stripAwsClaudeToolStrict(tools))
			if err != nil {
				return err
			}
			data["tools"] = normalized
		}
	}
	return nil
}

func normalizeAwsClaudeSystem(system any) any {
	blocks, ok := system.([]any)
	if !ok {
		return system
	}
	normalized := make([]any, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		block = normalizeAwsClaudeContentBlock(block)
		blockType, _ := block["type"].(string)
		if blockType != "text" {
			logger.LogWarn(context.Background(), "drop unsupported AWS Claude system block type %q", blockType)
			continue
		}
		normalized = append(normalized, block)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeAwsClaudeMessageContent(content any) any {
	blocks, ok := content.([]any)
	if !ok {
		return content
	}
	normalized := make([]any, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			normalized = append(normalized, rawBlock)
			continue
		}
		block = normalizeAwsClaudeContentBlock(block)
		normalized = append(normalized, block)
	}
	return normalized
}

func normalizeAwsClaudeContentBlock(block map[string]any) map[string]any {
	blockType, _ := block["type"].(string)
	switch blockType {
	case "input_text", "output_text":
		block["type"] = "text"
	case "tool_addition", "tool_removal":
		textBlock := map[string]any{
			"type": "text",
			"text": awsClaudeToolChangeSummary(block),
		}
		if cacheControl, ok := block["cache_control"]; ok {
			textBlock["cache_control"] = cacheControl
		}
		return textBlock
	case "":
		if text, ok := block["text"].(string); ok && text != "" {
			block["type"] = "text"
		}
	}
	if blockType == "tool_result" || blockType == "mid_conv_system" {
		if content, ok := block["content"]; ok {
			block["content"] = normalizeAwsClaudeMessageContent(content)
		}
	}
	if blockType == "tool_use" {
		switch input := block["input"].(type) {
		case string:
			var parsed map[string]any
			if err := common.UnmarshalJsonStr(input, &parsed); err == nil {
				block["input"] = parsed
			}
		case nil:
			block["input"] = map[string]any{}
		}
	}
	return block
}

func awsClaudeToolChangeSummary(block map[string]any) string {
	action := "Tool added"
	if block["type"] == "tool_removal" {
		action = "Tool removed"
	}
	name := ""
	if tool, ok := block["tool"].(map[string]any); ok {
		name, _ = tool["name"].(string)
		if definition, ok := tool["definition"].(map[string]any); ok {
			if definitionName, ok := definition["name"].(string); ok && definitionName != "" {
				name = definitionName
			}
		}
	}
	if name == "" {
		return action
	}
	return action + ": " + name
}

func stripAwsClaudeToolStrict(tools any) any {
	toolList, ok := tools.([]any)
	if !ok {
		return tools
	}
	for _, rawTool := range toolList {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		delete(tool, "strict")
		for _, nestedKey := range []string{"custom", "function"} {
			if nested, ok := tool[nestedKey].(map[string]any); ok {
				delete(nested, "strict")
			}
		}
	}
	return tools
}

func awsClaudeRejectsToolStrict(awsModelID string) bool {
	awsModelID = strings.ToLower(awsModelID)
	for _, family := range []string{
		"claude-opus-4-7",
		"claude-opus-4-8",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-fable-5",
		"claude-mythos-5",
	} {
		if strings.Contains(awsModelID, family) {
			return true
		}
	}
	return false
}

// NovaMessage Nova模型使用messages-v1格式
type NovaMessage struct {
	Role    string        `json:"role"`
	Content []NovaContent `json:"content"`
}

type NovaContent struct {
	Text string `json:"text"`
}

type NovaRequest struct {
	SchemaVersion   string               `json:"schemaVersion"`             // 请求版本，例如 "1.0"
	Messages        []NovaMessage        `json:"messages"`                  // 对话消息列表
	InferenceConfig *NovaInferenceConfig `json:"inferenceConfig,omitempty"` // 推理配置，可选
}

type NovaInferenceConfig struct {
	MaxTokens     int      `json:"maxTokens,omitempty"`     // 最大生成的 token 数
	Temperature   float64  `json:"temperature,omitempty"`   // 随机性 (默认 0.7, 范围 0-1)
	TopP          float64  `json:"topP,omitempty"`          // nucleus sampling (默认 0.9, 范围 0-1)
	TopK          int      `json:"topK,omitempty"`          // 限制候选 token 数 (默认 50, 范围 0-128)
	StopSequences []string `json:"stopSequences,omitempty"` // 停止生成的序列
}

// 转换OpenAI请求为Nova格式
func convertToNovaRequest(req *dto.GeneralOpenAIRequest) *NovaRequest {
	novaMessages := make([]NovaMessage, len(req.Messages))
	for i, msg := range req.Messages {
		novaMessages[i] = NovaMessage{
			Role:    msg.Role,
			Content: []NovaContent{{Text: msg.StringContent()}},
		}
	}

	novaReq := &NovaRequest{
		SchemaVersion: "messages-v1",
		Messages:      novaMessages,
	}

	// 设置推理配置
	if (req.MaxTokens != nil && *req.MaxTokens != 0) || (req.Temperature != nil && *req.Temperature != 0) || (req.TopP != nil && *req.TopP != 0) || (req.TopK != nil && *req.TopK != 0) || req.Stop != nil {
		novaReq.InferenceConfig = &NovaInferenceConfig{}
		if req.MaxTokens != nil && *req.MaxTokens != 0 {
			novaReq.InferenceConfig.MaxTokens = int(*req.MaxTokens)
		}
		if req.Temperature != nil && *req.Temperature != 0 {
			novaReq.InferenceConfig.Temperature = *req.Temperature
		}
		if req.TopP != nil && *req.TopP != 0 {
			novaReq.InferenceConfig.TopP = *req.TopP
		}
		if req.TopK != nil && *req.TopK != 0 {
			novaReq.InferenceConfig.TopK = *req.TopK
		}
		if req.Stop != nil {
			if stopSequences := parseStopSequences(req.Stop); len(stopSequences) > 0 {
				novaReq.InferenceConfig.StopSequences = stopSequences
			}
		}
	}

	return novaReq
}

// parseStopSequences 解析停止序列，支持字符串或字符串数组
func parseStopSequences(stop any) []string {
	if stop == nil {
		return nil
	}

	switch v := stop.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []any:
		var sequences []string
		for _, item := range v {
			if str, ok := item.(string); ok && str != "" {
				sequences = append(sequences, str)
			}
		}
		return sequences
	}
	return nil
}
