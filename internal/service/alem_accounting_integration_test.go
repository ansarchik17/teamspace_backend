package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type alemAccountingReader struct{ fakePersonalReader }

func (r *alemAccountingReader) Catalog() []AIDataDataset {
	catalog := r.fakePersonalReader.Catalog()
	for i := range catalog {
		if catalog[i].Key == "tasks" {
			catalog[i].CardViews = []AIDataCardView{{ItemsPaths: []string{""}, Title: []string{"title"}, Service: "tasks"}}
		}
	}
	return catalog
}

func TestAlemChatAccountingIntegration(t *testing.T) {
	for _, scenario := range []string{"tools", "first_failure", "last_failure", "json_fallback", "model_switch", "model_fallback", "publication", "publication_failure", "title", "output_limit"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, accounting, org := newAIOperationServiceDB(t)
			_, err := db.Exec(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=10000000000,max_five_hour_microcredits=10000000000;
INSERT INTO users(id,organization_id) SELECT 42,id FROM organizations;
INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) SELECT id,42,10000000000,10000000000 FROM organizations;
CREATE TABLE ai_conversations(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id BIGINT,title TEXT NOT NULL DEFAULT 'Chat',model_name TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE ai_messages(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id UUID REFERENCES ai_conversations(id) ON DELETE CASCADE,role TEXT,content TEXT,file_url TEXT,attachment_name TEXT,attachment_mime_type TEXT,extracted_text TEXT,tokens_usage INT NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),data_cards JSONB NOT NULL DEFAULT '[]',data_sources JSONB NOT NULL DEFAULT '[]');`)
			require.NoError(t, err)
			chatID := uuid.NewString()
			_, err = db.Exec(ctx, `INSERT INTO ai_conversations(id,user_id,model_name) VALUES($1,42,$2)`, chatID, DefaultAlemModel)
			require.NoError(t, err)
			_, err = db.Exec(ctx, `INSERT INTO ai_messages(conversation_id,role,content,extracted_text,created_at) VALUES($1,'user','earlier question','document context',now()-interval '3 minutes'),($1,'assistant','earlier answer',NULL,now()-interval '2 minutes')`, chatID)
			require.NoError(t, err)
			requests := []alem.ChatCompletionRequest{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req alem.ChatCompletionRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					w.WriteHeader(400)
					return
				}
				requests = append(requests, req)
				step := len(requests)
				w.Header().Set("Content-Type", "application/json")
				if (scenario == "last_failure" && step == 2) || scenario == "first_failure" {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"message":"temporarily unavailable"}}`)
					return
				}
				if scenario == "publication_failure" && step == 3 {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"message":"temporarily unavailable"}}`)
					return
				}
				if scenario == "model_fallback" && step == 1 {
					w.WriteHeader(403)
					fmt.Fprint(w, `{"error":{"message":"model access denied"}}`)
					return
				}
				msg := alem.Message{Role: "assistant", Content: `{"answer_mode":"grounded","answer":"Проверено.","card_ids":["S1-C1"]}`}
				if scenario == "output_limit" {
					msg.Content = `{"answer_mode":"general","answer":"Сохранённая часть`
				} else if scenario == "title" {
					msg.Content = "Короткое название"
				} else if scenario == "model_fallback" {
					msg.Content = `{"answer_mode":"general","answer":"Готово.","card_ids":[]}`
				} else if step == 1 {
					if scenario == "json_fallback" {
						msg.Content = "draft without protocol"
					} else {
						msg.Content = nil
						msg.ToolCalls = []alem.ToolCall{personalTool("read-1", `{"dataset":"tasks","params":{"mine":"true"}}`)}
					}
				} else if scenario == "json_fallback" && step == 2 {
					msg.Content = `{"action":"read","queries":[{"dataset":"tasks","params":{"mine":"true"}}]}`
				} else if scenario == "json_fallback" {
					msg.Content = `{"action":"answer","answer_mode":"grounded","answer":"Проверено.","card_ids":["S1-C1"]}`
				}
				if scenario == "publication" || scenario == "publication_failure" {
					if step == 2 {
						msg.Content = `{"answer_mode":"grounded","answer":"read_user_data","card_ids":["S1-C1"]}`
					}
					if step == 3 {
						msg.Content = `{"answer":"Проверено.","card_ids":["S1-C1"]}`
					}
				}
				finishReason, output := "stop", 1
				if scenario == "output_limit" {
					finishReason, output = "length", req.MaxTokens
				}
				json.NewEncoder(w).Encode(alem.ChatCompletionResponse{ID: fmt.Sprintf("provider-%d", step), Model: req.Model, Choices: []alem.Choice{{Message: msg, FinishReason: finishReason}}, Usage: alem.Usage{PromptTokens: 2, CompletionTokens: output, TotalTokens: 2 + output}})
			}))
			defer provider.Close()
			original := alem.BaseURL
			alem.BaseURL = provider.URL
			defer func() { alem.BaseURL = original }()
			s := &AlemService{client: alem.NewClient("synthetic-test-key", 5*time.Second, 3), repo: repository.NewAIRepository(db, zap.NewNop()), logger: zap.NewNop(), defaultModel: DefaultAlemModel, userData: &alemAccountingReader{}}
			s.SetPaidAccounting(accounting, alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}})
			newModel := ""
			if scenario == "model_switch" || scenario == "model_fallback" {
				newModel = "zai-org/GLM-4.6"
			}
			if scenario == "title" {
				paidCtx, e := s.paidUserContext(ctx, 42)
				require.NoError(t, e)
				s.GenerateTitle(paidCtx, chatID, uuid.NewString(), "question")
			} else {
				_, err = s.SendMessage(WithAIDataIdentity(ctx, AIDataIdentity{UserID: 42, ChatID: chatID}), chatID, 42, "current question", nil, newModel)
				if scenario == "last_failure" || scenario == "first_failure" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			}
			wantCalls, settled, unknown, used := 2, 2, 0, int64(10000)
			switch scenario {
			case "first_failure":
				wantCalls, settled, unknown, used = 1, 0, 1, 0
			case "last_failure":
				settled, unknown, used = 1, 1, 5000
			case "json_fallback":
				wantCalls, settled, used = 3, 3, 15000
			case "model_switch":
				used = 20000
			case "publication":
				wantCalls, settled, used = 3, 3, 15000
			case "publication_failure":
				wantCalls, settled, unknown, used = 3, 2, 1, 10000
			case "model_fallback":
				settled, unknown, used = 1, 1, 5000
			case "title":
				wantCalls, settled, used = 1, 1, 5000
			case "output_limit":
				wantCalls, settled, used = 1, 1, 18_002_000
				var saved string
				require.NoError(t, db.QueryRow(ctx, `SELECT content FROM ai_messages WHERE conversation_id=$1 AND role='assistant' ORDER BY created_at DESC LIMIT 1`, chatID).Scan(&saved))
				require.Contains(t, saved, "Сохранённая часть")
				require.Contains(t, saved, "Ответ остановлен")
			}
			require.Len(t, requests, wantCalls, "hidden retries or unaccounted extra steps")
			var count, completed, pending int
			var charged, held int64
			require.NoError(t, db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='settled'),count(*) FILTER(WHERE status='unknown'),coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&count, &completed, &pending, &charged))
			require.Equal(t, wantCalls, count)
			require.Equal(t, settled, completed)
			require.Equal(t, unknown, pending)
			require.Equal(t, used, charged)
			require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&charged, &held))
			require.Equal(t, used, charged)
			if unknown > 0 {
				require.Positive(t, held)
			} else {
				require.Zero(t, held)
			}
			if scenario == "model_switch" {
				for _, r := range requests {
					require.Equal(t, newModel, r.Model)
				}
			}
			if scenario != "title" {
				var savedTurns int
				require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_messages WHERE conversation_id=$1 AND role='user' AND content='current question'`, chatID).Scan(&savedTurns))
				require.Equal(t, 1, savedTurns, "dispatched or paid turns must remain in history, including unknown outcomes")
				wire, _ := json.Marshal(requests[0])
				require.Contains(t, string(wire), "earlier question")
				require.Contains(t, string(wire), "earlier answer")
				require.Contains(t, string(wire), "current question")
			}
			if scenario == "tools" {
				wire, _ := json.Marshal(requests[1])
				require.True(t, strings.Contains(string(wire), `"role":"tool"`))
			}
			// Chat history is not the accounting ledger.
			require.NoError(t, s.DeleteChat(ctx, chatID, 42))
			require.NoError(t, db.QueryRow(ctx, `SELECT coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&charged))
			require.Equal(t, used, charged)
		})
	}
}
