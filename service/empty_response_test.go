package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasOpenAITextDeliverable(t *testing.T) {
	t.Run("nil response is empty", func(t *testing.T) {
		assert.False(t, HasOpenAITextDeliverable(nil))
	})

	t.Run("empty string content and no tools is empty", func(t *testing.T) {
		resp := &dto.OpenAITextResponse{
			Choices: []dto.OpenAITextResponseChoice{
				{Message: dto.Message{Content: ""}},
			},
		}

		assert.False(t, HasOpenAITextDeliverable(resp))
	})

	t.Run("empty content and no tools is empty", func(t *testing.T) {
		resp := &dto.OpenAITextResponse{
			Choices: []dto.OpenAITextResponseChoice{
				{Message: dto.Message{Content: []any{}}},
			},
		}

		assert.False(t, HasOpenAITextDeliverable(resp))
	})

	t.Run("null content from json and no tools is empty", func(t *testing.T) {
		var resp dto.OpenAITextResponse
		require.NoError(t, common.Unmarshal([]byte(`{
			"choices": [
				{
					"message": {
						"content": null
					}
				}
			]
		}`), &resp))

		assert.False(t, HasOpenAITextDeliverable(&resp))
	})

	t.Run("empty tool calls are empty", func(t *testing.T) {
		for _, rawToolCalls := range [][]byte{
			[]byte("null"),
			[]byte("[]"),
			[]byte("{}"),
		} {
			resp := &dto.OpenAITextResponse{
				Choices: []dto.OpenAITextResponseChoice{
					{Message: dto.Message{Content: "", ToolCalls: rawToolCalls}},
				},
			}

			assert.False(t, HasOpenAITextDeliverable(resp))
		}
	})

	t.Run("content is deliverable", func(t *testing.T) {
		resp := &dto.OpenAITextResponse{
			Choices: []dto.OpenAITextResponseChoice{
				{Message: dto.Message{Content: "ok"}},
			},
		}

		assert.True(t, HasOpenAITextDeliverable(resp))
	})

	t.Run("reasoning is deliverable", func(t *testing.T) {
		reasoning := "thinking"
		resp := &dto.OpenAITextResponse{
			Choices: []dto.OpenAITextResponseChoice{
				{Message: dto.Message{ReasoningContent: &reasoning}},
			},
		}

		assert.True(t, HasOpenAITextDeliverable(resp))
	})

	t.Run("tool call is deliverable", func(t *testing.T) {
		msg := dto.Message{}
		msg.SetToolCalls([]dto.ToolCallResponse{
			{
				ID:   "call_1",
				Type: "function",
				Function: dto.FunctionResponse{
					Name:      "search",
					Arguments: "{}",
				},
			},
		})
		resp := &dto.OpenAITextResponse{
			Choices: []dto.OpenAITextResponseChoice{{Message: msg}},
		}

		assert.True(t, HasOpenAITextDeliverable(resp))
	})
}

func TestHasOpenAIResponsesDeliverable(t *testing.T) {
	assert.False(t, HasOpenAIResponsesDeliverable(&dto.OpenAIResponsesResponse{}))

	assert.True(t, HasOpenAIResponsesDeliverable(&dto.OpenAIResponsesResponse{
		Output: []dto.ResponsesOutput{
			{
				Type: "message",
				Role: "assistant",
				Content: []dto.ResponsesOutputContent{
					{Type: "output_text", Text: "ok"},
				},
			},
		},
	}))

	assert.True(t, HasOpenAIResponsesDeliverable(&dto.OpenAIResponsesResponse{
		Output: []dto.ResponsesOutput{
			{Type: "function_call", Name: "lookup", Arguments: []byte(`{}`)},
		},
	}))
}

func TestHasGeminiChatDeliverable(t *testing.T) {
	blocked := "SAFETY"
	assert.False(t, HasGeminiChatDeliverable(&dto.GeminiChatResponse{
		PromptFeedback: &dto.GeminiChatPromptFeedback{BlockReason: &blocked},
	}))

	assert.True(t, HasGeminiChatDeliverable(&dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Parts: []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{FunctionName: "lookup"}}},
				},
			},
		},
	}))
}

func TestHasClaudeDeliverable(t *testing.T) {
	assert.False(t, HasClaudeDeliverable(&dto.ClaudeResponse{}))

	text := "ok"
	assert.True(t, HasClaudeDeliverable(&dto.ClaudeResponse{
		Content: []dto.ClaudeMediaMessage{{Type: "text", Text: &text}},
	}))

	assert.True(t, HasClaudeDeliverable(&dto.ClaudeResponse{
		Content: []dto.ClaudeMediaMessage{{Type: "tool_use", Name: "lookup"}},
	}))
}

func TestNewEmptyResponseErrorIsRetryable(t *testing.T) {
	err := NewEmptyResponseError("openai", "choices empty")
	require.NotNil(t, err)
	assert.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	assert.Equal(t, http.StatusBadGateway, err.StatusCode)
	assert.False(t, types.IsSkipRetryError(err))
}
