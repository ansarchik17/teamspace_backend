package agent

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type sessionService struct {
	cfg  config.AgentConfig
	repo SessionRepository
}

func NewSessionService(cfg config.AgentConfig, repo SessionRepository) SessionService {
	return &sessionService{cfg: cfg, repo: repo}
}

func (s *sessionService) GetOrCreate(ctx context.Context, user domain.AgentUserContext, requestedSessionID string, metadata map[string]interface{}) (*domain.AgentSession, error) {
	if requestedSessionID != "" {
		session, err := s.repo.GetSession(ctx, requestedSessionID, user.UserID)
		if err != nil {
			return nil, err
		}
		if session != nil {
			if metadata == nil {
				metadata = session.Metadata
			}
			if err := s.repo.TouchSession(ctx, session.ID, user.UserID, metadata, time.Now().Add(s.cfg.SessionTTL)); err != nil {
				return nil, err
			}
			session.Metadata = metadata
			session.ExpiresAt = time.Now().Add(s.cfg.SessionTTL)
			return session, nil
		}
	}

	now := time.Now()
	session := &domain.AgentSession{
		ID:        uuid.NewString(),
		UserID:    user.UserID,
		Status:    domain.AgentSessionActive,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(s.cfg.SessionTTL),
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *sessionService) Get(ctx context.Context, user domain.AgentUserContext, sessionID string) (*domain.AgentSession, error) {
	return s.repo.GetSession(ctx, sessionID, user.UserID)
}

func (s *sessionService) List(ctx context.Context, user domain.AgentUserContext, limit int) ([]domain.AgentSession, error) {
	return s.repo.ListSessions(ctx, user.UserID, limit)
}

func (s *sessionService) UpdateMetadata(ctx context.Context, user domain.AgentUserContext, sessionID string, metadata map[string]interface{}) (*domain.AgentSession, error) {
	existing, err := s.repo.GetSession(ctx, sessionID, user.UserID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	expiresAt := time.Now().Add(s.cfg.SessionTTL)
	if err := s.repo.UpdateSessionMetadata(ctx, sessionID, user.UserID, metadata, expiresAt); err != nil {
		return nil, err
	}
	existing.Metadata = metadata
	existing.UpdatedAt = time.Now()
	existing.ExpiresAt = expiresAt
	return existing, nil
}

func (s *sessionService) Close(ctx context.Context, user domain.AgentUserContext, sessionID string) error {
	return s.repo.CloseSession(ctx, sessionID, user.UserID)
}

func (s *sessionService) Delete(ctx context.Context, user domain.AgentUserContext, sessionID string) error {
	return s.repo.DeleteSession(ctx, sessionID, user.UserID)
}

func (s *sessionService) SaveMessage(ctx context.Context, message *domain.AgentMessage) error {
	return s.repo.CreateMessage(ctx, message)
}

func (s *sessionService) ListMessages(ctx context.Context, user domain.AgentUserContext, sessionID string) ([]domain.AgentMessage, error) {
	return s.repo.ListMessages(ctx, sessionID, user.UserID)
}
