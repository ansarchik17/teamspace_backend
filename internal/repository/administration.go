package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type administrationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func requireActiveAdministrationActor(ctx context.Context, db administrationQuerier, actor int64) error {
	var active bool
	err := db.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1 AND deleted_at IS NULL`, actor).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!active || actor <= 0)) {
		return apperrors.Forbidden("administrator required")
	}
	return err
}

// Read permissions from the database so revocation does not depend on JWT expiry.
func authorizeAdministration(ctx context.Context, db administrationQuerier, actor int64, owner *int64, platformOnly bool) error {
	return authorizeAdministrationQuery(ctx, db, actor, owner, platformOnly, false)
}

// Call inside a mutation transaction; the shared row lock prevents a concurrent
// role revocation or account deactivation from overtaking the mutation.
func authorizeAdministrationLocked(ctx context.Context, db administrationQuerier, actor int64, owner *int64, platformOnly bool) error {
	return authorizeAdministrationQuery(ctx, db, actor, owner, platformOnly, true)
}

func authorizeAdministrationQuery(ctx context.Context, db administrationQuerier, actor int64, owner *int64, platformOnly, lock bool) error {
	var role domain.Role
	var active bool
	query := `SELECT role,is_active FROM users WHERE id=$1 AND deleted_at IS NULL`
	if lock {
		query += ` FOR SHARE`
	}
	err := db.QueryRow(ctx, query, actor).Scan(&role, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.Forbidden("administrator required")
	}
	if err != nil {
		return err
	}
	if !domain.AdministrationAllowed(role, active, actor, owner, platformOnly) {
		return apperrors.Forbidden("administrator required")
	}
	return nil
}
