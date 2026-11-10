package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type WhiteboardRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewWhiteboardRepository(db *pgxpool.Pool, logger *zap.Logger) *WhiteboardRepository {
	return &WhiteboardRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "whiteboard")),
	}
}

func (r *WhiteboardRepository) GetState(ctx context.Context, roomID uuid.UUID) ([]domain.WSMessage, error) {
	var raw []byte
	err := r.db.QueryRow(ctx, `
		SELECT state
		FROM whiteboard_states
		WHERE board_id = $1
	`, roomID).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return []domain.WSMessage{}, nil
		}
		return nil, fmt.Errorf("get whiteboard state: %w", err)
	}

	if len(raw) == 0 {
		return []domain.WSMessage{}, nil
	}

	var state []domain.WSMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("unmarshal whiteboard state: %w", err)
	}

	return state, nil
}

func (r *WhiteboardRepository) SaveState(ctx context.Context, roomID uuid.UUID, state []domain.WSMessage, userID int64) error {
	return r.UpsertState(ctx, roomID, state, userID)
}

func (r *WhiteboardRepository) UpsertState(ctx context.Context, roomID uuid.UUID, state []domain.WSMessage, userID int64) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal whiteboard state: %w", err)
	}

	var actorID any
	if userID > 0 {
		actorID = userID
	}

	_, err = r.db.Exec(ctx, `
		INSERT INTO whiteboard_states (board_id, state, created_by, updated_by, created_at, updated_at)
		VALUES ($1, $2::jsonb, $3, $3, NOW(), NOW())
		ON CONFLICT (board_id) DO UPDATE
		SET state = EXCLUDED.state,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = NOW()
	`, roomID, payload, actorID)
	if err != nil {
		return fmt.Errorf("upsert whiteboard state: %w", err)
	}

	return nil
}

func (r *WhiteboardRepository) DeleteState(ctx context.Context, roomID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM whiteboard_states WHERE board_id = $1`, roomID)
	if err != nil {
		return fmt.Errorf("delete whiteboard state: %w", err)
	}
	return nil
}
