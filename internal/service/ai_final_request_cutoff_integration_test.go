package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestAlemFinalQuotaTurnAcceptedAndConsumesRemainder(t *testing.T) {
	for _, scenario := range []string{"five_hour", "weekly", "no_company", "capped_output", "kk", "99_percent", "75_percent"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, accounting, org := newAIOperationServiceDB(t)
			_, err := db.Exec(ctx, `CREATE TABLE organization_user_blocks(user_id BIGINT);
CREATE TABLE ai_conversations(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id BIGINT,title TEXT NOT NULL DEFAULT 'Chat',model_name TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE ai_messages(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id UUID REFERENCES ai_conversations(id) ON DELETE CASCADE,role TEXT,content TEXT,file_url TEXT,attachment_name TEXT,attachment_mime_type TEXT,extracted_text TEXT,tokens_usage INT NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),data_cards JSONB NOT NULL DEFAULT '[]',data_sources JSONB NOT NULL DEFAULT '[]');`)
			require.NoError(t, err)
			uid := int64(1)
			callerOrg := org
			var userOrg *uuid.UUID = &org
			q, h := int64(1000000), int64(100000)
			scope := "five_hour"
			if scenario == "weekly" {
				q = 100000
				scope = "weekly"
			}
			if scenario == "capped_output" {
				q, h = 20000000, 2000000
			}
			if scenario == "no_company" {
				callerOrg = uuid.Nil
				userOrg = nil
				_, err = db.Exec(ctx, `UPDATE users SET organization_id=NULL WHERE id=1;INSERT INTO user_ai_personal_quotas(user_id,weekly_quota_microcredits,five_hour_limit_microcredits) VALUES(1,1000000,100000)`)
				require.NoError(t, err)
			} else {
				_, err = db.Exec(ctx, `UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=$1,five_hour_limit_microcredits=$2`, q, h)
				require.NoError(t, err)
			}
			var providerCharged int64
			if scenario != "capped_output" {
				caller, e := domain.NewAIPaidUserCaller(callerOrg, uid)
				require.NoError(t, e)
				req := serviceAIOperationRequest(callerOrg, "verified-earlier-turn")
				req.UserID = caller.UserID
				charge := int64(95000)
				if scenario == "99_percent" {
					charge = 99000
				}
				if scenario == "75_percent" {
					charge = 75000
				}
				req.Microcredits = charge
				store := repository.NewAIReservationRepository(db, nil)
				seed, e := store.Reserve(ctx, req)
				require.NoError(t, e)
				input, output := charge/1000, int64(0)
				_, e = store.SettleSuccess(ctx, domain.AISettleRequest{OrganizationID: callerOrg, AttemptID: seed.AttemptID, ActualModel: DefaultAlemModel, UsageAdapterVersion: "test-v1", UsageSource: "provider_usage", Usage: domain.AICreditUsage{Status: "verified", InputTokens: &input, OutputTokens: &output}})
				require.NoError(t, e)
				providerCharged = charge
			}
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request alem.ChatCompletionRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, "capped_output", scenario, "unaffordable input must not be dispatched")
				require.Less(t, request.MaxTokens, 1000)
				providerCharged = 2000 + int64(request.MaxTokens)*3000
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(alem.ChatCompletionResponse{ID: "paid-partial", Model: request.Model, Choices: []alem.Choice{{FinishReason: "length", Message: alem.Message{Role: "assistant", Content: "Сохранённая часть ответа"}}}, Usage: alem.Usage{PromptTokens: 2, CompletionTokens: request.MaxTokens, TotalTokens: 2 + request.MaxTokens}})
			}))
			defer provider.Close()
			before := alem.BaseURL
			alem.BaseURL = provider.URL
			defer func() { alem.BaseURL = before }()
			repo := repository.NewAIRepository(db, zap.NewNop())
			chat, e := repo.CreateConversation(ctx, uid, DefaultAlemModel, "Existing chat")
			require.NoError(t, e)
			service := &AlemService{client: alem.NewClient("synthetic-local-key", time.Second, 0), repo: repo, logger: zap.NewNop(), defaultModel: DefaultAlemModel}
			service.SetPaidAccounting(accounting, alemPayerStub{user: &domain.User{ID: uid, OrganizationID: userOrg, IsActive: true}})
			if scenario == "kk" {
				ctx = i18n.WithLocale(ctx, i18n.Kazakh)
			}
			response, e := service.SendMessage(ctx, chat, uid, "Обработай запрос", nil, "")
			require.NoError(t, e)
			require.NotNil(t, response.QuotaStop)
			require.Equal(t, "quota_exhausted", response.Choices[0].FinishReason)
			require.Equal(t, scope, response.QuotaStop.Scope)
			if scenario == "kk" {
				require.Contains(t, response.Choices[0].Message.Content, "таусылды")
			} else {
				require.Contains(t, response.Choices[0].Message.Content, "лимит исчерпан")
			}
			if scenario == "capped_output" {
				require.EqualValues(t, 1, calls.Load())
				require.Contains(t, response.Choices[0].Message.Content, "Сохранённая часть")
			} else {
				require.Zero(t, calls.Load())
			}
			debit, e := strconv.ParseInt(response.QuotaStop.ConsumedMicrocredits, 10, 64)
			require.NoError(t, e)
			require.Equal(t, h-providerCharged, debit)
			limits, e := repository.NewOrganizationRepository(db, zap.NewNop()).GetUserAILimits(ctx, uid)
			require.NoError(t, e)
			require.Zero(t, *limits.FiveHour.RemainingPercent)
			require.Zero(t, *limits.FiveHour.Available)
			require.False(t, limits.Access.CanStartRequest)
			if scenario == "weekly" {
				require.Zero(t, *limits.Weekly.RemainingPercent)
			}
			var actual int64
			require.NoError(t, db.QueryRow(ctx, `SELECT COALESCE(sum(charged_microcredits),0)::bigint FROM ai_usage_attempts`).Scan(&actual))
			require.Equal(t, providerCharged, actual, "provider expense must not include the quota cutoff debit")
			history, e := repo.GetMessages(ctx, chat)
			require.NoError(t, e)
			require.Len(t, history, 2)
			require.Equal(t, "user", history[0].Role)
			require.Equal(t, "assistant", history[1].Role)
			require.Equal(t, "Обработай запрос", history[0].Content)
			require.False(t, strings.Contains(history[1].Content, "RESERVATION_TOO_LARGE"))
		})
	}
}
