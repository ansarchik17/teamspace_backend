package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type AgentRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewAgentRepository(db *pgxpool.Pool, logger *zap.Logger) *AgentRepository {
	return &AgentRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "agent")),
	}
}

func (r *AgentRepository) CreateSession(ctx context.Context, session *domain.AgentSession) error {
	if session.ID == "" {
		session.ID = uuid.NewString()
	}
	metadata, _ := json.Marshal(session.Metadata)
	_, err := r.db.Exec(ctx, `
		INSERT INTO agent_sessions (id, user_id, status, metadata_json, created_at, updated_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, session.ID, session.UserID, session.Status, metadata, session.CreatedAt, session.UpdatedAt, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create agent session: %w", err)
	}
	return nil
}

func (r *AgentRepository) GetSession(ctx context.Context, sessionID string, userID int64) (*domain.AgentSession, error) {
	var (
		session  domain.AgentSession
		metadata []byte
	)
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, status, metadata_json, created_at, updated_at, expires_at
		FROM agent_sessions
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID).Scan(
		&session.ID, &session.UserID, &session.Status, &metadata, &session.CreatedAt, &session.UpdatedAt, &session.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent session: %w", err)
	}
	if len(metadata) > 0 {
		_ = json.Unmarshal(metadata, &session.Metadata)
	}
	return &session, nil
}

func (r *AgentRepository) ListSessions(ctx context.Context, userID int64, limit int) ([]domain.AgentSession, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, status, metadata_json, created_at, updated_at, expires_at
		FROM agent_sessions
		WHERE user_id = $1
		  AND status = $2
		ORDER BY updated_at DESC
		LIMIT $3
	`, userID, domain.AgentSessionActive, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]domain.AgentSession, 0, limit)
	for rows.Next() {
		var (
			session  domain.AgentSession
			metadata []byte
		)
		if err := rows.Scan(
			&session.ID, &session.UserID, &session.Status, &metadata, &session.CreatedAt, &session.UpdatedAt, &session.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent session: %w", err)
		}
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &session.Metadata)
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (r *AgentRepository) TouchSession(ctx context.Context, sessionID string, userID int64, metadata map[string]interface{}, expiresAt time.Time) error {
	payload, _ := json.Marshal(metadata)
	_, err := r.db.Exec(ctx, `
		UPDATE agent_sessions
		SET metadata_json = $3, updated_at = NOW(), expires_at = $4
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID, payload, expiresAt)
	if err != nil {
		return fmt.Errorf("touch agent session: %w", err)
	}
	return nil
}

func (r *AgentRepository) UpdateSessionMetadata(ctx context.Context, sessionID string, userID int64, metadata map[string]interface{}, expiresAt time.Time) error {
	payload, _ := json.Marshal(metadata)
	_, err := r.db.Exec(ctx, `
		UPDATE agent_sessions
		SET metadata_json = $3, updated_at = NOW(), expires_at = $4
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID, payload, expiresAt)
	if err != nil {
		return fmt.Errorf("update agent session metadata: %w", err)
	}
	return nil
}

func (r *AgentRepository) CloseSession(ctx context.Context, sessionID string, userID int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE agent_sessions
		SET status = $3, updated_at = NOW()
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID, domain.AgentSessionClosed)
	if err != nil {
		return fmt.Errorf("close agent session: %w", err)
	}
	return nil
}

func (r *AgentRepository) DeleteSession(ctx context.Context, sessionID string, userID int64) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM agent_sessions
		WHERE id = $1 AND user_id = $2
	`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("delete agent session: %w", err)
	}
	return nil
}

func (r *AgentRepository) CreateMessage(ctx context.Context, message *domain.AgentMessage) error {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	entities, _ := json.Marshal(message.Entities)
	actions, _ := json.Marshal(message.Actions)
	metadata, _ := json.Marshal(message.Metadata)
	_, err := r.db.Exec(ctx, `
		INSERT INTO agent_messages (id, session_id, user_id, role, content, intent, entities_json, actions_json, metadata_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, message.ID, message.SessionID, message.UserID, message.Role, message.Content, message.Intent, entities, actions, metadata, message.CreatedAt)
	if err != nil {
		return fmt.Errorf("create agent message: %w", err)
	}
	return nil
}

