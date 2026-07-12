package openai

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

func TestOpenaiHandlerContentFilterReturnsGenericRetryableError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}]}`,
		)),
	}
	info := &relaycommon.RelayInfo{
		DetectEmptyResponseForScheduler: true,
		ChannelMeta:                     &relaycommon.ChannelMeta{},
	}

	usage, apiErr := OpenaiHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeEmptyResponse, apiErr.GetErrorCode())
	require.NotContains(t, apiErr.Error(), "content_filter")
	require.Equal(t, "openai_finish_reason=content_filter", common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamContentBlocked))
	require.Equal(t, 0, recorder.Body.Len())
}
