package apperrors

import "net/http"

const ErrCodeUserIdentityConflict ErrorCode = "USER_IDENTITY_CONFLICT"

func UserIdentityConflict() *AppError {
	return New(ErrCodeUserIdentityConflict, "user identity already exists", http.StatusConflict)
}
