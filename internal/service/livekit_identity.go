package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type LiveKitTokenContext struct {
	RequestID string
	DeviceID  string
	SessionID string
	UserAgent string
}

type ParticipantIdentity struct {
	Raw       string
	UserID    int64
	SessionID string
}

func BuildParticipantIdentity(userID int64, sessionID string) string {
	sanitizedSessionID := sanitizeParticipantSessionID(sessionID)
	if sanitizedSessionID == "" {
		return fmt.Sprintf("%d", userID)
	}
	return fmt.Sprintf("%d:%s", userID, sanitizedSessionID)
}

func ResolveParticipantIdentityScope(tokenCtx LiveKitTokenContext) string {
	if deviceID := sanitizeParticipantSessionID(tokenCtx.DeviceID); deviceID != "" {
		return "dev." + deviceID
	}
	return sanitizeParticipantSessionID(tokenCtx.SessionID)
}

func BuildParticipantIdentityForToken(userID int64, tokenCtx LiveKitTokenContext) string {
	return BuildParticipantIdentity(userID, ResolveParticipantIdentityScope(tokenCtx))
}

func ParseParticipantIdentity(identity string) (ParticipantIdentity, error) {
	raw := strings.TrimSpace(identity)
	if raw == "" {
		return ParticipantIdentity{}, fmt.Errorf("participant identity is empty")
	}

	parts := strings.SplitN(raw, ":", 2)
	userID, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return ParticipantIdentity{}, fmt.Errorf("parse participant user id: %w", err)
	}

	parsed := ParticipantIdentity{
		Raw:    raw,
		UserID: userID,
	}
	if len(parts) == 2 {
		parsed.SessionID = sanitizeParticipantSessionID(parts[1])
	}
	return parsed, nil
}

func IdentityBelongsToUser(identity string, userID int64) bool {
	parsed, err := ParseParticipantIdentity(identity)
	if err != nil {
		return false
	}
	return parsed.UserID == userID
}

func IdentityMode(identity string) string {
	parsed, err := ParseParticipantIdentity(identity)
	if err != nil {
		return "unknown"
	}
	if parsed.SessionID == "" {
		return "user"
	}
	return "user_session"
}

func sanitizeParticipantSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(sessionID))

	for _, char := range sessionID {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-', char == '_', char == '.':
			builder.WriteRune(char)
		}
	}

	sanitized := builder.String()
	if len(sanitized) > 64 {
		sanitized = sanitized[:64]
	}
	return sanitized
}

func buildTokenMetadata(userID int64, username string, tokenCtx LiveKitTokenContext, identity string) string {
	metadata := map[string]string{
		"user_id":       strconv.FormatInt(userID, 10),
		"username":      strings.TrimSpace(username),
		"identity":      strings.TrimSpace(identity),
		"identity_mode": IdentityMode(identity),
	}

	if requestID := strings.TrimSpace(tokenCtx.RequestID); requestID != "" {
		metadata["request_id"] = requestID
	}
	if deviceID := sanitizeParticipantSessionID(tokenCtx.DeviceID); deviceID != "" {
		metadata["device_id"] = deviceID
	}
	if sessionID := sanitizeParticipantSessionID(tokenCtx.SessionID); sessionID != "" {
		metadata["call_session_id"] = sessionID
	}
	if userAgent := strings.TrimSpace(tokenCtx.UserAgent); userAgent != "" {
		metadata["user_agent"] = userAgent
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildTokenAttributes(userID int64, tokenCtx LiveKitTokenContext, identity string) map[string]string {
	attributes := map[string]string{
		"user_id":       strconv.FormatInt(userID, 10),
		"identity_mode": IdentityMode(identity),
	}

	if requestID := strings.TrimSpace(tokenCtx.RequestID); requestID != "" {
		attributes["request_id"] = requestID
	}
	if deviceID := sanitizeParticipantSessionID(tokenCtx.DeviceID); deviceID != "" {
		attributes["device_id"] = deviceID
	}
	if sessionID := sanitizeParticipantSessionID(tokenCtx.SessionID); sessionID != "" {
		attributes["call_session_id"] = sessionID
	}

	return attributes
}
