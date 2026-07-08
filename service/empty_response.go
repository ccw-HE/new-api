package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

func HasOpenAITextDeliverable(resp *dto.OpenAITextResponse) bool {
	if resp == nil || len(resp.Choices) == 0 {
		return false
	}
	for _, choice := range resp.Choices {
		if strings.TrimSpace(choice.Message.StringContent()) != "" {
			return true
		}
		if strings.TrimSpace(choice.Message.GetReasoningContent()) != "" {
			return true
		}
		if rawJSONHasPayload(choice.Message.ToolCalls) {
			return true
		}
	}
	return false
}

func HasOpenAIResponsesDeliverable(resp *dto.OpenAIResponsesResponse) bool {
	if resp == nil || len(resp.Output) == 0 {
		return false
	}
	for _, output := range resp.Output {
		switch output.Type {
		case "message":
			for _, content := range output.Content {
				if strings.TrimSpace(content.Text) != "" {
					return true
				}
			}
		case "function_call", dto.ResponsesOutputTypeImageGenerationCall:
			return true
		default:
			if strings.HasSuffix(output.Type, "_call") || strings.Contains(output.Type, "tool") {
				return true
			}
		}
	}
	return false
}

func HasGeminiChatDeliverable(resp *dto.GeminiChatResponse) bool {
	if resp == nil || len(resp.Candidates) == 0 {
		return false
	}
	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				return true
			}
			if part.FunctionCall != nil {
				return true
			}
			if part.InlineData != nil {
				return true
			}
			if part.ExecutableCode != nil && strings.TrimSpace(part.ExecutableCode.Code) != "" {
				return true
			}
			if part.CodeExecutionResult != nil && (strings.TrimSpace(part.CodeExecutionResult.Output) != "" || strings.TrimSpace(part.CodeExecutionResult.Outcome) != "") {
				return true
			}
		}
	}
	return false
}

func HasClaudeDeliverable(resp *dto.ClaudeResponse) bool {
	if resp == nil {
		return false
	}
	if strings.TrimSpace(resp.Completion) != "" {
		return true
	}
	for _, content := range resp.Content {
		if strings.TrimSpace(content.GetText()) != "" {
			return true
		}
		if content.Thinking != nil && strings.TrimSpace(*content.Thinking) != "" {
			return true
		}
		if content.Type == "tool_use" {
			return true
		}
	}
	return false
}

func NewEmptyResponseError(provider string, reason string) *types.NewAPIError {
	message := "empty response"
	if provider != "" {
		message = fmt.Sprintf("empty response from %s", provider)
	}
	if reason != "" {
		message = fmt.Sprintf("%s: %s", message, reason)
	}
	return types.NewOpenAIError(errors.New(message), types.ErrorCodeEmptyResponse, http.StatusBadGateway)
}

func rawJSONHasPayload(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return trimmed != "" && trimmed != "null" && trimmed != "[]" && trimmed != "{}"
}
