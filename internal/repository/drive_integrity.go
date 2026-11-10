package repository

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrFolderCycle = errors.New("folder destination creates a cycle")

// Serialize changes to the folder tree before checking ancestry. Without this,
// simultaneous A->B and B->A requests can both pass a service-level check.
func lockFolderDestination(ctx context.Context, tx pgx.Tx, source uuid.UUID, target *uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724619203)`); err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	var exists, cycle bool
	err := tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (
 SELECT id,parent_id FROM folders WHERE id=$1 AND deleted_at IS NULL
 UNION SELECT f.id,f.parent_id FROM folders f JOIN ancestors a ON f.id=a.parent_id
 ) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$1),EXISTS(SELECT 1 FROM ancestors WHERE id=$2)`, target, source).Scan(&exists, &cycle)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("target folder not found")
	}
	if cycle {
		return ErrFolderCycle
	}
	return nil
}
