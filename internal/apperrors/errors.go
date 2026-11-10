package apperrors

import (
	"errors"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"net/http"
)

// ErrorCode represents application error codes
type ErrorCode string

const (
	// General errors
	ErrCodeInternal                    ErrorCode = "INTERNAL_ERROR"
	ErrCodeValidation                  ErrorCode = "VALIDATION_ERROR"
	ErrCodeAccessDependencies          ErrorCode = "ACCESS_DEPENDENCIES_MISSING"
	ErrCodeAccessPermissionUnavailable ErrorCode = "ACCESS_PERMISSION_UNAVAILABLE"
	ErrCodeNotFound                    ErrorCode = "NOT_FOUND"
	ErrCodeUnauthorized                ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden                   ErrorCode = "FORBIDDEN"
	ErrCodeConflict                    ErrorCode = "CONFLICT"
	ErrCodeODOEventConflict            ErrorCode = "ODO_EVENT_CONFLICT"
	ErrCodeODOCommandConflict          ErrorCode = "ODO_COMMAND_CONFLICT"
	ErrCodeODOUnavailable              ErrorCode = "ODO_UNAVAILABLE"
	ErrCodeODOInvalidReceipt           ErrorCode = "ODO_INVALID_RECEIPT"
	ErrCodeODOReauthRequired           ErrorCode = "ODO_REAUTH_REQUIRED"
	ErrCodeODOBackfillChanged          ErrorCode = "ODO_BACKFILL_CHANGED"
	ErrCodeODORevisionConflict         ErrorCode = "ODO_REVISION_CONFLICT"
	ErrCodeODOLegacyConflict           ErrorCode = "ODO_LEGACY_CONFLICT"
	ErrCodeBadRequest                  ErrorCode = "BAD_REQUEST"
	ErrCodeTooManyReqs                 ErrorCode = "TOO_MANY_REQUESTS"

	// Auth errors
	ErrCodeInvalidCredentials   ErrorCode = "INVALID_CREDENTIALS"
	ErrCodeTokenExpired         ErrorCode = "TOKEN_EXPIRED"
	ErrCodeTokenInvalid         ErrorCode = "TOKEN_INVALID"
	ErrCodeUserExists           ErrorCode = "USER_EXISTS"
	ErrCodeWeakPassword         ErrorCode = "WEAK_PASSWORD"
	ErrCodeQRLoginRequired      ErrorCode = "QR_LOGIN_REQUIRED"
	ErrCodeInvalidPIN           ErrorCode = "INVALID_PIN"
	ErrCodeReauthRequired       ErrorCode = "REAUTH_REQUIRED"
	ErrCodeRefreshFailed        ErrorCode = "REFRESH_FAILED"
	ErrCodeEgovAccessDenied     ErrorCode = "EGOV_ACCESS_DENIED"
	ErrCodeEgovInvalidCallback  ErrorCode = "EGOV_INVALID_CALLBACK"
	ErrCodeEgovInvalidLang      ErrorCode = "EGOV_INVALID_LANG"
	ErrCodeEgovInvalidState     ErrorCode = "EGOV_INVALID_STATE"
	ErrCodeEgovStateExpired     ErrorCode = "EGOV_STATE_EXPIRED"
	ErrCodeEgovCodeExchange     ErrorCode = "EGOV_CODE_EXCHANGE_FAILED"
	ErrCodeEgovInvalidClient    ErrorCode = "EGOV_INVALID_CLIENT"
	ErrCodeEgovInvalidGrant     ErrorCode = "EGOV_INVALID_GRANT"
	ErrCodeEgovInvalidToken     ErrorCode = "EGOV_INVALID_TOKEN"
	ErrCodeEgovRedirectMismatch ErrorCode = "EGOV_REDIRECT_MISMATCH"
	ErrCodeEgovScope            ErrorCode = "EGOV_INSUFFICIENT_SCOPE"
	ErrCodeEgovProfileFetch     ErrorCode = "EGOV_PROFILE_FETCH_FAILED"
	ErrCodeEgovIINMissing       ErrorCode = "EGOV_IIN_MISSING"
	ErrCodeEgovUserConflict     ErrorCode = "EGOV_USER_CONFLICT"
	ErrCodeEgovLegalUnsupported ErrorCode = "EGOV_LEGAL_NOT_SUPPORTED"
	ErrCodeEgovUnavailable      ErrorCode = "EGOV_SERVICE_UNAVAILABLE"

	// Chat errors
	ErrCodeChatNotFound        ErrorCode = "CHAT_NOT_FOUND"
	ErrCodeMessageNotFound     ErrorCode = "MESSAGE_NOT_FOUND"
	ErrCodeNotParticipant      ErrorCode = "NOT_PARTICIPANT"
	ErrCodeCannotEditMessage   ErrorCode = "CANNOT_EDIT_MESSAGE"
	ErrCodeCannotDeleteMessage ErrorCode = "CANNOT_DELETE_MESSAGE"
	ErrCodeEditWindowExpired   ErrorCode = "EDIT_WINDOW_EXPIRED"
	ErrCodeDeleteWindowExpired ErrorCode = "DELETE_WINDOW_EXPIRED"

	// Group errors
	ErrCodeGroupNotFound  ErrorCode = "GROUP_NOT_FOUND"
	ErrCodeNotGroupMember ErrorCode = "NOT_GROUP_MEMBER"
	ErrCodeNotGroupAdmin  ErrorCode = "NOT_GROUP_ADMIN"
	ErrCodeGroupFull      ErrorCode = "GROUP_FULL"

	// Channel errors
	ErrCodeNotSubscribed ErrorCode = "NOT_SUBSCRIBED"

	// Pinned errors
	ErrCodeMaxPinnedReached ErrorCode = "MAX_PINNED_REACHED"

	// File errors
	ErrCodeFileTooBig              ErrorCode = "FILE_TOO_BIG"
	ErrCodeInvalidFileType         ErrorCode = "INVALID_FILE_TYPE"
	ErrCodeFileUploadFailed        ErrorCode = "FILE_UPLOAD_FAILED"
	ErrCodeAttachmentPresignFailed ErrorCode = "ATTACHMENT_PRESIGN_FAILED"
	ErrCodeUploadFailed            ErrorCode = "UPLOAD_FAILED"
	ErrCodeAttachmentNotFound      ErrorCode = "ATTACHMENT_NOT_FOUND"
	ErrCodeAttachmentNotUploaded   ErrorCode = "ATTACHMENT_NOT_UPLOADED"

	// Notes errors
	ErrCodeNoteNotFound         ErrorCode = "NOTE_NOT_FOUND"
	ErrCodeNoteFolderNotFound   ErrorCode = "FOLDER_NOT_FOUND"
	ErrCodeNoteVersionConflict  ErrorCode = "NOTE_VERSION_CONFLICT"
	ErrCodeTableBlockNotFound   ErrorCode = "TABLE_BLOCK_NOT_FOUND"
	ErrCodeTableRowOutOfRange   ErrorCode = "TABLE_ROW_OUT_OF_RANGE"
	ErrCodeTableColOutOfRange   ErrorCode = "TABLE_COLUMN_OUT_OF_RANGE"
	ErrCodeTableMinSizeReached  ErrorCode = "TABLE_MIN_SIZE_REACHED"
	ErrCodeInvalidMark          ErrorCode = "INVALID_MARK"
	ErrCodeInvalidParagraphRuns ErrorCode = "INVALID_PARAGRAPH_RUNS"

	// Room & Resource booking errors
	ErrCodeRoomNotFound      ErrorCode = "ROOM_NOT_FOUND"
	ErrCodeRoomAlreadyBooked ErrorCode = "ROOM_ALREADY_BOOKED"
	ErrCodeRoomInactive      ErrorCode = "ROOM_INACTIVE"
)

