package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestFinalRelayStreamOutcomeClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		reason         relaycommon.StreamEndReason
		hasSoftError   bool
		channelSuccess bool
		requestSuccess bool
	}{
		{name: "done", reason: relaycommon.StreamEndReasonDone, channelSuccess: true, requestSuccess: true},
		{name: "eof", reason: relaycommon.StreamEndReasonEOF, channelSuccess: true, requestSuccess: true},
		{name: "client gone", reason: relaycommon.StreamEndReasonClientGone, channelSuccess: true, requestSuccess: false},
		{name: "scanner error", reason: relaycommon.StreamEndReasonScannerErr, channelSuccess: false, requestSuccess: false},
		{name: "timeout", reason: relaycommon.StreamEndReasonTimeout, channelSuccess: false, requestSuccess: false},
		{name: "panic", reason: relaycommon.StreamEndReasonPanic, channelSuccess: false, requestSuccess: false},
		{name: "ping failure", reason: relaycommon.StreamEndReasonPingFail, channelSuccess: false, requestSuccess: false},
		{name: "soft upstream error", reason: relaycommon.StreamEndReasonDone, hasSoftError: true, channelSuccess: false, requestSuccess: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			status := relaycommon.NewStreamStatus()
			status.SetEndReason(test.reason, nil)
			if test.hasSoftError {
				status.RecordError("upstream stream error")
			}
			info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: status}

			assert.Equal(t, test.channelSuccess, IsFinalRelayChannelAttemptSuccessful(info))
			assert.Equal(t, test.requestSuccess, IsFinalRelayRequestSuccessful(ctx, info))
		})
	}
}

func TestCanceledRequestContextFailsFinalRequestButNotChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestCtx)
	info := &relaycommon.RelayInfo{}

	assert.True(t, IsFinalRelayChannelAttemptSuccessful(info))
	assert.False(t, IsFinalRelayRequestSuccessful(ctx, info), "request context ended with %v", requestCtx.Err())
}
