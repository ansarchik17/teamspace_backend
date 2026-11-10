package service

import (
	"strconv"
	"strings"

	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type liveKitClientURLResolver struct {
	defaultURL      string
	publicURL       string
	publicSelectors map[string]struct{}
}

func newLiveKitClientURLResolver(cfg config.LiveKitConfig) liveKitClientURLResolver {
	resolver := liveKitClientURLResolver{
		defaultURL: strings.TrimSpace(cfg.URL),
		publicURL:  sanitizePublicLiveKitClientURL(cfg.URL, cfg.PublicURL),
	}
	if len(cfg.PublicUsers) == 0 {
		return resolver
	}

	resolver.publicSelectors = make(map[string]struct{}, len(cfg.PublicUsers))
	for _, selector := range cfg.PublicUsers {
		normalized := normalizeLiveKitPublicSelector(selector)
		if normalized == "" {
			continue
		}
		resolver.publicSelectors[normalized] = struct{}{}
	}
	return resolver
}

func sanitizePublicLiveKitClientURL(defaultURL, publicURL string) string {
	publicURL = strings.TrimSpace(publicURL)
	if publicURL == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(publicURL), "wss://") {
		return publicURL
	}
	return strings.TrimSpace(defaultURL)
}

func normalizeLiveKitPublicSelector(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (r liveKitClientURLResolver) Resolve(userID int64, username, email string) string {
	if r.publicURL == "" || len(r.publicSelectors) == 0 {
		return r.defaultURL
	}
	if userID > 0 {
		if _, ok := r.publicSelectors[strconv.FormatInt(userID, 10)]; ok {
			return r.publicURL
		}
	}
	if username != "" {
		if _, ok := r.publicSelectors[normalizeLiveKitPublicSelector(username)]; ok {
			return r.publicURL
		}
	}
	if email != "" {
		if _, ok := r.publicSelectors[normalizeLiveKitPublicSelector(email)]; ok {
			return r.publicURL
		}
	}
	return r.defaultURL
}

func (r liveKitClientURLResolver) ResolveUser(user *domain.User) string {
	if user == nil {
		return r.defaultURL
	}
	return r.Resolve(user.ID, user.Username, user.Email)
}
