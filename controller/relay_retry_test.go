package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
