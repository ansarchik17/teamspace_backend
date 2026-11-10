package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	legacyservice "github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestService_ChatLowConfidenceAsksClarification(t *testing.T) {
	repo := newMemoryRepo()
	svc := newTestService(t, repo, nil)

	resp, err := svc.Chat(context.Background(), domain.AgentUserContext{UserID: 1}, &domain.AgentChatRequest{
		Message: "ага",
	})

	require.Nil(t, err)
	require.Contains(t, resp.Answer, "уточнение")
	require.NotNil(t, resp.Intent)
}

func TestService_ChatRequiresConfirmation(t *testing.T) {
	repo := newMemoryRepo()
	svc := newTestService(t, repo, nil)

	resp, err := svc.Chat(context.Background(), domain.AgentUserContext{UserID: 1}, &domain.AgentChatRequest{
		Message: "Создай напоминание завтра в 09:00",
	})

	require.Nil(t, err)
	require.True(t, resp.RequiresConfirmation)
	require.NotEmpty(t, resp.ConfirmationToken)
	require.Equal(t, "create_reminder", resp.ProposedAction.Type)
}

func TestService_ChatMyTasksUsesUserAwareIntentWithoutConfirmation(t *testing.T) {
	repo := newMemoryRepo()
	svc := newTestService(t, repo, nil)

	resp, err := svc.Chat(context.Background(), domain.AgentUserContext{UserID: 1}, &domain.AgentChatRequest{
		Message:  "Покажи мои задачи на сегодня",
		Timezone: "Asia/Almaty",
	})

	require.Nil(t, err)
	require.NotNil(t, resp.Intent)
	require.Equal(t, domain.AgentIntentGetTasks, resp.Intent.Name)
	require.False(t, resp.RequiresConfirmation)
	require.Equal(t, "today", resp.Entities["period"])
}

func TestService_ExecuteActionRejectsExpiredToken(t *testing.T) {
	repo := newMemoryRepo()
	svc := newTestService(t, repo, nil)
	user := domain.AgentUserContext{UserID: 1}
	session, err := svc.CreateSession(context.Background(), user, nil)
	require.NoError(t, err)
	token := "token-1"
	repo.confirmations[hashToken(token)] = &domain.AgentActionConfirmation{
		ID:         "c1",
		SessionID:  session.ID,
		UserID:     1,
		TokenHash:  hashToken(token),
		ActionType: "start_call",
		Payload:    map[string]interface{}{"target": "Alex"},
		Status:     domain.AgentConfirmationPending,
		ExpiresAt:  time.Now().Add(-time.Minute),
		CreatedAt:  time.Now().Add(-2 * time.Minute),
	}

	resp, appErr := svc.ExecuteAction(context.Background(), user, session.ID, token)

	require.Nil(t, resp)
	require.NotNil(t, appErr)
	require.Contains(t, appErr.Message, "expired")
}

func TestService_ProcessLegacyDelegates(t *testing.T) {
	legacy := &legacyStub{
		resp: &legacyservice.SmartAgentChatResponse{Reply: "legacy-ok", SessionID: "s1"},
	}
	svc := newTestService(t, newMemoryRepo(), legacy)

	resp, err := svc.ProcessLegacy(context.Background(), 10, &legacyservice.SmartAgentChatRequest{Message: "hi"})

	require.NoError(t, err)
	require.Equal(t, "legacy-ok", resp.Reply)
	require.True(t, legacy.called)
}

func newTestService(t *testing.T, repo *memoryRepo, legacy LegacyChatProcessor) *Service {
	t.Helper()
	if legacy == nil {
		legacy = &legacyStub{resp: &legacyservice.SmartAgentChatResponse{Reply: "ok"}}
	}
	tools := BuildDefaultTools(ToolDependencies{})
	return NewService(
		config.AgentConfig{
			Enabled:                   true,
			V2Enabled:                 true,
			IntentConfidenceThreshold: 0.75,
			SessionTTL:                time.Hour,
			ConfirmationTTL:           time.Minute,
			ExternalTimeout:           time.Second,
		},
		zap.NewNop(),
		repo,
		NewKeywordIntentResolver(0.75),
		NewSimpleEntityExtractor(),
		NewPermissionChecker(),
		NewToolRegistry(tools...),
		NewResponseBuilder(),
		NewAuditLogger(repo),
		legacy,
	)
}

