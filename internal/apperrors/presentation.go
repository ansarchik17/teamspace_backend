package apperrors

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

var publicCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,79}$`)

// StatusCode supplies stable codes for legacy endpoints that had only text.
func CodeForStatus(status int) ErrorCode {
	switch status {
	case http.StatusUnauthorized:
		return ErrCodeUnauthorized
	case http.StatusForbidden:
		return ErrCodeForbidden
	case http.StatusNotFound:
		return ErrCodeNotFound
	case http.StatusConflict:
		return ErrCodeConflict
	case http.StatusTooManyRequests:
		return ErrCodeTooManyReqs
	case http.StatusRequestEntityTooLarge:
		return ErrCodeFileTooBig
	case http.StatusUnprocessableEntity:
		return ErrCodeValidation
	default:
		if status >= 500 {
			return ErrCodeInternal
		}
		return ErrCodeBadRequest
	}
}

func SafeCode(code string, status int) ErrorCode {
	if publicCode.MatchString(code) {
		return ErrorCode(code)
	}
	return CodeForStatus(status)
}

func LocalizedMessage(code ErrorCode, status int, locale i18n.Locale) string {
	text, err := i18n.Render(locale, "error."+string(code), nil)
	if err != nil {
		text = i18n.Text(locale, "error."+string(CodeForStatus(status)), nil)
	}
	return text
}

func ResponseBodyLocale(err *AppError, locale i18n.Locale) ErrorResponse {
	if err == nil {
		err = Internal("An unexpected error occurred")
	}
	code := SafeCode(string(err.Code), err.StatusCode)
	message := LocalizedMessage(code, err.StatusCode, locale)
	if err.StatusCode < 500 {
		variants := map[string]string{
			"file is required":                                     "FILE_REQUIRED",
			"invalid note id":                                      "INVALID_NOTE_ID",
			"too many files in one request":                        "TOO_MANY_FILES",
			"user identity (user_id or email) is required":         "USER_IDENTITY_REQUIRED",
			"language must be ru or kk":                            "LANGUAGE_INVALID",
			"duplicate file already exists in the target location": "DUPLICATE_FILE",
			"calendar_conflict":                                    "CALENDAR_CONFLICT",
		}
		if variant, ok := variants[err.Message]; ok {
			message = i18n.Text(locale, "error_variant."+variant, nil)
		}
	}
	userMessage := message
	if text, renderErr := i18n.Render(locale, "error_user."+string(code), nil); renderErr == nil {
		userMessage = text
	}
	details := PublicDetails(code, err.Details, locale)
	if code == ErrCodeAIQuotaExceeded {
		// Derive this variant only from allowlisted business metadata; never
		// preserve a raw provider or handler error string as the public message.
		if blockers, ok := details["blockers"].([]map[string]any); ok && len(blockers) == 1 && blockers[0]["reason"] == "RESERVATION_PENDING" && blockers[0]["scope"] == "account" {
			message = i18n.Text(locale, "error.AI_GENERATION_PENDING", nil)
			userMessage = message
		}
	}
	return ErrorResponse{Error: ErrorPayload{Code: code, Message: message, UserMessage: userMessage, Details: details}}
}

// PublicDetails is an allowlist of business metadata. Diagnostics remain on the
// AppError for logging; backend/provider responses and arbitrary strings never
// become UI details.
func PublicDetails(code ErrorCode, details map[string]any, locale i18n.Locale) map[string]any {
	if code == "INVITATION_REGISTRATION_LOGIN" {
		if username, ok := details["username"].(string); ok && len(username) > 0 && len(username) <= 200 && !strings.ContainsAny(username, "\r\n\x00") {
			return map[string]any{"username": username}
		}
		return nil
	}
	if code == ErrCodeAICompanySuspended && details["reason"] == "COMPANY_SUSPENDED" {
		return map[string]any{"reason": "COMPANY_SUSPENDED"}
	}
	if code == ErrCodeAIConfigurationRequired && details["reason"] == "COMPANY_BUDGET_UNCONFIGURED" {
		return map[string]any{"reason": "COMPANY_BUDGET_UNCONFIGURED"}
	}
	if code == ErrCodeAIRevisionConflict {
		out := make(map[string]any)
		if scope, ok := details["scope"].(string); ok && (scope == "company" || scope == "user") {
			out["scope"] = scope
		}
		for _, field := range []string{"expected_revision", "current_revision"} {
			if value, ok := details[field].(string); ok {
				if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 && strconv.FormatInt(parsed, 10) == value {
					out[field] = value
				}
			}
		}
		return out
	}
	if code == ErrCodeAIBudgetConflict {
		if reason, ok := details["reason"].(string); ok {
			switch reason {
			case "INSUFFICIENT_BUDGET", "BUDGET_BELOW_COMMITTED", "INCREASE_WHILE_OVERCOMMITTED":
				return map[string]any{"reason": reason}
			}
		}
		return nil
	}
	if code == ErrCodeAIAccessDisabled {
		out := make(map[string]any)
		if reason, ok := details["reason"].(string); ok {
			switch reason {
			case "NO_ORGANIZATION", "COMPANY_SUSPENDED", "AI_ACCESS_DENIED",
				"COMPANY_BUDGET_UNCONFIGURED", "COMPANY_BUDGET_DISABLED",
				"USER_QUOTA_UNCONFIGURED", "USER_QUOTA_DISABLED", "FIVE_HOUR_DISABLED",
				"SERVICE_QUOTA_DISABLED", "CREDIT_POLICY_UNAVAILABLE":
				out["reason"] = reason
			}
		}
		if scope, ok := details["scope"].(string); ok {
			switch scope {
			case "account", "company", "weekly", "five_hour", "service", "policy":
				out["scope"] = scope
			}
		}
		if len(out) > 0 {
			return out
		}
		return nil
	}
	if code == ErrCodeAIQuotaExceeded {
		out := make(map[string]any)
		if blockers, ok := details["blockers"]; ok {
			out["blockers"] = sanitizeBlockers(blockers)
		}
		out["retry_at"] = details["retry_at"]
		if retryKind, ok := details["retry_kind"].(string); ok {
			switch retryKind {
			case "none", "estimated", "requires_action", "unknown":
				out["retry_kind"] = retryKind
			default:
				out["retry_kind"] = "requires_action"
			}
		} else {
			out["retry_kind"] = "requires_action"
		}
		out["recheck_at"] = details["recheck_at"]
		if serverTime, ok := details["server_time"].(string); ok {
			out["server_time"] = serverTime
		}
		return out
	}
	if code == ErrCodeAIReservationConflict {
		if reason, ok := details["reason"].(string); ok {
			switch reason {
			case "OPERATION_CONFLICT", "IDEMPOTENCY_CONFLICT":
				return map[string]any{"reason": reason}
			}
		}
		return nil
	}
	if code == ErrCodeQRLoginRequired {
		result := map[string]any{"auth_method": "qr"}
		for _, field := range []string{"title", "description", "action", "action_text"} {
			result[field] = i18n.Text(locale, "error_qr."+field, nil)
		}
		return result
	}
	var allowed []string
	switch code {
	case ErrCodeDriveQuotaExceeded, ErrCodeFileTooBig:
		allowed = []string{"quota_bytes", "used_bytes", "requested_bytes", "available_bytes", "limit_bytes", "max_size", "max_size_bytes", "file_size", "file_size_bytes"}
	case ErrCodeRoomAlreadyBooked:
		allowed = []string{"room_id", "room_name", "conflicting_slot"}
	case ErrCodeConflict:
		allowed = []string{"conflicts", "suggested_slots", "current_version", "expected_version", "duplicate_file_id", "duplicate_file_name", "duplicate_folder_id"}
	case ErrCodeForbidden:
		allowed = []string{"resource_type", "resource_id", "required_role", "current_file_role"}
	case ErrCodeNoteVersionConflict:
		allowed = []string{"current_version", "expected_version"}
	case ErrCodeBadRequest:
		allowed = []string{"max_files_per_request"}
	case ErrCodeValidation:
		result := make(map[string]any)
		for field, value := range details {
			tag, ok := value.(string)
			if !ok || !publicCode.MatchString(field) {
				continue
			}
			switch tag {
			case "required", "email", "min", "max", "len", "oneof":
				result[field] = i18n.Text(locale, "validation."+tag, nil)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}
	result := make(map[string]any)
	for _, key := range allowed {
		if value, ok := details[key]; ok {
			switch key {
			case "resource_id", "duplicate_file_id", "duplicate_folder_id", "room_id":
				text := fmt.Sprint(value)
				if _, err := uuid.Parse(text); err != nil {
					continue
				}
			case "resource_type":
				if value != "file" && value != "folder" {
					continue
				}
			case "required_role", "current_file_role":
				if value != "viewer" && value != "editor" && value != "owner" {
					continue
				}
			case "room_name", "duplicate_file_name":
				if _, ok := value.(string); !ok {
					continue
				}
			}
			if strings.HasSuffix(key, "bytes") || key == "max_size" || key == "file_size" || key == "max_files_per_request" || strings.HasSuffix(key, "version") {
				if value == nil {
					continue
				}
				switch reflect.TypeOf(value).Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				default:
					continue
				}
			}
			if key == "conflicts" || key == "suggested_slots" || key == "conflicting_slot" {
				kind := reflect.TypeOf(value)
				if kind == nil {
					continue
				}
				if key == "conflicting_slot" && kind.Kind() != reflect.Map {
					continue
				}
				if key != "conflicting_slot" && kind.Kind() != reflect.Slice && kind.Kind() != reflect.Array {
					continue
				}
				value = publicBusinessMetadata(value)
			}
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func publicBusinessMetadata(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if json.Unmarshal(encoded, &decoded) != nil {
		return nil
	}
	var clean func(any) any
	clean = func(item any) any {
		switch item := item.(type) {
		case map[string]any:
			result := make(map[string]any)
			for key, child := range item {
				switch strings.ToLower(key) {
				case "response", "error", "debug", "trace", "stack", "query", "reason", "message", "user_message", "detail", "details", "token", "access_token", "refresh_token", "password", "keycloak_error", "provider_error":
					continue
				}
				result[key] = clean(child)
			}
			return result
		case []any:
			for index := range item {
				item[index] = clean(item[index])
			}
			return item
		default:
			return item
		}
	}
	return clean(decoded)
}

func sanitizeBlockers(raw any) []map[string]any {
	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case []map[string]any:
		for _, item := range v {
			list = append(list, item)
		}
	default:
		// Try JSON round-trip for structs like []domain.AIBlocker
		if data, err := json.Marshal(raw); err == nil {
			var decoded []any
			if json.Unmarshal(data, &decoded) == nil {
				list = decoded
			}
		}
	}
	clean := make([]map[string]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		b := make(map[string]any)
		if reason, ok := m["reason"].(string); ok {
			switch reason {
			case "FIVE_HOUR_EXHAUSTED", "USER_WEEKLY_EXHAUSTED", "COMPANY_WEEKLY_EXHAUSTED",
				"SERVICE_WEEKLY_EXHAUSTED", "RESERVATION_PENDING", "RESERVATION_TOO_LARGE":
				b["reason"] = reason
			}
		}
		if scope, ok := m["scope"].(string); ok {
			switch scope {
			case "five_hour", "weekly", "company", "service", "account":
				b["scope"] = scope
			}
		}
		if enforced, ok := m["enforced"].(bool); ok {
			b["enforced"] = enforced
		}
		if resetsAt, ok := m["resets_at"].(string); ok && resetsAt != "" {
			b["resets_at"] = resetsAt
		} else {
			b["resets_at"] = nil
		}
		if len(b) > 0 {
			clean = append(clean, b)
		}
	}
	return clean
}
