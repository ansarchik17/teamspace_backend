package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ExternalPublicationImportRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewExternalPublicationImportRepository(db *pgxpool.Pool, logger *zap.Logger) *ExternalPublicationImportRepository {
	return &ExternalPublicationImportRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "external_publication_import")),
	}
}

func (r *ExternalPublicationImportRepository) GetBySourceExternalID(
	ctx context.Context,
	sourceName, externalID string,
) (*domain.ExternalPublicationImport, error) {
	const query = `
		SELECT id, source_name, external_id, source_url, publication_id, title, checksum, imported_at, updated_at
		FROM external_publication_imports
		WHERE source_name = $1 AND external_id = $2
	`

	record := &domain.ExternalPublicationImport{}
	err := r.db.QueryRow(ctx, query, sourceName, externalID).Scan(
		&record.ID,
		&record.SourceName,
		&record.ExternalID,
		&record.SourceURL,
		&record.PublicationID,
		&record.Title,
		&record.Checksum,
		&record.ImportedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get external publication import: %w", err)
	}
	return record, nil
}

func (r *ExternalPublicationImportRepository) Upsert(
	ctx context.Context,
	record *domain.ExternalPublicationImport,
) error {
	if record == nil {
		return fmt.Errorf("external publication import is nil")
	}
	if record.ID == uuid.Nil {
		record.ID = uuid.New()
	}
	now := time.Now()
	if record.ImportedAt.IsZero() {
		record.ImportedAt = now
	}
	record.UpdatedAt = now

	const query = `
		INSERT INTO external_publication_imports (
			id, source_name, external_id, source_url, publication_id, title, checksum, imported_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (source_name, external_id)
		DO UPDATE SET
			source_url = EXCLUDED.source_url,
			publication_id = EXCLUDED.publication_id,
			title = EXCLUDED.title,
			checksum = EXCLUDED.checksum,
			updated_at = EXCLUDED.updated_at
	`

	if _, err := r.db.Exec(ctx, query,
		record.ID,
		record.SourceName,
		record.ExternalID,
		record.SourceURL,
		record.PublicationID,
		record.Title,
		record.Checksum,
		record.ImportedAt,
		record.UpdatedAt,
	); err != nil {
		return fmt.Errorf("upsert external publication import: %w", err)
	}
	return nil
}
