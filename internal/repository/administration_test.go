package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type administrationTestDB struct {
	role   domain.Role
	active bool
	err    error
}

func (db *administrationTestDB) QueryRow(context.Context, string, ...any) pgx.Row { return db }
func (db *administrationTestDB) Scan(dest ...any) error {
	if db.err != nil {
		return db.err
	}
	if len(dest) == 1 {
		*dest[0].(*bool) = db.active
	} else {
		*dest[0].(*domain.Role) = db.role
		*dest[1].(*bool) = db.active
	}
	return nil
}

func TestAdministrationRevocationAndFailures(t *testing.T) {
	ctx := context.Background()
	db := &administrationTestDB{role: domain.RoleSuperAdmin, active: true}
	require.NoError(t, authorizeAdministration(ctx, db, 1, nil, true))
	db.role = domain.Role("user")
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(authorizeAdministration(ctx, db, 1, nil, true)).Code)
	db.role, db.active = domain.RoleSuperAdmin, false
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(authorizeAdministration(ctx, db, 1, nil, true)).Code)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(requireActiveAdministrationActor(ctx, db, 1)).Code)
	db.err = pgx.ErrNoRows
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(authorizeAdministration(ctx, db, 1, nil, true)).Code)
	db.err = errors.New("database unavailable")
	require.ErrorIs(t, authorizeAdministration(ctx, db, 1, nil, true), db.err)
}
