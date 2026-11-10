package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type EmailLogRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewEmailLogRepository(db *pgxpool.Pool, logger *zap.Logger) *EmailLogRepository {
	return &EmailLogRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "email_log")),
	}
}

func (r *EmailLogRepository) Create(ctx context.Context, log *domain.EmailLog) error {
	log.CreatedAt = time.Now()

	query := `
		INSERT INTO email_logs (sent_email, matched_user_id, content, status, timestamp, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`

	err := r.db.QueryRow(ctx, query,
		log.SentEmail,
		log.MatchedUserID,
		log.Content,
		log.Status,
		log.Timestamp,
		log.CreatedAt,
	).Scan(&log.ID)

	if err != nil {
		r.logger.Error("Failed to create email log", zap.Error(err))
		return fmt.Errorf("create email log: %w", err)
	}

	r.logger.Info("Email log created", zap.String("id", log.ID.String()), zap.String("email", log.SentEmail))
	return nil
}

func (r *EmailLogRepository) GetAll(ctx context.Context, limit, offset int) ([]*domain.EmailLog, error) {
	query := `
		SELECT
			el.id,
			el.sent_email,
			el.matched_user_id,
			el.content,
			el.status,
			el.timestamp,
			el.created_at,
			u.email,
			u.username
		FROM email_logs el
		LEFT JOIN users u ON el.matched_user_id = u.id
		ORDER BY el.created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.Query(ctx, query, limit, offset)
	if err != nil {
		r.logger.Error("Failed to get email logs", zap.Error(err))
		return nil, fmt.Errorf("get email logs: %w", err)
	}
	defer rows.Close()

	var logs []*domain.EmailLog
	for rows.Next() {
		log := &domain.EmailLog{}
		if err := rows.Scan(
			&log.ID,
			&log.SentEmail,
			&log.MatchedUserID,
			&log.Content,
			&log.Status,
			&log.Timestamp,
			&log.CreatedAt,
			&log.MatchedUserEmail,
			&log.MatchedUserName,
		); err != nil {
			r.logger.Error("Failed to scan email log", zap.Error(err))
			continue
		}
		logs = append(logs, log)
	}

	return logs, nil
}
