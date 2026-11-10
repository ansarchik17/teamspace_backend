package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func (s *OrganizationService) OrganizationAccess(ctx context.Context, actor int64, org *uuid.UUID) (*domain.OrganizationAccess, error) {
	return s.repo.OrganizationAccess(ctx, actor, org)
}
func (s *OrganizationService) ChangeOrganizationAccess(ctx context.Context, actor int64, org *uuid.UUID, action string, data repository.AccessChange) error {
	if action == "link" {
		if org == nil {
			return apperrors.Forbidden("Связь с СД настраивает системный администратор")
		}
		companies, err := s.AccessSDCompanies(ctx, actor, *org)
		if err != nil {
			return err
		}
		found := false
		for _, company := range companies {
			if company.ID == data.CompanyID {
				found = true
				data.CompanyTenantRoot = company.IsTenantRoot
			}
		}
		if !found {
			return apperrors.Validation("Активная компания с таким ID не найдена в СД")
		}
	}
	return s.repo.ChangeOrganizationAccess(ctx, actor, org, action, data)
}
func (s *OrganizationService) ResolveOwnAccess(ctx context.Context, actor int64, app string) (domain.ResolvedAccess, error) {
	return s.repo.ResolveOwnAccess(ctx, actor, app)
}
func (s *OrganizationService) ResolveSDExternalAccess(ctx context.Context, company uuid.UUID, username string) (domain.ResolvedAccess, error) {
	return s.repo.ResolveSDExternalAccess(ctx, company, username)
}

func (s *OrganizationService) AccessSDCompanies(ctx context.Context, actor int64, org uuid.UUID) ([]domain.AccessSDCompany, error) {
	if err := s.repo.AuthorizeAccessPlatform(ctx, actor, org); err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")), "/")
	token := strings.TrimSpace(os.Getenv("SD_ACCESS_INTERNAL_TOKEN"))
	if base == "" || token == "" {
		return nil, apperrors.Conflict("Системный администратор должен настроить SD_ACCESS_API_URL и SD_ACCESS_INTERNAL_TOKEN на сервере Workspace")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/internal/workspace/companies", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", token)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, apperrors.Conflict("Не удалось подключиться к каталогу компаний СД")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.Conflict("Каталог компаний СД недоступен. Проверьте URL и общий токен")
	}
	result := []domain.AccessSDCompany{}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *OrganizationService) ResolveSDIdentity(ctx context.Context, subject string) (domain.SDWorkspaceIdentity, error) {
	return s.repo.ResolveSDIdentity(ctx, subject)
}
