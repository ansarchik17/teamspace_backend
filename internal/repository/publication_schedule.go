package repository

import (
	"context"

	"github.com/google/uuid"
)

func (r *PublicationRepository) PublishDue(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `UPDATE publications SET status='published', updated_at=NOW() WHERE status='scheduled' AND scheduled_at <= NOW() AND published_at <= NOW() AND deleted_at IS NULL`)
	return err
}

// PublishDueWithIDs returns newly published items so the scheduler can send alerts.
func (r *PublicationRepository) PublishDueWithIDs(ctx context.Context) ([]uuid.UUID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE publications SET status='published', updated_at=NOW() WHERE status='scheduled' AND scheduled_at <= NOW() AND published_at <= NOW() AND deleted_at IS NULL RETURNING id, author_id, importance`)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	type pendingAlert struct {
		id       uuid.UUID
		authorID int64
	}
	var alerts []pendingAlert
	for rows.Next() {
		var id uuid.UUID
		var authorID int64
		var importance string
		if err := rows.Scan(&id, &authorID, &importance); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		if importance == "important" || importance == "urgent" {
			alerts = append(alerts, pendingAlert{id, authorID})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, alert := range alerts {
		if _, err := tx.Exec(ctx, `INSERT INTO publication_broadcast_queue (publication_id,triggered_by) VALUES ($1,$2) ON CONFLICT DO NOTHING`, alert.id, alert.authorID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

// ArchiveDue archives all publications whose archive_at timestamp has passed.
func (r *PublicationRepository) ArchiveDue(ctx context.Context) error {
	_, err := r.ArchiveDueWithIDs(ctx)
	return err
}

// ArchiveDueWithIDs finds and archives all publications whose archive_at time has arrived.
func (r *PublicationRepository) ArchiveDueWithIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE publications
		SET status = 'archived',
		    archived_at = NOW(),
		    is_pinned = false,
		    is_banner = false,
		    updated_at = NOW()
		WHERE status = 'published'
		  AND archive_at IS NOT NULL
		  AND archive_at <= NOW()
		  AND deleted_at IS NULL
		RETURNING id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
