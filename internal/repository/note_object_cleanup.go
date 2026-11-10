package repository

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Enqueue in the same transaction as the note deletion: a failed enqueue rolls it back.
func enqueueNoteObjectCleanup(ctx context.Context, tx pgx.Tx, attachments []domain.NoteAttachment) error {
	for _, a := range attachments {
		if a.ObjectName == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO note_object_cleanup(object_name) VALUES($1) ON CONFLICT DO NOTHING`, a.ObjectName); err != nil {
			return err
		}
	}
	return nil
}

// ProcessNoteObjectCleanup holds row locks so multiple replicas cannot claim the same job.
// Object removal is idempotent; a crash before commit safely retries the removal.
func (r *NoteRepository) ProcessNoteObjectCleanup(ctx context.Context, remove func(context.Context, string) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT object_name FROM note_object_cleanup WHERE next_attempt_at <= NOW() ORDER BY next_attempt_at, object_name LIMIT 20 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if remove(ctx, key) != nil {
			_, err = tx.Exec(ctx, `UPDATE note_object_cleanup SET attempts = attempts + 1, next_attempt_at = NOW() + INTERVAL '1 minute' WHERE object_name = $1`, key)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM note_object_cleanup WHERE object_name = $1`, key)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
