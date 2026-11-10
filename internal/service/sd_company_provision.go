package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) ProvisionSDCompany(ctx context.Context, actor int64, org uuid.UUID, address string) (domain.AccessSDCompany, error) {
	profile, err := s.repo.SDCompanyProvisionDetails(ctx, actor, org)
	if err != nil {
		return domain.AccessSDCompany{}, err
	}
	profile.Address = strings.TrimSpace(address)
	return s.provisionVerifiedSDCompany(ctx, actor, profile, false)
}

func (s *OrganizationService) ensureSDCompanyForUser(ctx context.Context, actor int64) error {
	profile, err := s.repo.SDCompanyProvisionDetailsForUser(ctx, actor)
	if err != nil {
		return err
	}
	_, err = s.provisionVerifiedSDCompany(ctx, actor, profile, true)
	return err
}

func (s *OrganizationService) provisionVerifiedSDCompany(ctx context.Context, actor int64, profile domain.SDCompanyProvision, automatic bool) (domain.AccessSDCompany, error) {
	var out domain.AccessSDCompany
	if profile.LinkedCompany != nil {
		return *profile.LinkedCompany, nil
	}
	if len([]rune(profile.Address)) > 1000 {
		return out, apperrors.Validation("Адрес компании должен содержать не более 1000 символов")
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")), "/")
	token := strings.TrimSpace(os.Getenv("SD_ACCESS_INTERNAL_TOKEN"))
	if base == "" || token == "" {
		return out, apperrors.Conflict("Интеграция SenimDesk ещё не настроена на сервере")
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/internal/workspace/companies", bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", token)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return out, apperrors.Conflict("Создание компании СД не подтверждено. Повторите попытку: повторный запрос восстановит связь без дубликатов")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&failure)
		if resp.StatusCode == http.StatusConflict && failure.Message != "" {
			return out, apperrors.Conflict(failure.Message)
		}
		return out, apperrors.Conflict("Не удалось создать компанию SenimDesk. Проверьте обновление backend СД и общий серверный токен")
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return out, err
	}
	expected := uuid.NewSHA1(uuid.NameSpaceURL, []byte("workspace:sd-company:"+profile.OrganizationID.String()))
	if out.ID != expected || !out.IsTenantRoot {
		return out, apperrors.Conflict("SenimDesk вернул некорректную компанию")
	}
	if automatic {
		err = s.repo.LinkAutomaticallyProvisionedSDCompany(ctx, actor, profile, out.ID)
	} else {
		err = s.repo.LinkProvisionedSDCompany(ctx, actor, profile, out.ID)
	}
	if err != nil {
		return out, err
	}
	return out, nil
}
