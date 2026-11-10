package repository

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func (r *UserRepository) CheckNotOffboarded(ctx context.Context, user int64) error {
	var offboarded bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employee_offboardings WHERE user_id=$1)`, user).Scan(&offboarded); err != nil {
		return err
	}
	if offboarded {
		return apperrors.Forbidden("employee is offboarded")
	}
	return nil
}
