package apperrors

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http"
)

const ErrCodeDriveQuotaExceeded ErrorCode = "DRIVE_QUOTA_EXCEEDED"

func DriveQuotaExceeded(details map[string]any) *AppError {
	return New(ErrCodeDriveQuotaExceeded, "Drive storage quota exceeded", http.StatusInsufficientStorage).
		WithUserMessage("Недостаточно свободного места в Drive. Превышена квота хранилища.").WithDetails(details)
}

// Match only the SQLSTATE codes owned by the Drive quota migration. Wrapping a
// database error must not turn a quota rejection into a generic server error.
func driveStorageDatabaseError(err error) *AppError {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	if pgErr.Code != "P7501" && pgErr.Code != "P7502" && pgErr.Code != "P7503" {
		return nil
	}
	details := map[string]any{}
	_ = json.Unmarshal([]byte(pgErr.Detail), &details)
	switch pgErr.Code {
	case "P7501":
		return DriveQuotaExceeded(details)
	case "P7502":
		return New(ErrCodeFileTooBig, "Drive upload limit exceeded", http.StatusRequestEntityTooLarge).WithUserMessage("Файл превышает допустимый размер загрузки.").WithDetails(details)
	default:
		// The company quota trigger also uses P7503 for an exhausted
		// allocation budget. Retrying cannot fix this deterministic rejection.
		if pgErr.Message == "Company storage allocations exceed budget" {
			return New("COMPANY_BUDGET_BELOW_USAGE", "company quota is below requested allocations", http.StatusConflict)
		}
		return Conflict("Drive storage changed concurrently; retry the request").WithUserMessage("Хранилище изменилось параллельно. Повторите запрос.")
	}
}

func DriveUploadTooBig(limit int64) *AppError {
	err := FileTooBig(limit)
	err.StatusCode = http.StatusRequestEntityTooLarge
	return err
}
