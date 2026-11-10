package apperrors

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
)

func offboardingDatabaseError(err error) *AppError {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	if pgErr.Code == "P7601" {
		return Forbidden("employee is inactive or offboarded")
	}
	if pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "55P03" || pgErr.Code == "57014" {
		return Conflict("data changed concurrently; retry the request")
	}
	return nil
}
