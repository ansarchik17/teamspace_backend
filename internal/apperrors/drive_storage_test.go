package apperrors

import (
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/west6ide/workspace-backend/internal/i18n"
)

func TestDriveStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		code    string
		status  int
		appCode ErrorCode
	}{{"P7501", 507, ErrCodeDriveQuotaExceeded}, {"P7502", 413, ErrCodeFileTooBig}, {"P7503", 409, ErrCodeConflict}} {
		err := fmt.Errorf("copy failed: %w", &pgconn.PgError{Code: tc.code, Detail: `{"quota_bytes":100}`})
		result := ToAppError(InternalWithErr("wrapped failure", err))
		require.Equal(t, tc.status, result.StatusCode)
		require.Equal(t, tc.appCode, result.Code)
		require.NotEmpty(t, result.UserMessage)
	}
	require.Equal(t, 413, DriveUploadTooBig(10).StatusCode)
	require.Equal(t, 500, ToAppError(&pgconn.PgError{Code: "23505", Detail: "private database detail"}).StatusCode)
}

func TestCompanyAllocationFailureIsNotReportedAsConcurrentChange(t *testing.T) {
	err := fmt.Errorf("create user: %w", &pgconn.PgError{
		Code: "P7503", Message: "Company storage allocations exceed budget",
	})
	result := ToAppError(InternalWithErr("Failed to sync local user", err))
	require.Equal(t, 409, result.StatusCode)
	require.Equal(t, ErrorCode("COMPANY_BUDGET_BELOW_USAGE"), result.Code)
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		body := ResponseBodyLocale(result, locale)
		require.Equal(t, ErrorCode("COMPANY_BUDGET_BELOW_USAGE"), body.Error.Code)
		require.NotEqual(t, LocalizedMessage(ErrCodeConflict, 409, locale), body.Error.Message)
		require.NotContains(t, body.Error.Message, "P7503")
	}
}
