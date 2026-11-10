package apperrors

const (
	ErrCodeInvitationUnavailable           ErrorCode = "INVITATION_UNAVAILABLE"
	ErrCodeInvitationCodeRequired          ErrorCode = "INVITATION_CODE_REQUIRED"
	ErrCodeInvitationCodeInvalid           ErrorCode = "INVITATION_CODE_INVALID"
	ErrCodeInvitationAlreadyExists         ErrorCode = "INVITATION_ALREADY_EXISTS"
	ErrCodeInvitationDifferentOrganization ErrorCode = "INVITATION_DIFFERENT_ORGANIZATION"
	ErrCodeInvitationIdentityConflict      ErrorCode = "INVITATION_IDENTITY_CONFLICT"
	ErrCodeInvitationRegistrationRequired  ErrorCode = "INVITATION_REGISTRATION_REQUIRED"
	ErrCodeInvitationPasswordPolicy        ErrorCode = "INVITATION_PASSWORD_POLICY"
)

func InvitationIdentityConflict() *AppError {
	return New(ErrCodeInvitationIdentityConflict, "invitation identity already exists", 409)
}
