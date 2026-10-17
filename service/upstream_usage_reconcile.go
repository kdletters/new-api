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
	upstreamUsageReconcileTotalTimeout  = 20 * time.Second
)

var upstreamUsageReconcileDelays = []time.Duration{
	0,
	time.Second,
	2 * time.Second,
	4 * time.Second,
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

// ReconcileEstimatedClaudeStreamUsage replaces a local fallback estimate with the
// upstream New API token log when an incomplete Claude stream leaves billing
// usage estimated. This covers both zero-frame disconnects and partial streams
// that ended before the terminal usage frame. The caller is already gone at this
// point, so a bounded post-stream wait does not delay the client. If the
// upstream log is unavailable, the estimate is kept.
func ReconcileEstimatedClaudeStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) (result *dto.Usage) {
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

		entry, err := fetchUpstreamTokenUsageLog(ctx, client, endpoint, info.ApiKey, upstreamRequestID)
		if err != nil {
			lastErr = err
			if errors.Is(err, errUpstreamUsageLogEndpointUnavailable) {
				break
			}
			continue
		}
		if entry == nil {
			lastErr = nil
			continue
		}
		reconciled, ok := upstreamUsageFromTokenLog(entry)
		if !ok {
			lastErr = fmt.Errorf("upstream usage log %d has invalid or incomplete usage", entry.ID)
			continue
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

func fetchUpstreamTokenUsageLog(ctx context.Context, client *http.Client, endpoint string, apiKey string, requestID string) (*upstreamTokenUsageLog, error) {
	requestCtx, cancel := context.WithTimeout(ctx, upstreamTokenUsageLogRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
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
	for i := range payload.Data {
		entry := &payload.Data[i]
		if entry.Type != model.LogTypeConsume {
			continue
		}
		if entry.RequestID == requestID || entry.UpstreamRequestID == requestID {
			return entry, nil
		}
	}
	return nil, nil
}

func upstreamUsageFromTokenLog(entry *upstreamTokenUsageLog) (*dto.Usage, bool) {
	if entry == nil || entry.Type != model.LogTypeConsume {
		return nil, false
	}
	if entry.PromptTokens < 0 || entry.CompletionTokens < 0 ||
		entry.PromptTokens > common.MaxQuota || entry.CompletionTokens > common.MaxQuota ||
		entry.PromptTokens > common.MaxQuota-entry.CompletionTokens {
		return nil, false
	}

	other := upstreamUsageOtherMap(entry.Other)
	cacheTokens, ok := upstreamUsageCount(other, "cache_tokens")
	if !ok {
		return nil, false
	}
	cacheCreationTokens, ok := upstreamUsageCount(other, "cache_creation_tokens")
	if !ok {
		return nil, false
	}
	cacheCreation5m, ok := upstreamUsageCount(other, "cache_creation_tokens_5m")
	if !ok {
		return nil, false
	}
	cacheCreation1h, ok := upstreamUsageCount(other, "cache_creation_tokens_1h")
	if !ok {
		return nil, false
	}
	if cacheCreationTokens == 0 {
		if cacheCreation5m > common.MaxQuota-cacheCreation1h {
			return nil, false
		}
		cacheCreationTokens = cacheCreation5m + cacheCreation1h
	} else if cacheCreation5m > cacheCreationTokens ||
		cacheCreation1h > cacheCreationTokens-cacheCreation5m {
		return nil, false
	}
	if entry.PromptTokens == 0 && entry.CompletionTokens == 0 && cacheTokens == 0 && cacheCreationTokens == 0 {
		return nil, false
	}

	claudeUsage := &dto.ClaudeUsage{
		InputTokens:                 entry.PromptTokens,
		OutputTokens:                entry.CompletionTokens,
		CacheReadInputTokens:        cacheTokens,
		CacheCreationInputTokens:    cacheCreationTokens,
		ClaudeCacheCreation5mTokens: cacheCreation5m,
		ClaudeCacheCreation1hTokens: cacheCreation1h,
	}
	if cacheCreation5m > 0 || cacheCreation1h > 0 {
		claudeUsage.CacheCreation = &dto.ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: cacheCreation5m,
			Ephemeral1hInputTokens: cacheCreation1h,
		}
	}
	billingUsage := dto.NewClaudeMessagesBillingUsage(claudeUsage)
	if billingUsage == nil {
		return nil, false
	}

	return &dto.Usage{
		PromptTokens:         entry.PromptTokens,
		CompletionTokens:     entry.CompletionTokens,
		TotalTokens:          entry.PromptTokens + entry.CompletionTokens,
		PromptCacheHitTokens: cacheTokens,
		UsageSemantic:        "anthropic",
		UsageSource:          "anthropic",
		BillingUsage:         billingUsage,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         cacheTokens,
			CachedCreationTokens: cacheCreationTokens,
		},
		InputTokens:                 entry.PromptTokens,
		OutputTokens:                entry.CompletionTokens,
		ClaudeCacheCreation5mTokens: cacheCreation5m,
		ClaudeCacheCreation1hTokens: cacheCreation1h,
	}, true
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
