package service

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"strings"
)

type emailProfileLookup interface {
	GetByEmail(context.Context, string) (*domain.User, error)
}

// Email addresses can belong to external recipients without a profile.
func recipientEmailLocale(ctx context.Context, lookup emailProfileLookup, address string) i18n.Locale {
	if lookup != nil {
		user, err := lookup.GetByEmail(ctx, strings.TrimSpace(address))
		if err == nil && user != nil {
			return i18n.ResolveRecipient(user.Language)
		}
	}
	return i18n.Russian
}

func (s *NotificationService) recipientLocaleByEmail(ctx context.Context, address string) i18n.Locale {
	lookup, _ := s.userRepo.(emailProfileLookup)
	return recipientEmailLocale(ctx, lookup, address)
}

func emailImportance(locale i18n.Locale, importance string) string {
	switch importance {
	case domain.PublicationImportanceUrgent:
		return i18n.Text(locale, "email.importance.urgent", nil)
	case domain.PublicationImportanceImportant:
		return i18n.Text(locale, "email.importance.important", nil)
	default:
		return i18n.Text(locale, "email.importance.normal", nil)
	}
}