// AppError represents an application error with context
type AppError struct {
	Code        ErrorCode      `json:"code"`
	Message     string         `json:"message"`
	UserMessage string         `json:"user_message,omitempty"`
	StatusCode  int            `json:"-"`
	Details     map[string]any `json:"details,omitempty"`
	Err         error          `json:"-"`
}

// Error implements the error interface
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap implements the unwrap interface for errors.Is and errors.As
func (e *AppError) Unwrap() error {
	return e.Err
}

// WithDetail adds a detail to the error
func (e *AppError) WithDetail(key string, value any) *AppError {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}

func (e *AppError) WithDetails(details map[string]any) *AppError {
	if details == nil {
		return e
	}
	for k, v := range details {
		e = e.WithDetail(k, v)
	}
	return e
}

func (e *AppError) WithUserMessage(message string) *AppError {
	e.UserMessage = message
	return e
}

// WithError wraps an underlying error
func (e *AppError) WithError(err error) *AppError {
	e.Err = err
	return e
}

// New creates a new AppError
func New(code ErrorCode, message string, statusCode int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		StatusCode: statusCode,
	}
}

// Common error constructors
func Internal(message string) *AppError {
	return New(ErrCodeInternal, message, http.StatusInternalServerError)
}

func InternalWithErr(message string, err error) *AppError {
	if offboardErr := offboardingDatabaseError(err); offboardErr != nil {
		return offboardErr
	}
	if storageErr := driveStorageDatabaseError(err); storageErr != nil {
		return storageErr
	}
	return Internal(message).WithError(err)
}

