package auth

import (
	"context"
	"strings"
)

type LoginMonitor interface {
	CheckBlocked(ctx context.Context, userID int64, deviceID string) (bool, error)
	NotifyLogin(ctx context.Context, event LoginEvent) error
}

type LoginEvent struct {
	UserID     int64
	Email      string
	Username   string
	FullName   string
	Source     string
	IP         string
	UserAgent  string
	DeviceID   string
	SessionID  string
	AuthMethod string
}

type LoginRequestMeta struct {
	Source    string
	IP        string
	UserAgent string
	DeviceID  string
	SessionID string
	RequestID string
}

type loginRequestMetaContextKey struct{}

func WithLoginRequestMeta(ctx context.Context, meta LoginRequestMeta) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	meta.Source = strings.TrimSpace(meta.Source)
	meta.IP = strings.TrimSpace(meta.IP)
	meta.UserAgent = strings.TrimSpace(meta.UserAgent)
	meta.DeviceID = strings.TrimSpace(meta.DeviceID)
	meta.SessionID = strings.TrimSpace(meta.SessionID)
	meta.RequestID = strings.TrimSpace(meta.RequestID)
	return context.WithValue(ctx, loginRequestMetaContextKey{}, meta)
}

func LoginRequestMetaFromContext(ctx context.Context) LoginRequestMeta {
	if ctx == nil {
		return LoginRequestMeta{}
	}
	meta, _ := ctx.Value(loginRequestMetaContextKey{}).(LoginRequestMeta)
	return meta
}

func buildLoginEventFromUser(ctx context.Context, userID int64, email, username, fullName, authMethod string) LoginEvent {
	meta := LoginRequestMetaFromContext(ctx)
	return LoginEvent{
		UserID:     userID,
		Email:      strings.TrimSpace(email),
		Username:   strings.TrimSpace(username),
		FullName:   strings.TrimSpace(fullName),
		Source:     strings.TrimSpace(meta.Source),
		IP:         strings.TrimSpace(meta.IP),
		UserAgent:  strings.TrimSpace(meta.UserAgent),
		DeviceID:   strings.TrimSpace(meta.DeviceID),
		SessionID:  strings.TrimSpace(meta.SessionID),
		AuthMethod: strings.TrimSpace(authMethod),
	}
}
