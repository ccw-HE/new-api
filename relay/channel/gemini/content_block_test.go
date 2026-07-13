package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiHandlersPromptBlockReturnsGenericRetryableError(t *testing.T) {
	tests := []struct {
		name   string
		handle func(*gin.Context, *relaycommon.RelayInfo, *http.Response) *types.NewAPIError
	}{
		{
			name: "translated response",
			handle: func(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) *types.NewAPIError {
				_, apiErr := GeminiChatHandler(c, info, resp)
				return apiErr
			},
		},
		{
			name: "native response",
			handle: func(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) *types.NewAPIError {
				_, apiErr := GeminiTextGenerationHandler(c, info, resp)
				return apiErr
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"promptFeedback":{"blockReason":"SAFETY"}}`))}
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatGemini}

			apiErr := tt.handle(c, info, resp)

			require.NotNil(t, apiErr)
			require.Equal(t, types.ErrorCodeEmptyResponse, apiErr.GetErrorCode())
			require.NotContains(t, apiErr.Error(), "SAFETY")
			require.Equal(t, "gemini_block_reason=SAFETY", common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
			require.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamContentBlocked))
			require.Equal(t, 0, recorder.Body.Len())
		})
	}
}
