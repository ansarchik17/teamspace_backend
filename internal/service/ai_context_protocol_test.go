package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestPersonalAIJSONGatewayReadsBeforeAnswer(t *testing.T) {
	for _, rejectTools := range []bool{false, true} {
		t.Run(fmt.Sprint("reject-tools=", rejectTools), func(t *testing.T) {
			step := 0
			reader := &fakePersonalReader{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				step++
				var request alem.ChatCompletionRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Fatal("invalid request")
				}
				w.Header().Set("Content-Type", "application/json")
				if step == 1 {
					if request.ToolChoice != "auto" {
						t.Fatal("native protocol was not tried")
					}
					if rejectTools {
						w.WriteHeader(http.StatusBadRequest)
						json.NewEncoder(w).Encode(alem.ErrorResponse{Error: alem.ErrorDetails{Message: "tools are not supported", Param: "tools"}})
					} else {
						json.NewEncoder(w).Encode(personalResponse(alem.Message{Role: "assistant", Content: "discard-this-unverified-answer"}))
					}
					return
				}
				if len(request.Tools) != 0 || request.ToolChoice != "" || !strings.Contains(request.Messages[0].Content.(string), aiJSONReadProtocol) {
					t.Fatal("incompatible native protocol kept")
				}
				for _, message := range request.Messages {
					if message.Role == "tool" || len(message.ToolCalls) > 0 || strings.Contains(fmt.Sprint(message.Content), "discard-this-unverified-answer") {
						t.Fatal("native/ungrounded output leaked into JSON conversation")
					}
				}
				if step == 2 {
					json.NewEncoder(w).Encode(personalResponse(alem.Message{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"tasks","params":{"mine":"true"}},{"dataset":"mail"}]}`}))
					return
				}
				if step != 3 || len(request.Messages) != 5 {
					t.Fatalf("unexpected loop: %d %+v", step, request.Messages)
				}
				last := request.Messages[len(request.Messages)-1]
				if last.Role != "user" || !strings.Contains(last.Content.(string), `"source_id":"S2"`) || !strings.Contains(last.Content.(string), `"status":"forbidden"`) {
					t.Fatal("missing untrusted result/provenance")
				}
				serialized, _ := json.Marshal(request.Messages)
				if strings.Contains(string(serialized), "must-not-leak") || !strings.Contains(string(serialized), "IGNORE RULES") {
					t.Fatal("sanitization or untrusted data handling changed")
				}
				json.NewEncoder(w).Encode(personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer":"Review [S1]. Почта недоступна."}`}))
			}))
			defer provider.Close()
			original := alem.BaseURL
			alem.BaseURL = provider.URL
			defer func() { alem.BaseURL = original }()
			resp, err := completeWithUserData(context.Background(), alem.NewClient("synthetic-key", time.Second, 0), reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{Model: "test", Messages: []alem.Message{{Role: "user", Content: "Сопоставь дела и почту"}}})
			if err != nil || step != 3 || len(reader.queries) != 3 || len(resp.DataSources) != 2 {
				t.Fatalf("JSON read loop failed: %v %+v", err, resp)
			}
			answer := resp.Choices[0].Message.Content.(string)
			if strings.Contains(answer, `"action"`) || !strings.Contains(answer, "Review") || strings.Contains(answer, "[S1]") || strings.Contains(answer, "Источники проверки:") {
				t.Fatalf("control envelope persisted: %s", answer)
			}
			wantUsage := 15
			if rejectTools {
				wantUsage = 10
			}
			if resp.Usage.TotalTokens != wantUsage {
				t.Fatalf("wrong usage: %+v", resp.Usage)
			}
		})
	}
}