func Validation(message string) *AppError {
	return New(ErrCodeValidation, message, http.StatusBadRequest)
}

func NotFound(resource string) *AppError {
	return New(ErrCodeNotFound, fmt.Sprintf("%s not found", resource), http.StatusNotFound)
}

func Unauthorized(message string) *AppError {
	return New(ErrCodeUnauthorized, message, http.StatusUnauthorized)
}

func Forbidden(message string) *AppError {
	return New(ErrCodeForbidden, message, http.StatusForbidden)
}

func Conflict(message string) *AppError {
	return New(ErrCodeConflict, message, http.StatusConflict)
}

func BadRequest(message string) *AppError {
	return New(ErrCodeBadRequest, message, http.StatusBadRequest)
}

func TooManyRequests(message string) *AppError {
	return New(ErrCodeTooManyReqs, message, http.StatusTooManyRequests)
}

// Auth errors
func InvalidCredentials() *AppError {
	return New(ErrCodeInvalidCredentials, "Неверный логин или пароль", http.StatusUnauthorized).
		WithUserMessage("Неверный логин или пароль. Проверьте данные и попробуйте снова.")
}

func TokenExpired() *AppError {
	return New(ErrCodeTokenExpired, "Token has expired", http.StatusUnauthorized)
}

func TokenInvalid() *AppError {
	return New(ErrCodeTokenInvalid, "Invalid token", http.StatusUnauthorized)
}

func UserExists() *AppError {
	return New(ErrCodeUserExists, "User already exists", http.StatusConflict)
}

func WeakPassword() *AppError {
	return New(ErrCodeWeakPassword, "Password is too weak", http.StatusBadRequest)
}

func QRLoginRequired() *AppError {
	return New(
		ErrCodeQRLoginRequired,
		"Вход по логину и паролю в веб-версии недоступен. Пожалуйста, авторизуйтесь через QR-код в мобильном приложении.",
		http.StatusConflict,
	).WithDetails(map[string]any{
		"title":       "Войдите через QR-код",
		"description": "Этот аккаунт уже используется на мобильном устройстве. Для входа в веб-версию подтвердите авторизацию через QR-код в приложении.",
		"action":      "Откройте мобильное приложение и отсканируйте QR-код на экране входа.",
		"action_text": "Войти через QR",
		"auth_method": "qr",
	})
}

func InvalidPIN() *AppError {
	return New(ErrCodeInvalidPIN, "Invalid PIN code", http.StatusBadRequest).
		WithUserMessage("Неверный PIN-код.")
}

func ReauthRequired(message string) *AppError {
	return New(ErrCodeReauthRequired, message, http.StatusUnauthorized).
		WithUserMessage("Сессия истекла. Войдите заново.")
}

func RefreshFailed(message string) *AppError {
	return New(ErrCodeRefreshFailed, message, http.StatusBadGateway).
		WithUserMessage("Не удалось обновить сессию. Попробуйте ещё раз.")
}

// Chat errors
func ChatNotFound() *AppError {
	return New(ErrCodeChatNotFound, "Chat not found", http.StatusNotFound)
}

func MessageNotFound() *AppError {
	return New(ErrCodeMessageNotFound, "Message not found", http.StatusNotFound)
}

func NotParticipant() *AppError {
	return New(ErrCodeNotParticipant, "You are not a participant of this chat", http.StatusForbidden)
}

func CannotEditMessage() *AppError {
	return New(ErrCodeCannotEditMessage, "Cannot edit this message", http.StatusForbidden)
}

func CannotDeleteMessage() *AppError {
	return New(ErrCodeCannotDeleteMessage, "Cannot delete this message", http.StatusForbidden)
}

func EditWindowExpired() *AppError {
	return New(ErrCodeEditWindowExpired, "Message edit window has expired", http.StatusForbidden).
		WithUserMessage("Время для редактирования сообщения истекло.")
}

func DeleteWindowExpired() *AppError {
	return New(ErrCodeDeleteWindowExpired, "Delete-for-everyone window has expired", http.StatusForbidden).
		WithUserMessage("Время для удаления сообщения у всех истекло.")
}

// Group errors
func GroupNotFound() *AppError {
	return New(ErrCodeGroupNotFound, "Group not found", http.StatusNotFound)
}

func NotGroupMember() *AppError {
	return New(ErrCodeNotGroupMember, "You are not a member of this group", http.StatusForbidden)
}

func NotGroupAdmin() *AppError {
	return New(ErrCodeNotGroupAdmin, "You are not an admin of this group", http.StatusForbidden)
}

// Channel errors
func NotSubscribed() *AppError {
	return New(ErrCodeNotSubscribed, "You are not subscribed to this channel", http.StatusForbidden)
}

