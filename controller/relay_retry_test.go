package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryEmptyResponseBusinessError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NotNil(t, c)

	emptyOK := types.NewOpenAIError(errors.New("empty response"), types.ErrorCodeEmptyResponse, http.StatusOK)
	assert.True(t, shouldRetry(c, emptyOK, 1))

	normalOK := types.NewOpenAIError(errors.New("ok"), types.ErrorCodeBadResponse, http.StatusOK)
	assert.False(t, shouldRetry(c, normalOK, 1))

	assert.False(t, shouldRetry(c, emptyOK, 0))
}

func TestShouldRetryStopsWhenRequestContextIsDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NotNil(t, c)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	c.Request = req.WithContext(ctx)

	err := types.NewOpenAIError(errors.New("upstream error: do request failed"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)

	assert.False(t, shouldRetry(c, err, 3))
}

func TestSchedulerFailureDispositionUsesConfiguredStatusCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orig := operation_setting.AutomaticRetryStatusCodeRanges
	t.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = orig })

	statusError := func(code int) *types.NewAPIError {
		return types.NewOpenAIError(errors.New(http.StatusText(code)), types.ErrorCodeBadResponseStatusCode, code)
	}

	tests := []struct {
		name   string
		err    *types.NewAPIError
		ranges []operation_setting.StatusCodeRange
		want   service.SchedulerFailureDisposition
	}{
		{
			name:   "configured 504 retries current channel",
			err:    statusError(http.StatusGatewayTimeout),
			ranges: []operation_setting.StatusCodeRange{{Start: 504, End: 504}},
			want:   service.SchedulerFailureRetryCurrent,
		},
		{
			name: "unconfigured 504 fails over immediately",
			err:  statusError(http.StatusGatewayTimeout),
			want: service.SchedulerFailureFailoverNow,
		},
		{
			name: "unconfigured 524 fails over immediately",
			err:  statusError(524),
			want: service.SchedulerFailureFailoverNow,
		},
		{
			name: "unconfigured 408 fails over immediately",
			err:  statusError(http.StatusRequestTimeout),
			want: service.SchedulerFailureFailoverNow,
		},
		{
			name: "unconfigured 429 fails over immediately",
			err:  statusError(http.StatusTooManyRequests),
			want: service.SchedulerFailureFailoverNow,
		},
		{
			name: "unconfigured 400 stops",
			err:  statusError(http.StatusBadRequest),
			want: service.SchedulerFailureStop,
		},
		{
			name: "transport failure changes channel without disabling",
			err: types.NewErrorWithStatusCode(
				errors.New("dial tcp: connection refused"),
				types.ErrorCodeDoRequestFailed,
				http.StatusInternalServerError,
			),
			want: service.SchedulerFailureFailoverWithoutDisable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operation_setting.AutomaticRetryStatusCodeRanges = tt.ranges
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

			assert.Equal(t, tt.want, schedulerFailureDisposition(c, tt.err, 3))
		})
	}
}

func TestSchedulerFailureDispositionStopsForTerminalConditions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiErr := types.NewOpenAIError(errors.New("upstream error"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)

	t.Run("attempts exhausted", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		assert.Equal(t, service.SchedulerFailureStop, schedulerFailureDisposition(c, apiErr, 0))
	})

	t.Run("specific channel", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Set("specific_channel_id", 1)
		assert.Equal(t, service.SchedulerFailureStop, schedulerFailureDisposition(c, apiErr, 3))
	})

	t.Run("explicit skip retry", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		skipErr := types.NewOpenAIError(
			errors.New("invalid request"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
		assert.Equal(t, service.SchedulerFailureStop, schedulerFailureDisposition(c, skipErr, 3))
	})

	t.Run("request context cancelled", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		ctx, cancel := context.WithCancel(req.Context())
		cancel()
		c.Request = req.WithContext(ctx)
		assert.Equal(t, service.SchedulerFailureStop, schedulerFailureDisposition(c, apiErr, 3))
	})
}

func TestSchedulerFailureDispositionFailsOverContentBlockWithoutDisabling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	blockedErr := types.NewOpenAIError(errors.New("upstream response blocked"), types.ErrorCodeEmptyResponse, http.StatusBadGateway)

	common.SetContextKey(c, constant.ContextKeyUpstreamContentBlocked, true)
	assert.Equal(t, service.SchedulerFailureFailoverWithoutDisable, schedulerFailureDisposition(c, blockedErr, 3))

	common.SetContextKey(c, constant.ContextKeyUpstreamContentBlocked, false)
	assert.Equal(t, service.SchedulerFailureRetryCurrent, schedulerFailureDisposition(c, blockedErr, 3))
}

func TestContentBlockNeverDisablesChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUpstreamContentBlocked, true)

	originalRanges := operation_setting.AutomaticDisableStatusCodeRanges
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: http.StatusBadGateway, End: http.StatusBadGateway}}
	t.Cleanup(func() { operation_setting.AutomaticDisableStatusCodeRanges = originalRanges })

	blockedErr := types.NewOpenAIError(errors.New("upstream response blocked"), types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	assert.False(t, shouldDisableChannelForRelay(c, blockedErr))
}

func TestResetRelayAttemptStateClearsContentBlockMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUpstreamContentBlocked, true)
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "stale block reason")

	resetRelayAttemptState(c)

	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamContentBlocked))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
}