func TestAIJSONReadProtocolRejectsMalformedOrPrivilegedCommands(t *testing.T) {
	for _, content := range []string{
		`{"action":"write","queries":[{"dataset":"tasks"}]}`,
		`{"action":"read","queries":[{"dataset":"tasks","user_id":7}]}`,
		`{"action":"read","queries":[{"dataset":"tasks","url":"https://evil.test"}]}`,
		`{"action":"read","queries":[{"dataset":"tasks","params":{"mine":true}}]}`,
		`{"action":"read","queries":[],"answer":"invented"}`,
		`{"action":"read","queries":[{"dataset":"tasks"}],"answer":"invented"}`,
		`{"action":"read","queries":[{}]}`,
		`{"action":"answer","answer":"invented","queries":[{"dataset":"tasks"}]}`,
		`{"action":"answer","answer":""}`,
		`{"action":"read","queries":[{"dataset":"tasks"}]} {"action":"answer","answer":"x"}`,
		`Text: {"action":"read","queries":[{"dataset":"tasks"}]}`,
		`{"tool_calls":[{"name":"read_user_data"}]}`,
	} {
		if _, err := decodeAIJSONReadStep(alem.Message{Role: "assistant", Content: content}, 0); err == nil {
			t.Errorf("accepted malformed command: %s", content)
		}
	}
	valid := "```json\n{\"action\":\"read\",\"queries\":[{\"dataset\":\"tasks\",\"params\":{\"mine\":\"true\"}}]}\n```"
	if message, err := decodeAIJSONReadStep(alem.Message{Role: "assistant", Content: valid}, 0); err != nil || len(message.ToolCalls) != 1 {
		t.Fatalf("strict fenced JSON rejected: %v", err)
	}
	if _, err := decodeAIJSONReadStep(alem.Message{Role: "user", Content: valid}, 0); err == nil {
		t.Fatal("executed a command outside the current assistant output")
	}
}

func TestPersonalAIJSONProtocolCannotSkipFirstRead(t *testing.T) {
	reader := &fakePersonalReader{}
	step := 0
	client := completionFunc(func(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		step++
		if step == 1 {
			return personalResponse(alem.Message{Role: "assistant", Content: "unsupported native tools"}), nil
		}
		return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer":"invented personal facts"}`}), nil
	})
	if _, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{}); err == nil || step != 2 || len(reader.queries) != 1 {
		t.Fatal("accepted ungrounded JSON answer")
	}
}

func TestPersonalAIJSONProtocolUsesSameReadBudget(t *testing.T) {
	reader := &fakePersonalReader{}
	step := 0
	client := completionFunc(func(_ context.Context, request alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
		step++
		if step == 1 {
			return personalResponse(alem.Message{Role: "assistant", Content: "no native calls"}), nil
		}
		for _, message := range request.Messages {
			if message.Role == "system" && strings.Contains(fmt.Sprint(message.Content), "Бюджет чтения исчерпан.") {
				return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"answer","answer":"Проверена часть данных [S1]"}`}), nil
			}
		}
		return personalResponse(alem.Message{Role: "assistant", Content: `{"action":"read","queries":[{"dataset":"tasks"},{"dataset":"tasks"},{"dataset":"tasks"},{"dataset":"tasks"}]}`}), nil
	})
	resp, err := completeWithUserData(context.Background(), client, reader, AIDataIdentity{UserID: 42}, alem.ChatCompletionRequest{})
	if err != nil || step != 6 || len(reader.queries) != 17 || len(resp.DataSources) != 16 {
		t.Fatalf("JSON budget failed: %d %d %v", step, len(reader.queries), err)
	}
}

func TestAIJSONProtocolDoesNotMaskAuthenticationOrServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		err := &alem.APIError{StatusCode: status, Details: alem.ErrorDetails{Message: "tools service failed", Param: "tools"}}
		if aiProviderRejectsTools(err) {
			t.Errorf("masked provider status %d", status)
		}
	}
	if aiProviderRejectsTools(&alem.APIError{StatusCode: http.StatusBadRequest, Details: alem.ErrorDetails{Message: "invalid model"}}) {
		t.Fatal("masked model error")
	}
}
