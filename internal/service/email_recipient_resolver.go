package service

import (
	"fmt"
	"net/mail"
	"strings"

	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type EmailRecipientResolver struct {
	domain       string
	specialUsers map[string]string
}

func NewEmailRecipientResolver(cfg config.SMTPConfig) *EmailRecipientResolver {
	specialUsers := make(map[string]string, len(cfg.CDNKindyUsers))
	for _, username := range cfg.CDNKindyUsers {
		trimmed := strings.TrimSpace(username)
		if trimmed == "" {
			continue
		}
		specialUsers[strings.ToLower(trimmed)] = trimmed
	}

	return &EmailRecipientResolver{
		domain:       strings.TrimSpace(cfg.CDNKindyDomain),
		specialUsers: specialUsers,
	}
}

func (r *EmailRecipientResolver) ResolveEmailRecipient(user *domain.User) (string, error) {
	if user == nil {
		return "", fmt.Errorf("%w: user is nil", ErrInvalidEmailRecipient)
	}

	username := strings.TrimSpace(user.Username)
	if username != "" && r != nil && r.domain != "" {
		if preserved, ok := r.specialUsers[strings.ToLower(username)]; ok {
			recipient := preserved + "@" + r.domain
			if _, err := mail.ParseAddress(recipient); err != nil {
				return "", fmt.Errorf("%w: %s", ErrInvalidEmailRecipient, recipient)
			}
			return recipient, nil
		}
	}

	recipient := strings.TrimSpace(user.Email)
	if recipient == "" {
		return "", fmt.Errorf("%w: empty email", ErrInvalidEmailRecipient)
	}
	if _, err := mail.ParseAddress(recipient); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidEmailRecipient, recipient)
	}
	return recipient, nil
}