type legacyStub struct {
	called bool
	resp   *legacyservice.SmartAgentChatResponse
	err    error
}

func (l *legacyStub) ProcessChat(_ context.Context, _ int64, _ *legacyservice.SmartAgentChatRequest) (*legacyservice.SmartAgentChatResponse, error) {
	l.called = true
	if l.err != nil {
		return nil, l.err
	}
	return l.resp, nil
}

type memoryRepo struct {
	sessions      map[string]*domain.AgentSession
	messages      []*domain.AgentMessage
	confirmations map[string]*domain.AgentActionConfirmation
	feedback      []*domain.AgentFeedback
	audits        []*domain.AgentAuditLog
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{
		sessions:      map[string]*domain.AgentSession{},
		confirmations: map[string]*domain.AgentActionConfirmation{},
	}
}

func (r *memoryRepo) CreateSession(_ context.Context, session *domain.AgentSession) error {
	r.sessions[session.ID] = session
	return nil
}

func (r *memoryRepo) GetSession(_ context.Context, sessionID string, userID int64) (*domain.AgentSession, error) {
	session := r.sessions[sessionID]
	if session == nil || session.UserID != userID {
		return nil, nil
	}
	return session, nil
}

func (r *memoryRepo) ListSessions(_ context.Context, userID int64, limit int) ([]domain.AgentSession, error) {
	out := make([]domain.AgentSession, 0, limit)
	for _, session := range r.sessions {
		if session.UserID == userID {
			out = append(out, *session)
		}
	}
	return out, nil
}

func (r *memoryRepo) TouchSession(_ context.Context, sessionID string, userID int64, metadata map[string]interface{}, expiresAt time.Time) error {
	session := r.sessions[sessionID]
	if session == nil || session.UserID != userID {
		return errors.New("session not found")
	}
	session.Metadata = metadata
	session.ExpiresAt = expiresAt
	return nil
}

func (r *memoryRepo) UpdateSessionMetadata(_ context.Context, sessionID string, userID int64, metadata map[string]interface{}, expiresAt time.Time) error {
	return r.TouchSession(context.Background(), sessionID, userID, metadata, expiresAt)
}

func (r *memoryRepo) CloseSession(_ context.Context, sessionID string, userID int64) error {
	session := r.sessions[sessionID]
	if session == nil || session.UserID != userID {
		return nil
	}
	session.Status = domain.AgentSessionClosed
	return nil
}

func (r *memoryRepo) DeleteSession(_ context.Context, sessionID string, userID int64) error {
	session := r.sessions[sessionID]
	if session == nil || session.UserID != userID {
		return nil
	}
	delete(r.sessions, sessionID)
	return nil
}

func (r *memoryRepo) CreateMessage(_ context.Context, message *domain.AgentMessage) error {
	r.messages = append(r.messages, message)
	return nil
}

func (r *memoryRepo) ListMessages(_ context.Context, sessionID string, userID int64) ([]domain.AgentMessage, error) {
	var out []domain.AgentMessage
	for _, item := range r.messages {
		if item.SessionID == sessionID && item.UserID == userID {
			out = append(out, *item)
		}
	}
	return out, nil
}

func (r *memoryRepo) CreateConfirmation(_ context.Context, confirmation *domain.AgentActionConfirmation) error {
	r.confirmations[confirmation.TokenHash] = confirmation
	return nil
}

func (r *memoryRepo) GetConfirmationByHash(_ context.Context, sessionID string, userID int64, tokenHash string) (*domain.AgentActionConfirmation, error) {
	item := r.confirmations[tokenHash]
	if item == nil || item.SessionID != sessionID || item.UserID != userID {
		return nil, nil
	}
	return item, nil
}

func (r *memoryRepo) MarkConfirmationExecuted(_ context.Context, id string) error {
	for _, item := range r.confirmations {
		if item.ID == id {
			item.Status = domain.AgentConfirmationExecuted
			now := time.Now()
			item.ExecutedAt = &now
			return nil
		}
	}
	return nil
}

func (r *memoryRepo) SaveFeedback(_ context.Context, feedback *domain.AgentFeedback) error {
	r.feedback = append(r.feedback, feedback)
	return nil
}

func (r *memoryRepo) SaveAuditLog(_ context.Context, entry *domain.AgentAuditLog) error {
	r.audits = append(r.audits, entry)
	return nil
}
