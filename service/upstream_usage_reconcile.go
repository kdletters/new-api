package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

const (
	upstreamTokenUsageLogPath           = "/api/log/token"
	upstreamTokenUsageLogMaxBodyBytes   = 8 << 20
	upstreamTokenUsageLogRequestTimeout = 8 * time.Second
	upstreamUsageReconcileTotalTimeout  = 90 * time.Second

	upstreamUsageReconciliationNotBilled = "not_billed"
)

var upstreamUsageReconcileDelays = []time.Duration{
	0,
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

var errUpstreamUsageLogEndpointUnavailable = errors.New("upstream usage log endpoint unavailable")

type upstreamTokenUsageLogResponse struct {
	Success bool                    `json:"success"`
	Message string                  `json:"message"`
	Data    []upstreamTokenUsageLog `json:"data"`
}

type upstreamTokenUsageLog struct {
	ID                int64  `json:"id"`
	Type              int    `json:"type"`
	Content           string `json:"content"`
	RequestID         string `json:"request_id"`
	UpstreamRequestID string `json:"upstream_request_id"`
	ModelName         string `json:"model_name"`
	PromptTokens      int    `json:"prompt_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
	Quota             int    `json:"quota"`
	UseTime           int    `json:"use_time"`
	IsStream          bool   `json:"is_stream"`
	Other             any    `json:"other"`
}

// ReconcileEstimatedTextUsage replaces a local fallback estimate with the
// upstream New API token log when an incomplete text stream leaves billing usage
// estimated. It supports Anthropic, OpenAI, and Gemini usage semantics. The
// caller is already gone at this point, so a bounded post-stream wait does not
// delay the client. If the upstream log is unavailable, the estimate is kept.
func ReconcileEstimatedTextUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) (result *dto.Usage) {
	if c == nil || info == nil || info.ChannelMeta == nil || usage == nil || usage.BillingUsage == nil {
		return usage
	}
	if strings.TrimSpace(info.ChannelBaseUrl) == "" || strings.TrimSpace(info.ApiKey) == "" {
		return usage
	}
	if !usage.BillingUsage.Estimated {
		return usage
	}
	upstreamRequestID := strings.TrimSpace(c.GetString(common.UpstreamRequestIdKey))
	if upstreamRequestID == "" {
		return usage
	}
	reconcileStarted := time.Now()
	defer func() {
		info.UpstreamUsageReconciliationWait = time.Since(reconcileStarted)
	}()
	endpoint, err := upstreamTokenUsageLogURL(info.ChannelBaseUrl)
	if err != nil {
		info.UpstreamUsageReconciliation = "invalid_endpoint"
		logger.LogWarn(c, "upstream usage reconciliation skipped: "+err.Error())
		return usage
	}
	client, err := GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		info.UpstreamUsageReconciliation = "failed"
		logger.LogWarn(c, "upstream usage reconciliation client failed: "+err.Error())
		return usage
	}

	ctx, cancel := context.WithTimeout(context.Background(), upstreamUsageReconcileTotalTimeout)
	defer cancel()

	var lastErr error
	for _, delay := range upstreamUsageReconcileDelays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				lastErr = ctx.Err()
				break
			}
		}
		if ctx.Err() != nil {
			break
		}

		entries, err := fetchUpstreamTokenUsageLog(ctx, client, endpoint, info.ApiKey, upstreamRequestID)
		if err != nil {
			lastErr = err
			if errors.Is(err, errUpstreamUsageLogEndpointUnavailable) {
				break
			}
			continue
		}
		if len(entries) == 0 {
			lastErr = nil
			continue
		}
		expectedSemantic := usage.BillingUsage.Semantic
		if expectedSemantic == "" {
			expectedSemantic = usage.UsageSemantic
		}
		var cancelledEntry *upstreamTokenUsageLog
		for i := range entries {
			entry := &entries[i]
			if entry.Type == model.LogTypeError {
				if upstreamLogIsClientCancelled(entry) {
					cancelledEntry = entry
				}
				continue
			}
			reconciled, ok := upstreamUsageFromTokenLog(entry, expectedSemantic, usage.BillingUsage.Source)
			if !ok {
				continue
			}
			if upstreamConsumeLogIsUnbilledClientCancellation(info, entry) {
				if notBilled := notBilledUsageForSemantic(expectedSemantic, usage.BillingUsage.Source); notBilled != nil {
					markUpstreamUsageNotBilled(c, info, upstreamRequestID, entry, "client_cancelled_zero_quota")
					return notBilled
				}
			}
			info.UpstreamUsageReconciliation = "matched"
			info.UpstreamUsageLogID = entry.ID
			info.UpstreamUsageQuota = entry.Quota
			common.SetContextKey(c, constant.ContextKeyLocalCountTokens, false)
			logger.LogInfo(c, fmt.Sprintf(
				"upstream usage reconciled: request_id=%s upstream_log_id=%d upstream_quota=%d prompt_tokens=%d completion_tokens=%d cache_tokens=%d cache_creation_tokens=%d",
				upstreamRequestID,
				entry.ID,
				entry.Quota,
				reconciled.PromptTokens,
				reconciled.CompletionTokens,
				reconciled.PromptTokensDetails.CachedTokens,
				reconciled.PromptTokensDetails.CachedCreationTokens,
			))
			return reconciled
		}
		if cancelledEntry != nil {
			reconciled := notBilledUsageForSemantic(expectedSemantic, usage.BillingUsage.Source)
			if reconciled != nil {
				markUpstreamUsageNotBilled(c, info, upstreamRequestID, cancelledEntry, "client_cancelled")
				return reconciled
			}
		}
		lastErr = fmt.Errorf("upstream usage logs for request %s have no usable usage", upstreamRequestID)
		continue
	}

	info.UpstreamUsageReconciliation = "not_found"
	if lastErr != nil {
		info.UpstreamUsageReconciliation = "failed"
	}
	logger.LogWarn(c, fmt.Sprintf("upstream usage reconciliation unavailable: request_id=%s status=%s error=%v", upstreamRequestID, info.UpstreamUsageReconciliation, lastErr))
	return usage
}

func upstreamTokenUsageLogURL(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", errors.New("upstream base URL is empty")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse upstream base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("upstream base URL must be absolute")
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/v1")
	parsed.Path = strings.TrimRight(path, "/") + upstreamTokenUsageLogPath
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func fetchUpstreamTokenUsageLog(ctx context.Context, client *http.Client, endpoint string, apiKey string, requestID string) ([]upstreamTokenUsageLog, error) {
	requestCtx, cancel := context.WithTimeout(ctx, upstreamTokenUsageLogRequestTimeout)
	defer cancel()

	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse upstream usage log URL: %w", err)
	}
	query := endpointURL.Query()
	query.Set("request_id", requestID)
	query.Set("upstream_request_id", requestID)
	endpointURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpointURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed ||
		resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: status=%d", errUpstreamUsageLogEndpointUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("upstream usage log status=%d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamTokenUsageLogMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > upstreamTokenUsageLogMaxBodyBytes {
		return nil, errors.New("upstream usage log response exceeds limit")
	}

	var payload upstreamTokenUsageLogResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode upstream usage log: %w", err)
	}
	if !payload.Success {
		return nil, fmt.Errorf("upstream usage log rejected: %s", strings.TrimSpace(payload.Message))
	}
	entries := make([]upstreamTokenUsageLog, 0, 2)
	for i := range payload.Data {
		entry := &payload.Data[i]
		if entry.Type != model.LogTypeConsume && entry.Type != model.LogTypeError {
			continue
		}
		if entry.RequestID == requestID || entry.UpstreamRequestID == requestID {
			entries = append(entries, *entry)
		}
	}
	return entries, nil
}

func upstreamLogIsClientCancelled(entry *upstreamTokenUsageLog) bool {
	if entry == nil || entry.Type != model.LogTypeError {
		return false
	}
	if strings.Contains(entry.Content, "status_code=499") {
		return true
	}
	other := upstreamUsageOtherMap(entry.Other)
	statusCode, ok := upstreamUsageCount(other, "status_code")
	return ok && statusCode == 499
}

// A New API-compatible upstream can persist a zero-quota consume row after the
// client cancels before any SSE data arrives. That row's prompt_tokens are a
// fallback estimate, not billable usage, so settling it would turn a cancelled
// request into a charge that the upstream never incurred.
func upstreamConsumeLogIsUnbilledClientCancellation(info *relaycommon.RelayInfo, entry *upstreamTokenUsageLog) bool {
	if info == nil || entry == nil || entry.Type != model.LogTypeConsume {
		return false
	}
	if entry.Quota != 0 || entry.CompletionTokens != 0 || info.ReceivedResponseCount != 0 {
		return false
	}
	return info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone
}

func markUpstreamUsageNotBilled(c *gin.Context, info *relaycommon.RelayInfo, upstreamRequestID string, entry *upstreamTokenUsageLog, reason string) {
	info.UpstreamUsageReconciliation = upstreamUsageReconciliationNotBilled
	info.UpstreamUsageLogID = entry.ID
	info.UpstreamUsageQuota = entry.Quota
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, false)
	logger.LogInfo(c, fmt.Sprintf(
		"upstream usage not billed: request_id=%s upstream_log_id=%d reason=%s prompt_tokens=%d completion_tokens=%d",
		upstreamRequestID,
		entry.ID,
		reason,
		entry.PromptTokens,
		entry.CompletionTokens,
	))
}

func notBilledUsageForSemantic(semantic string, source string) *dto.Usage {
	switch semantic {
	case dto.BillingUsageSemanticAnthropic:
		return &dto.Usage{
			UsageSemantic: semantic,
			UsageSource:   dto.BillingUsageSourceClaudeMessages,
		}
	case dto.BillingUsageSemanticOpenAI:
		if source == "" {
			source = dto.BillingUsageSourceOAIChat
		}
		return &dto.Usage{
			UsageSemantic: semantic,
			UsageSource:   source,
		}
	case dto.BillingUsageSemanticGemini:
		return &dto.Usage{
			UsageSemantic: semantic,
			UsageSource:   dto.BillingUsageSourceGeminiChat,
		}
	default:
		return nil
	}
}

type upstreamUsageCounts struct {
	prompt          int
	completion      int
	cacheRead       int
	cacheCreation   int
	cacheCreation5m int
	cacheCreation1h int
}

func upstreamUsageFromTokenLog(entry *upstreamTokenUsageLog, expectedSemantic string, expectedSource string) (*dto.Usage, bool) {
	if entry == nil || entry.Type != model.LogTypeConsume {
		return nil, false
	}
	if entry.PromptTokens < 0 || entry.CompletionTokens < 0 ||
		entry.PromptTokens > common.MaxQuota || entry.CompletionTokens > common.MaxQuota ||
		entry.PromptTokens > common.MaxQuota-entry.CompletionTokens {
		return nil, false
	}

	other := upstreamUsageOtherMap(entry.Other)
	counts := upstreamUsageCounts{prompt: entry.PromptTokens, completion: entry.CompletionTokens}
	var ok bool
	if counts.cacheRead, ok = upstreamUsageCount(other, "cache_tokens"); !ok {
		return nil, false
	}
	if counts.cacheCreation, ok = upstreamUsageCount(other, "cache_creation_tokens"); !ok {
		return nil, false
	}
	if counts.cacheCreation5m, ok = upstreamUsageCount(other, "cache_creation_tokens_5m"); !ok {
		return nil, false
	}
	if counts.cacheCreation1h, ok = upstreamUsageCount(other, "cache_creation_tokens_1h"); !ok {
		return nil, false
	}
	if counts.cacheCreation == 0 {
		if counts.cacheCreation5m > common.MaxQuota-counts.cacheCreation1h {
			return nil, false
		}
		counts.cacheCreation = counts.cacheCreation5m + counts.cacheCreation1h
	} else if counts.cacheCreation5m > counts.cacheCreation ||
		counts.cacheCreation1h > counts.cacheCreation-counts.cacheCreation5m {
		return nil, false
	}
	if counts.prompt == 0 && counts.completion == 0 && counts.cacheRead == 0 && counts.cacheCreation == 0 {
		return nil, false
	}

	semantic := strings.ToLower(strings.TrimSpace(upstreamUsageString(other, "usage_semantic")))
	if !isSupportedUsageSemantic(semantic) {
		semantic = strings.ToLower(strings.TrimSpace(expectedSemantic))
	}
	switch semantic {
	case dto.BillingUsageSemanticAnthropic:
		return anthropicUsageFromCounts(counts), true
	case dto.BillingUsageSemanticGemini:
		return geminiUsageFromCounts(counts)
	case dto.BillingUsageSemanticOpenAI:
		source := strings.TrimSpace(upstreamUsageString(other, "usage_source"))
		if source == "" {
			source = expectedSource
		}
		return openAIUsageFromCounts(counts, source), true
	default:
		return nil, false
	}
}

func isSupportedUsageSemantic(semantic string) bool {
	switch semantic {
	case dto.BillingUsageSemanticAnthropic, dto.BillingUsageSemanticOpenAI, dto.BillingUsageSemanticGemini:
		return true
	default:
		return false
	}
}

func anthropicUsageFromCounts(counts upstreamUsageCounts) *dto.Usage {
	claudeUsage := &dto.ClaudeUsage{
		InputTokens:                 counts.prompt,
		OutputTokens:                counts.completion,
		CacheReadInputTokens:        counts.cacheRead,
		CacheCreationInputTokens:    counts.cacheCreation,
		ClaudeCacheCreation5mTokens: counts.cacheCreation5m,
		ClaudeCacheCreation1hTokens: counts.cacheCreation1h,
	}
	if counts.cacheCreation5m > 0 || counts.cacheCreation1h > 0 {
		claudeUsage.CacheCreation = &dto.ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: counts.cacheCreation5m,
			Ephemeral1hInputTokens: counts.cacheCreation1h,
		}
	}
	return &dto.Usage{
		PromptTokens:         counts.prompt,
		CompletionTokens:     counts.completion,
		TotalTokens:          counts.prompt + counts.completion,
		PromptCacheHitTokens: counts.cacheRead,
		UsageSemantic:        dto.BillingUsageSemanticAnthropic,
		UsageSource:          dto.BillingUsageSourceClaudeMessages,
		BillingUsage:         dto.NewClaudeMessagesBillingUsage(claudeUsage),
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         counts.cacheRead,
			CachedCreationTokens: counts.cacheCreation,
		},
		InputTokens:                 counts.prompt,
		OutputTokens:                counts.completion,
		ClaudeCacheCreation5mTokens: counts.cacheCreation5m,
		ClaudeCacheCreation1hTokens: counts.cacheCreation1h,
	}
}

func openAIUsageFromCounts(counts upstreamUsageCounts, source string) *dto.Usage {
	if source == "" {
		source = dto.BillingUsageSourceOAIChat
	}
	usage := &dto.Usage{
		PromptTokens:         counts.prompt,
		CompletionTokens:     counts.completion,
		TotalTokens:          counts.prompt + counts.completion,
		PromptCacheHitTokens: counts.cacheRead,
		UsageSemantic:        dto.BillingUsageSemanticOpenAI,
		UsageSource:          source,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         counts.cacheRead,
			CachedCreationTokens: counts.cacheCreation,
		},
		InputTokens:  counts.prompt,
		OutputTokens: counts.completion,
	}
	if source == dto.BillingUsageSourceOAIResponses {
		usage.BillingUsage = dto.NewOpenAIResponsesBillingUsage(usage)
	} else {
		usage.BillingUsage = dto.NewOpenAIChatBillingUsage(usage)
	}
	return usage
}

func geminiUsageFromCounts(counts upstreamUsageCounts) (*dto.Usage, bool) {
	metadata := &dto.GeminiUsageMetadata{
		PromptTokenCount:        counts.prompt,
		CandidatesTokenCount:    counts.completion,
		TotalTokenCount:         counts.prompt + counts.completion,
		CachedContentTokenCount: counts.cacheRead,
	}
	billingUsage := dto.NewGeminiChatBillingUsage(metadata)
	if billingUsage == nil {
		return nil, false
	}
	return billingUsage.CanonicalUsage()
}

func upstreamUsageString(other map[string]any, key string) string {
	if len(other) == 0 {
		return ""
	}
	value, ok := other[key]
	if !ok {
		return ""
	}
	switch value := value.(type) {
	case string:
		return value
	default:
		return ""
	}
}

func upstreamUsageOtherMap(raw any) map[string]any {
	switch value := raw.(type) {
	case map[string]any:
		return value
	case string:
		var other map[string]any
		if err := common.UnmarshalJsonStr(value, &other); err == nil {
			return other
		}
	}
	return nil
}

func upstreamUsageCount(other map[string]any, key string) (int, bool) {
	if len(other) == 0 {
		return 0, true
	}
	value, exists := other[key]
	if !exists || value == nil {
		return 0, true
	}
	var parsed int64
	switch number := value.(type) {
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > float64(common.MaxQuota) || number != math.Trunc(number) {
			return 0, false
		}
		return int(number), true
	case int:
		parsed = int64(number)
	case int64:
		parsed = number
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
		if err != nil {
			return 0, false
		}
		parsed = value
	default:
		return 0, false
	}
	if parsed < 0 || parsed > int64(common.MaxQuota) {
		return 0, false
	}
	return int(parsed), true
}
