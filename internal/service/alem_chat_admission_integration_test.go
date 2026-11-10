package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type rejectedAIPaidCall struct {
	err    error
	cancel context.CancelFunc
}

func (r rejectedAIPaidCall) ExecutePaidCall(context.Context, domain.AIPaidCallParams, AIPaidDispatchFunc) (*AIPaidExecutionResult, error) {
	if r.cancel != nil {
		r.cancel()
	}
	return nil, r.err
}

func TestAlemAdmissionRejectionDoesNotPersistUnsentTurn(t *testing.T) {
	for _, rejection := range []error{
		domain.ErrAIConcurrencyLimitExceeded,
		domain.ErrAIRateLimitExceeded,
		domain.ErrAILimiterUnavailable,
		&domain.AIReservationDenied{Reason: "RESERVATION_TOO_LARGE", Scope: "five_hour"},
		&domain.AIReservationDenied{Reason: "RESERVATION_PENDING", Scope: "account"},
	} {
		t.Run(rejection.Error(), func(t *testing.T) {
			ctx, db, _, org := newAIOperationServiceDB(t)
			_, err := db.Exec(ctx, `CREATE TABLE ai_conversations(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id BIGINT,title TEXT NOT NULL DEFAULT 'Chat',model_name TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE ai_messages(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id UUID REFERENCES ai_conversations(id) ON DELETE CASCADE,role TEXT,content TEXT,file_url TEXT,attachment_name TEXT,attachment_mime_type TEXT,extracted_text TEXT,tokens_usage INT NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),data_cards JSONB NOT NULL DEFAULT '[]',data_sources JSONB NOT NULL DEFAULT '[]');`)
			require.NoError(t, err)
			repo := repository.NewAIRepository(db, zap.NewNop())
			chat, err := repo.CreateConversation(ctx, 1, DefaultAlemModel, "Existing chat")
			require.NoError(t, err)
			previousUser, err := repo.CreateMessageWithID(ctx, repository.AIMessage{ConversationID: chat, Role: "user", Content: "previous accepted question"})
			require.NoError(t, err)
			previousAssistant, err := repo.CreateMessageWithID(ctx, repository.AIMessage{ConversationID: chat, Role: "assistant", Content: "previous accepted answer"})
			require.NoError(t, err)
			for range 2 {
				callCtx, cancel := context.WithCancel(ctx)
				s := &AlemService{repo: repo, logger: zap.NewNop(), defaultModel: DefaultAlemModel}
				s.SetPaidAccounting(rejectedAIPaidCall{err: rejection, cancel: cancel}, alemPayerStub{user: &domain.User{ID: 1, OrganizationID: &org, IsActive: true}})
				_, err = s.SendMessage(callCtx, chat, 1, "unsent repeat", nil, "")
				require.ErrorIs(t, err, rejection)
				history, err := repo.GetMessages(ctx, chat)
				require.NoError(t, err)
				require.Len(t, history, 2, "a cancelled request context must not prevent exact-turn rollback")
			}
			// Scope checks forbid deleting another user's turn or an assistant answer.
			require.NoError(t, repo.DeleteUnacceptedUserMessage(ctx, chat, previousUser, 99))
			require.NoError(t, repo.DeleteUnacceptedUserMessage(ctx, uuid.NewString(), previousUser, 1))
			require.NoError(t, repo.DeleteUnacceptedUserMessage(ctx, chat, previousAssistant, 1))
			history, err := repo.GetMessages(ctx, chat)
			require.NoError(t, err)
			require.Len(t, history, 2)
		})
	}
}