func GroupFull() *AppError {
	return New(ErrCodeGroupFull, "Group has reached maximum members", http.StatusBadRequest)
}

// Pinned errors
func MaxPinnedChatsReached() *AppError {
	return New(ErrCodeMaxPinnedReached, "Maximum number of pinned chats reached (5)", http.StatusBadRequest)
}

// File errors
func FileTooBig(maxSize int64) *AppError {
	return New(ErrCodeFileTooBig, fmt.Sprintf("File size exceeds maximum of %d bytes", maxSize), http.StatusBadRequest)
}

func InvalidFileType(allowedTypes string) *AppError {
	return New(ErrCodeInvalidFileType, fmt.Sprintf("Invalid file type. Allowed: %s", allowedTypes), http.StatusBadRequest)
}

func FileUploadFailed() *AppError {
	return New(ErrCodeFileUploadFailed, "File upload failed", http.StatusInternalServerError)
}

func AttachmentPresignFailed() *AppError {
	return New(ErrCodeAttachmentPresignFailed, "Failed to create upload URL", http.StatusBadGateway)
}

func UploadFailed() *AppError {
	return New(ErrCodeUploadFailed, "Upload failed", http.StatusBadGateway)
}

func AttachmentNotFound() *AppError {
	return New(ErrCodeAttachmentNotFound, "Attachment not found", http.StatusNotFound)
}

func AttachmentNotUploaded() *AppError {
	return New(ErrCodeAttachmentNotUploaded, "Attachment was not uploaded to storage", http.StatusBadRequest)
}

func NoteNotFound() *AppError {
	return New(ErrCodeNoteNotFound, "Note not found", http.StatusNotFound)
}

func NoteFolderNotFound() *AppError {
	return New(ErrCodeNoteFolderNotFound, "Folder not found", http.StatusNotFound)
}

func NoteVersionConflict() *AppError {
	return New(ErrCodeNoteVersionConflict, "Note version conflict. Please sync state from server.", http.StatusConflict)
}

func TableBlockNotFound() *AppError {
	return New(ErrCodeTableBlockNotFound, "Table block not found", http.StatusNotFound)
}

func TableRowOutOfRange() *AppError {
	return New(ErrCodeTableRowOutOfRange, "Table row index is out of range", http.StatusBadRequest)
}

func TableColumnOutOfRange() *AppError {
	return New(ErrCodeTableColOutOfRange, "Table column index is out of range", http.StatusBadRequest)
}

func TableMinSizeReached() *AppError {
	return New(ErrCodeTableMinSizeReached, "Table must keep at least one column", http.StatusBadRequest)
}

func InvalidMark(message string) *AppError {
	return New(ErrCodeInvalidMark, message, http.StatusBadRequest)
}

func InvalidParagraphRuns(message string) *AppError {
	return New(ErrCodeInvalidParagraphRuns, message, http.StatusBadRequest)
}

// Room & Resource booking errors
func RoomNotFound() *AppError {
	return New(ErrCodeRoomNotFound, "Переговорная комната не найдена", http.StatusNotFound).
		WithUserMessage("Переговорная комната не найдена.")
}

func RoomAlreadyBooked(message string) *AppError {
	if message == "" {
		message = "Переговорная комната уже забронирована на указанное время"
	}
	return New(ErrCodeRoomAlreadyBooked, message, http.StatusConflict).
		WithUserMessage(message)
}

func RoomInactive() *AppError {
	return New(ErrCodeRoomInactive, "Переговорная комната временно недоступна для бронирования", http.StatusBadRequest).
		WithUserMessage("Переговорная комната временно недоступна для бронирования.")
}

// IsAppError checks if an error is an AppError
func IsAppError(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr)
}

// GetAppError extracts AppError from error
func GetAppError(err error) *AppError {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return nil
}

// ToAppError converts any error to AppError
func ToAppError(err error) *AppError {
	if err == nil {
		return nil
	}

	if offboardErr := offboardingDatabaseError(err); offboardErr != nil {
		return offboardErr
	}
	if storageErr := driveStorageDatabaseError(err); storageErr != nil {
		return storageErr
	}
	if appErr := GetAppError(err); appErr != nil {
		return appErr
	}

	return Internal("An unexpected error occurred").WithError(err)
}

type ErrorResponse struct {
	Error ErrorPayload `json:"error"`
}

type ErrorPayload struct {
	Code        ErrorCode      `json:"code"`
	Message     string         `json:"message"`
	UserMessage string         `json:"user_message,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

func ResponseBody(err *AppError) ErrorResponse {
	return ResponseBodyLocale(err, i18n.Russian)
}
