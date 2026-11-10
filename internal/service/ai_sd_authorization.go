package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// The shared key authenticates Workspace, while the verified profile resolves
// the actual SD user and company. No privileged SD service account is used.
func (s *OrganizationService) AIReadSDAuthorization(ctx context.Context, identity AIDataIdentity) (AIConnectorAuthorization, error) {
	return s.workspaceSDUserAuthorization(ctx, identity, false)
}

func (s *OrganizationService) IFormSDAuthorization(ctx context.Context, actor int64) (AIConnectorAuthorization, error) {
	return s.workspaceSDUserAuthorization(ctx, AIDataIdentity{UserID: actor}, true)
}

func (s *OrganizationService) workspaceSDUserAuthorization(ctx context.Context, identity AIDataIdentity, automation bool) (AIConnectorAuthorization, error) {
	profile, err := s.repo.SDHandoffProfile(ctx, identity.UserID)
	if err != nil {
		return AIConnectorAuthorization{}, err
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")), "/")
	token := strings.TrimSpace(os.Getenv("SD_ACCESS_INTERNAL_TOKEN"))
	if base == "" || token == "" {
		return AIConnectorAuthorization{}, fmt.Errorf("SD integration is not configured")
	}
	payload, err := json.Marshal(map[string]any{"profile": profile})
	if err != nil {
		return AIConnectorAuthorization{}, err
	}
	endpoint := "/api/internal/workspace/ai-read-token"
	if automation {
		endpoint = "/api/internal/workspace/iform-token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+endpoint, bytes.NewReader(payload))
	if err != nil {
		return AIConnectorAuthorization{}, err
	}
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return AIConnectorAuthorization{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AIConnectorAuthorization{}, fmt.Errorf("SD user access unavailable")
	}
	var result struct {
		Token string `json:"access_token"`
		Host  string `json:"host"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result) != nil || result.Token == "" || result.Host == "" {
		return AIConnectorAuthorization{}, fmt.Errorf("invalid SD authorization response")
	}
	return AIConnectorAuthorization{Token: "Bearer " + result.Token, Host: result.Host}, nil
}
