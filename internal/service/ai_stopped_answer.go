package service

import (
	"context"
	"strings"

	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

// A provider output cap is a normal terminal result, not a reason to pay for
// another completion or execute a partially generated tool command.
func stoppedAIAnswer(ctx context.Context, resp *alem.ChatCompletionResponse, usage alem.Usage, sources []alem.DataSource, scope string) *alem.ChatCompletionResponse {
	if resp == nil {
		resp = &alem.ChatCompletionResponse{}
	}
	answer := ""
	if len(resp.Choices) > 0 {
		message := resp.Choices[0].Message
		if len(message.ToolCalls) == 0 && message.Role == "assistant" {
			decoded := decodeAIDataAnswer(message)
			if decoded.AnswerMode == "" {
				decoded, _ = decodeAIJSONReadStep(message, 0)
			}
			if decoded.AnswerMode == "" {
				// Recover only a cut-off answer string in an otherwise strict
				// answer envelope. Never repair or execute read/tool commands.
				if raw, ok := message.Content.(string); ok {
					raw = aiJSONEnvelopeContent(raw)
					header, body, found := strings.Cut(raw, "\n")
					if found && (strings.TrimSpace(header) == "```" || strings.EqualFold(strings.TrimSpace(header), "```json")) {
						raw = body
					}
					decoded, _ = decodeAIJSONReadStep(alem.Message{Role: "assistant", Content: strings.TrimSpace(raw) + "\"}"}, 0)
				}
			}
			if decoded.AnswerMode == "general" || (decoded.AnswerMode == "grounded" && len(sources) > 0) {
				answer, _ = decoded.Content.(string)
				if decoded.AnswerMode == "grounded" {
					answer = cleanAIUserAnswer(answer)
					if hasAIInternalText(answer, nil) {
						answer = ""
					}
				}
			}
		}
	}
	key := "ai.answer_stopped.output"
	switch scope {
	case "five_hour", "weekly", "company", "busy":
		key = "ai.answer_stopped." + scope
	}
	notice := i18n.Text(i18n.FromContext(ctx), key, nil)
	text := strings.TrimSpace(answer)
	if text != "" {
		text += "\n\n"
	}
	text += notice
	resp.Choices = []alem.Choice{{Index: 0, FinishReason: "length", Message: alem.Message{Role: "assistant", Content: text}}}
	resp.Usage = usage
	resp.DataSources = sources
	resp.DataCards = nil
	return resp
}
