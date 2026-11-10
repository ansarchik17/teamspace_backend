package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestAlemNewRequestWorksAfterOldOutcomeIsParkedForManualAccounting(t *testing.T) {
	ctx, db, _, org := newAIOperationServiceDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE organization_ai_budgets SET weekly_budget_microcredits=1000000000,max_five_hour_microcredits=1000000000;
UPDATE organization_ai_user_quotas SET weekly_quota_microcredits=1000000000,five_hour_limit_microcredits=1000000000;
INSERT INTO users(id,organization_id) SELECT 42,organization_id FROM users WHERE id=1;
INSERT INTO organization_ai_user_quotas(organization_id,user_id,weekly_quota_microcredits,five_hour_limit_microcredits) SELECT organization_id,42,weekly_quota_microcredits,five_hour_limit_microcredits FROM organization_ai_user_quotas WHERE user_id=1;
CREATE TABLE ai_conversations(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id BIGINT,title TEXT NOT NULL DEFAULT 'Chat',model_name TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE ai_messages(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id UUID REFERENCES ai_conversations(id) ON DELETE CASCADE,role TEXT,content TEXT,file_url TEXT,attachment_name TEXT,attachment_mime_type TEXT,extracted_text TEXT,tokens_usage INT NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),data_cards JSONB NOT NULL DEFAULT '[]',data_sources JSONB NOT NULL DEFAULT '[]');`)
	require.NoError(t, err)
	chatRepo := repository.NewAIRepository(db, zap.NewNop())
	chatID, err := chatRepo.CreateConversation(ctx, 42, DefaultAlemModel, "Existing chat")
	require.NoError(t, err)
	require.NoError(t, chatRepo.CreateMessage(ctx, repository.AIMessage{ConversationID: chatID, Role: "user", Content: "earlier question"}))
	require.NoError(t, chatRepo.CreateMessage(ctx, repository.AIMessage{ConversationID: chatID, Role: "assistant", Content: "earlier answer"}))
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"message":"temporarily unavailable"}}`)
			return
		}
		var request alem.ChatCompletionRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(alem.ChatCompletionResponse{ID: "confirmed-new-answer", Model: request.Model,
			Choices: []alem.Choice{{Message: alem.Message{Role: "assistant", Content: `{"answer_mode":"general","answer":"Новый ответ получен","card_ids":[]}`}, FinishReason: "stop"}},
			Usage:   alem.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}})
	}))
	defer provider.Close()
	previousURL := alem.BaseURL
	alem.BaseURL = provider.URL
	defer func() { alem.BaseURL = previousURL }()
	store := repository.NewAIReservationRepository(db, nil, repository.WithAIUserConcurrencyLimit(1))
	limiter := NewAIGenerationLimiterWithStore(NewMemorySharedLimiterStore(), zap.NewNop(), config.AILimiterConfig{
		Enabled: true, MaxConcurrentPerUser: 1, RateLimitRequests: 100, RateLimitWindow: time.Minute,
		LeaseTTL: 10 * time.Second, HeartbeatInterval: 2 * time.Second, FailClosed: true,
	})
	s := &AlemService{client: alem.NewClient("synthetic-test-key", 5*time.Second, 3), repo: chatRepo, logger: zap.NewNop(), defaultModel: DefaultAlemModel, userData: &alemAccountingReader{}}
	s.SetPaidAccounting(NewAIPaidCallerService(store, WithAILimiter(limiter)), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}})
	callCtx := WithAIDataIdentity(ctx, AIDataIdentity{UserID: 42, ChatID: chatID})
	_, err = s.SendMessage(callCtx, chatID, 42, "failed original", nil, "")
	require.Error(t, err)
	_, err = s.SendMessage(callCtx, chatID, 42, "rejected while uncertain", nil, "")
	var pending *domain.AIReservationDenied
	require.ErrorAs(t, err, &pending)
	require.Equal(t, "account", pending.Scope)
	require.EqualValues(t, 1, calls.Load(), "no automatic paid retry")
	_, err = db.Exec(ctx, `UPDATE ai_usage_reservations SET reconcile_after=created_at WHERE status='active'`)
	require.NoError(t, err)
	worker, err := NewAIReconciliationWorker(store, aiReconciliationTestConfig(), nil, zap.NewNop())
	require.NoError(t, err)
	stats, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Manual)
	response, err := s.SendMessage(callCtx, chatID, 42, "new explicit question", nil, "")
	require.NoError(t, err, "a manual accounting case must not disable the assistant")
	require.Equal(t, "Новый ответ получен", response.Choices[0].Message.Content)
	require.EqualValues(t, 2, calls.Load())
	var unknown, settled, rejectedTurns int
	var charged, held int64
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='unknown'),count(*) FILTER (WHERE status='settled'),coalesce(sum(charged_microcredits),0) FROM ai_usage_attempts`).Scan(&unknown, &settled, &charged))
	require.Equal(t, 1, unknown)
	require.Equal(t, 1, settled)
	require.EqualValues(t, 5_000, charged)
	require.NoError(t, db.QueryRow(ctx, `SELECT reserved_microcredits FROM organization_ai_weekly_periods WHERE organization_id=$1`, org).Scan(&held))
	require.Positive(t, held, "the old unknown cost remains reserved")
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM ai_messages WHERE content='rejected while uncertain'`).Scan(&rejectedTurns))
	require.Zero(t, rejectedTurns)
}
