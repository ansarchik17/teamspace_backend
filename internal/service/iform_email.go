package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"os"
	"strings"
)

func (s *OrganizationService) IFormEmailSuggestion(ctx context.Context, actor int64, company uuid.UUID, first, last string, unavailable []string) (string, string, error) {
	organization, err := s.repo.CurrentOrganization(ctx, actor)
	if err != nil {
		return "", "", err
	}
	if organization.ID != company {
		return "", "", apperrors.Forbidden("Компания пользователя изменилась")
	}
	base := strings.TrimSpace(os.Getenv("WORKSPACE_MAIL_DOMAIN_BASE"))
	if base == "" {
		base = "teamspace.kz"
	}
	mailDomain := domain.CompanyMailDomain(organization.Slug, base)
	login := domain.SuggestedEmployeeLogin(first, last)
	if mailDomain == "" {
		return "", "", apperrors.Internal("Не настроен почтовый домен компании")
	}
	if login == "" {
		return "", "", apperrors.Validation("Укажите имя и фамилию для корпоративной почты")
	}
	blocked := map[string]bool{}
	for _, email := range unavailable {
		blocked[strings.ToLower(email)] = true
	}
	candidates := []string{}
	for i := 0; i < 2000; i++ {
		local := login
		if i > 0 {
			local = fmt.Sprintf("%s%d", login, i+1)
		}
		email := local + "@" + mailDomain
		if !blocked[email] {
			candidates = append(candidates, email)
		}
	}
	email, err := s.repo.FirstAvailableIFormEmail(ctx, candidates)
	if err != nil {
		return "", "", err
	}
	if email == "" {
		return "", "", apperrors.Conflict("Не удалось подобрать свободный адрес почты")
	}
	return email, mailDomain, nil
}
