package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func sdCounterUnavailable() error {
	return apperrors.New("SD_COUNTER_UNAVAILABLE", "SD ticket counter unavailable", http.StatusServiceUnavailable)
}

// Use an already linked user and company. A dashboard read must never provision
// an SD account, mint a browser handoff or use an administrator's identity.
func (s *OrganizationService) SDOpenTicketCount(ctx context.Context, actor int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	authorization, err := s.AIReadSDAuthorization(ctx, AIDataIdentity{UserID: actor})
	if err != nil {
		var app *apperrors.AppError
		if errors.As(err, &app) && app.StatusCode == http.StatusForbidden {
			return 0, err
		}
		return 0, sdCounterUnavailable()
	}
	return readSDOpenTicketCount(ctx, strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")), authorization)
}

func readSDOpenTicketCount(ctx context.Context, base string, authorization AIConnectorAuthorization) (int64, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		authorization.Token == "" || authorization.Host == "" || strings.ContainsAny(authorization.Host, "/\\?#@ \r\n\t") {
		return 0, sdCounterUnavailable()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/dashboard/stats?limit=1", nil)
	if err != nil {
		return 0, sdCounterUnavailable()
	}
	req.Host = authorization.Host
	req.Header.Set("Authorization", authorization.Token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return 0, sdCounterUnavailable()
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		return 0, apperrors.Forbidden("SD ticket access denied")
	}
	if response.StatusCode != http.StatusOK {
		return 0, sdCounterUnavailable()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return 0, sdCounterUnavailable()
	}
	var result struct {
		OpenCount *int64 `json:"open_count"`
	}
	if json.Unmarshal(raw, &result) != nil || result.OpenCount == nil || *result.OpenCount < 0 || *result.OpenCount > 9007199254740991 {
		return 0, sdCounterUnavailable()
	}
	return *result.OpenCount, nil
}
