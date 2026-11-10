package agent

import (
	"context"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type repoAuditLogger struct {
	repo SessionRepository
}

func NewAuditLogger(repo SessionRepository) AuditLogger {
	return &repoAuditLogger{repo: repo}
}

func (l *repoAuditLogger) Log(ctx context.Context, user domain.AgentUserContext, sessionID, eventType string, payload map[string]interface{}) error {
	return l.repo.SaveAuditLog(ctx, &domain.AgentAuditLog{
		UserID:    user.UserID,
		SessionID: sessionID,
		EventType: eventType,
		Payload:   payload,
		IP:        user.IP,
		UserAgent: user.UserAgent,
		CreatedAt: time.Now(),
	})
}
