package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) CreateSDHandoff(ctx context.Context, actor int64, browserHash string) (string, error) {
	profile, err := s.repo.SDHandoffProfile(ctx, actor)
	var appError *apperrors.AppError
	if errors.As(err, &appError) && appError.Code == "SD_COMPANY_NOT_LINKED" {
		if err = s.ensureSDCompanyForUser(ctx, actor); err != nil {
			return "", err
		}
		// Reload both membership and the link: the user's company may have
		// changed while the remote tenant was being provisioned.
		profile, err = s.repo.SDHandoffProfile(ctx, actor)
	}
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")), "/")
	token := strings.TrimSpace(os.Getenv("SD_ACCESS_INTERNAL_TOKEN"))
	if base == "" || token == "" {
		return "", apperrors.Conflict("Единый вход SenimDesk ещё не настроен на сервере Workspace")
	}
	payload, err := json.Marshal(struct {
		Profile     domain.SDHandoffProfile `json:"profile"`
		BrowserHash string                  `json:"browser_hash"`
	}{profile, browserHash})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/internal/workspace/handoff", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", apperrors.Conflict("Не удалось подключиться к SenimDesk. Повторите попытку")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", apperrors.Conflict("SenimDesk отклонил вход. Проверьте привязку компании, активный поддомен и учётную запись СД")
	}
	var out struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return "", err
	}
	return out.RedirectURL, nil
}
