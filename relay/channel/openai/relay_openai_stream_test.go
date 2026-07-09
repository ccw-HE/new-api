package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newOAIStreamTestContext(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	return newOAIStreamTestContextWithModel(t, body, "gpt-test")
}

func newOAIStreamTestContextWithModel(t *testing.T, body string, model string) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: model,
		},
	}
	return c, recorder, resp, info
}

func TestOaiStreamHandlerKeepsNativeEmptyChatStreamSuccessfulByDefault(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	tests := []struct {
		name string
		body string
	}{
		{
			name: "single content null stop chunk",
			body: strings.Join([]string{
				`data: {"id":"chatcmpl-empty","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":"stop"}]}`,
				``,
				`data: [DONE]`,
				``,
			}, "\n"),
		},
		{
			name: "role chunk then stop chunk",
			body: strings.Join([]string{
				`data: {"id":"chatcmpl-empty","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
				``,
				`data: {"id":"chatcmpl-empty","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				``,
				`data: [DONE]`,
				``,
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, recorder, resp, info := newOAIStreamTestContext(t, tt.body)

			usage, err := OaiStreamHandler(c, info, resp)

			require.Nil(t, err)
			require.NotNil(t, usage)
			require.Contains(t, recorder.Body.String(), `data: [DONE]`)
		})
	}
}

func TestOaiStreamHandlerReturnsEmptyResponseBeforeWritingWhenSchedulerDetectsEmptyChatStream(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl-empty","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	c, recorder, resp, info := newOAIStreamTestContext(t, body)
	info.DetectEmptyResponseForScheduler = true

	usage, err := OaiStreamHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.Empty(t, recorder.Body.String())
}

func TestOaiStreamHandlerWrapsJSONChatResponseWhenStreamRequested(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := `{"id":"chatcmpl-json","object":"chat.completion","created":1710000000,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"mock success"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	c, recorder, resp, info := newOAIStreamTestContext(t, body)
	resp.Header.Set("Content-Type", "application/json; charset=utf-8")
	info.DetectEmptyResponseForScheduler = true

	usage, err := OaiStreamHandler(c, info, resp)

	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.TotalTokens)
	require.Contains(t, recorder.Body.String(), `"content":"mock success"`)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}

func TestOaiStreamHandlerKeepsSSEWhenContentTypeMissing(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"native stream"}}]}`,
		``,
		`data: {"id":"chatcmpl-sse","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	c, recorder, resp, info := newOAIStreamTestContext(t, body)
	resp.Header.Del("Content-Type")
	info.DetectEmptyResponseForScheduler = true

	usage, err := OaiStreamHandler(c, info, resp)

	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), `"content":"native stream"`)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}

func TestOpenaiHandlerKeepsNativeEmptyResponseSuccessfulByDefault(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := `{"id":"chatcmpl-empty","object":"chat.completion","created":1710000000,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`
	c, recorder, resp, info := newOAIStreamTestContext(t, body)

	usage, err := OpenaiHandler(c, info, resp)

	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), `"content":null`)
}

func TestOpenaiHandlerReturnsEmptyResponseWhenSchedulerDetectsEmptyResponse(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := `{"id":"chatcmpl-empty","object":"chat.completion","created":1710000000,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`
	c, recorder, resp, info := newOAIStreamTestContext(t, body)
	info.DetectEmptyResponseForScheduler = true

	usage, err := OpenaiHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.Empty(t, recorder.Body.String())
}

func TestOaiStreamHandlerDoesNotTreatToolCallAsEmptyResponse(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl-tool","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"chatcmpl-tool","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`,
		``,
		`data: {"id":"chatcmpl-tool","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	c, recorder, resp, info := newOAIStreamTestContext(t, body)
	info.DetectEmptyResponseForScheduler = true

	usage, err := OaiStreamHandler(c, info, resp)

	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), `"tool_calls"`)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}

func TestOaiStreamHandlerDoesNotTreatTextAsEmptyResponse(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl-text","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"chatcmpl-text","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		``,
		`data: {"id":"chatcmpl-text","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	c, recorder, resp, info := newOAIStreamTestContext(t, body)
	info.DetectEmptyResponseForScheduler = true

	usage, err := OaiStreamHandler(c, info, resp)

	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), `"content":"ok"`)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}

func TestOaiStreamHandlerDoesNotTreatAudioChunkAsEmptyResponse(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1710000000,"model":"gpt-audio","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1710000000,"model":"gpt-audio","choices":[{"index":0,"delta":{"audio":{"data":"abc","transcript":""}}}]}`,
		``,
		`data: {"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1710000000,"model":"gpt-audio","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	c, recorder, resp, info := newOAIStreamTestContextWithModel(t, body, "gpt-audio")
	info.DetectEmptyResponseForScheduler = true

	usage, err := OaiStreamHandler(c, info, resp)

	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), `"audio"`)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}
