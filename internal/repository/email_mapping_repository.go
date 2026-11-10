package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type EmailMappingRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewEmailMappingRepository(db *pgxpool.Pool, logger *zap.Logger) *EmailMappingRepository {
	return &EmailMappingRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "email_mapping")),
	}
}

// GetByIncomingEmail finds an active email mapping by incoming email
func (r *EmailMappingRepository) GetByIncomingEmail(ctx context.Context, incomingEmail string) (*domain.EmailMapping, error) {
	incomingEmail = strings.ToLower(strings.TrimSpace(incomingEmail))

	query := `
		SELECT id, incoming_email, target_email, user_id, is_active, created_at, updated_at
		FROM email_mappings
		WHERE LOWER(incoming_email) = $1 AND is_active = TRUE
		LIMIT 1
	`

	var mapping domain.EmailMapping
	err := r.db.QueryRow(ctx, query, incomingEmail).Scan(
		&mapping.ID,
		&mapping.IncomingEmail,
		&mapping.TargetEmail,
		&mapping.UserID,
		&mapping.IsActive,
		&mapping.CreatedAt,
		&mapping.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // No mapping found
		}
		r.logger.Error("Failed to get email mapping", zap.Error(err), zap.String("incoming_email", incomingEmail))
		return nil, fmt.Errorf("get email mapping: %w", err)
	}

	return &mapping, nil
}

// Create creates a new email mapping
func (r *EmailMappingRepository) Create(ctx context.Context, mapping *domain.EmailMapping) error {
	mapping.IncomingEmail = strings.ToLower(strings.TrimSpace(mapping.IncomingEmail))
	mapping.TargetEmail = strings.ToLower(strings.TrimSpace(mapping.TargetEmail))

	query := `
		INSERT INTO email_mappings (incoming_email, target_email, user_id, is_active)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRow(ctx, query,
		mapping.IncomingEmail,
		mapping.TargetEmail,
		mapping.UserID,
		mapping.IsActive,
	).Scan(&mapping.ID, &mapping.CreatedAt, &mapping.UpdatedAt)

	if err != nil {
		r.logger.Error("Failed to create email mapping", zap.Error(err))
		return fmt.Errorf("create email mapping: %w", err)
	}

	return nil
}

// GetAll retrieves all email mappings
func (r *EmailMappingRepository) GetAll(ctx context.Context, limit, offset int) ([]*domain.EmailMapping, error) {
	query := `
		SELECT id, incoming_email, target_email, user_id, is_active, created_at, updated_at
		FROM email_mappings
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.Query(ctx, query, limit, offset)
	if err != nil {
		r.logger.Error("Failed to get email mappings", zap.Error(err))
		return nil, fmt.Errorf("get email mappings: %w", err)
	}
	defer rows.Close()

	var mappings []*domain.EmailMapping
	for rows.Next() {
		var m domain.EmailMapping
		if err := rows.Scan(
			&m.ID,
			&m.IncomingEmail,
			&m.TargetEmail,
			&m.UserID,
			&m.IsActive,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			r.logger.Error("Failed to scan email mapping", zap.Error(err))
			continue
		}
		mappings = append(mappings, &m)
	}

	return mappings, nil
}

// Delete deletes an email mapping by ID
func (r *EmailMappingRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM email_mappings WHERE id = $1`
	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to delete email mapping", zap.Error(err))
		return fmt.Errorf("delete email mapping: %w", err)
	}
	return nil
}
