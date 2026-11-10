package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Corporate identity providers are managed only through the platform API.
// Keep the former current-company entry point fail-closed for older clients.
func (r *OrganizationIdentityProviderRepository) ForCurrentCompany(ctx context.Context, actor int64) (*OrganizationIdentityProviderRepository, uuid.UUID, error) {
	return nil, uuid.Nil, apperrors.Forbidden("corporate identity providers require platform administration")
}

func (r *OrganizationIdentityProviderRepository) ensureScope(ctx context.Context, actor int64, org uuid.UUID) error {
	if r.currentOrganization == nil {
		return r.ensurePlatformScope(ctx, actor, org)
	}
	return apperrors.Forbidden("corporate identity providers require platform administration")
}

// Hold company/actor locks while Keycloak and the audited database operation run.
// Public platform methods continue to require the platform role.
func (r *OrganizationIdentityProviderRepository) WithAdministrationTransaction(ctx context.Context, actor int64, org uuid.UUID, operation func(*OrganizationIdentityProviderRepository) error, compensate func()) error {
	if err := r.ensureScope(ctx, actor, org); err != nil {
		return err
	}
	pool, ok := r.db.(*pgxpool.Pool)
	if !ok {
		return fmt.Errorf("identity provider transaction requires pool")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,17007))`, org.String()); err != nil {
		_ = conn.Conn().Close(context.Background())
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,17007))`, org.String()); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()

	var owner *int64
	var status string
	err = tx.QueryRow(ctx, `SELECT owner_id,status FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrOrganizationNotFound
	}
	if err != nil {
		return err
	}
	if r.currentOrganization != nil {
		if *r.currentOrganization != org {
			return apperrors.Forbidden("outside current company")
		}
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL)`, actor, org).Scan(&member); err != nil {
			return err
		}
		if !member && (owner == nil || *owner != actor) {
			return apperrors.Forbidden("company changed")
		}
	}
	if err := authorizeAdministrationLocked(ctx, tx, actor, owner, r.currentOrganization == nil); err != nil {
		return err
	}
	if status != "active" {
		return apperrors.Conflict("organization suspended")
	}
	clone := *r
	clone.db = administrationTx{tx}
	if err := operation(&clone); err != nil {
		if compensate != nil {
			compensate()
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if compensate != nil {
			compensate()
		}
		return err
	}
	return nil
}
