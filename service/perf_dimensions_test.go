package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinalRelayStreamOutcomeClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		reason          relaycommon.StreamEndReason
		hasSoftError    bool
		requireTerminal bool
		channelSuccess  bool
		requestSuccess  bool
	}{
		{name: "done", reason: relaycommon.StreamEndReasonDone, channelSuccess: true, requestSuccess: true},
		{name: "eof", reason: relaycommon.StreamEndReasonEOF, channelSuccess: true, requestSuccess: true},
		{name: "required terminal missing", reason: relaycommon.StreamEndReasonEOF, requireTerminal: true, channelSuccess: false, requestSuccess: false},
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
			if test.requireTerminal {
				status.RequireTerminal()
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

func TestAppendStreamStatusRequiresProtocolTerminal(t *testing.T) {
	for _, tt := range []struct {
		name            string
		requireTerminal bool
		want            string
	}{
		{name: "legacy eof", want: "ok"},
		{name: "missing required terminal", requireTerminal: true, want: "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status := relaycommon.NewStreamStatus()
			status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
			if tt.requireTerminal {
				status.RequireTerminal()
			}
			other := model.NewLogOther()

			appendStreamStatus(&relaycommon.RelayInfo{IsStream: true, StreamStatus: status}, other)

			streamStatus, ok := other.Snapshot()["stream_status"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.want, streamStatus["status"])
			assert.Equal(t, string(relaycommon.StreamEndReasonEOF), streamStatus["end_reason"])
		})
	}
}