func (r *AgentRepository) ListMessages(ctx context.Context, sessionID string, userID int64) ([]domain.AgentMessage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, session_id, user_id, role, content, intent, entities_json, actions_json, metadata_json, created_at
		FROM agent_messages
		WHERE session_id = $1 AND user_id = $2
		ORDER BY created_at ASC
	`, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("list agent messages: %w", err)
	}
	defer rows.Close()

	var messages []domain.AgentMessage
	for rows.Next() {
		var (
			message  domain.AgentMessage
			entities []byte
			actions  []byte
			metadata []byte
		)
		if err := rows.Scan(
			&message.ID, &message.SessionID, &message.UserID, &message.Role, &message.Content, &message.Intent,
			&entities, &actions, &metadata, &message.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent message: %w", err)
		}
		if len(entities) > 0 {
			_ = json.Unmarshal(entities, &message.Entities)
		}
		if len(actions) > 0 {
			_ = json.Unmarshal(actions, &message.Actions)
		}
		if len(metadata) > 0 {
			_ = json.Unmarshal(metadata, &message.Metadata)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (r *AgentRepository) CreateConfirmation(ctx context.Context, confirmation *domain.AgentActionConfirmation) error {
	if confirmation.ID == "" {
		confirmation.ID = uuid.NewString()
	}
	payload, _ := json.Marshal(confirmation.Payload)
	_, err := r.db.Exec(ctx, `
		INSERT INTO agent_action_confirmations (id, session_id, user_id, token_hash, action_type, payload_json, status, expires_at, executed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, confirmation.ID, confirmation.SessionID, confirmation.UserID, confirmation.TokenHash, confirmation.ActionType, payload, confirmation.Status, confirmation.ExpiresAt, confirmation.ExecutedAt, confirmation.CreatedAt)
	if err != nil {
		return fmt.Errorf("create agent confirmation: %w", err)
	}
	return nil
}

func (r *AgentRepository) GetConfirmationByHash(ctx context.Context, sessionID string, userID int64, tokenHash string) (*domain.AgentActionConfirmation, error) {
	var (
		item    domain.AgentActionConfirmation
		payload []byte
	)
	err := r.db.QueryRow(ctx, `
		SELECT id, session_id, user_id, token_hash, action_type, payload_json, status, expires_at, executed_at, created_at
		FROM agent_action_confirmations
		WHERE session_id = $1 AND user_id = $2 AND token_hash = $3
	`, sessionID, userID, tokenHash).Scan(
		&item.ID, &item.SessionID, &item.UserID, &item.TokenHash, &item.ActionType, &payload, &item.Status, &item.ExpiresAt, &item.ExecutedAt, &item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent confirmation: %w", err)
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &item.Payload)
	}
	return &item, nil
}

func (r *AgentRepository) MarkConfirmationExecuted(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE agent_action_confirmations
		SET status = $2, executed_at = NOW()
		WHERE id = $1
	`, id, domain.AgentConfirmationExecuted)
	if err != nil {
		return fmt.Errorf("mark confirmation executed: %w", err)
	}
	return nil
}

func (r *AgentRepository) SaveFeedback(ctx context.Context, feedback *domain.AgentFeedback) error {
	if feedback.ID == "" {
		feedback.ID = uuid.NewString()
	}
	metadata, _ := json.Marshal(feedback.Metadata)
	_, err := r.db.Exec(ctx, `
		INSERT INTO agent_feedback (id, user_id, session_id, message_id, rating, comment, metadata_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, feedback.ID, feedback.UserID, feedback.SessionID, feedback.MessageID, feedback.Rating, feedback.Comment, metadata, feedback.CreatedAt)
	if err != nil {
		return fmt.Errorf("save agent feedback: %w", err)
	}
	return nil
}

func (r *AgentRepository) SaveAuditLog(ctx context.Context, entry *domain.AgentAuditLog) error {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	payload, _ := json.Marshal(entry.Payload)
	_, err := r.db.Exec(ctx, `
		INSERT INTO agent_audit_logs (id, user_id, session_id, event_type, payload_json, ip, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, entry.ID, entry.UserID, entry.SessionID, entry.EventType, payload, entry.IP, entry.UserAgent, entry.CreatedAt)
	if err != nil {
		return fmt.Errorf("save agent audit log: %w", err)
	}
	return nil
}
