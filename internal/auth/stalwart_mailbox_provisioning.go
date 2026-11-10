package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// StalwartProvisionerConfig contains backend-only credentials for the
// authenticated Stalwart management API. It is never sent to the frontend.
type StalwartProvisionerConfig struct {
	BaseURL           string
	PublicURL         string
	AdminUser         string
	AdminPassword     string
	DefaultDomain     string
	DefaultQuotaMB    int
	RequestTimeout    time.Duration
	MaxSuffixAttempts int
}

type StalwartMailboxProvisioner struct {
	cfg        StalwartProvisionerConfig
	httpClient *http.Client
	logger     *zap.Logger
}

type stalwartMethodResponse struct {
	Name string
	Args map[string]any
}

func NewStalwartMailboxProvisioner(cfg StalwartProvisionerConfig, logger *zap.Logger) *StalwartMailboxProvisioner {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.DefaultQuotaMB <= 0 {
		cfg.DefaultQuotaMB = 3072
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 15 * time.Second
	}
	if cfg.MaxSuffixAttempts <= 0 {
		cfg.MaxSuffixAttempts = 100
	}

	return &StalwartMailboxProvisioner{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.RequestTimeout},
		logger:     logger.With(zap.String("component", "stalwart_mailbox_provisioner")),
	}
}

func (p *StalwartMailboxProvisioner) Enabled() bool {
	if p == nil {
		return false
	}
	return strings.TrimSpace(p.cfg.BaseURL) != "" &&
		strings.TrimSpace(p.cfg.AdminUser) != "" &&
		strings.TrimSpace(p.cfg.AdminPassword) != "" &&
		strings.TrimSpace(p.cfg.DefaultDomain) != ""
}

func (p *StalwartMailboxProvisioner) ProvisionPrimaryMailbox(ctx context.Context, user *domain.UserResponse) (string, error) {
	if user == nil {
		return "", apperrors.Internal("mailbox provisioning requires a user")
	}
	if !p.Enabled() {
		return "", apperrors.Internal("stalwart mailbox provisioning is not configured")
	}

	domainName := strings.ToLower(strings.TrimSpace(p.cfg.DefaultDomain))
	domainID, err := p.findDomainID(ctx, domainName)
	if err != nil {
		return "", err
	}

	baseLocalPart := candidateLocalPart(user)
	displayName := mailboxDisplayName(user)
	for suffix := 0; suffix <= p.cfg.MaxSuffixAttempts; suffix++ {
		localPart := mailboxCandidate(baseLocalPart, suffix)
		mailboxLogin, created, err := p.tryCreateMailbox(ctx, domainID, domainName, localPart, displayName)
		if err != nil {
			var conflictErr *stalwartConflictError
			if AsStalwartConflictError(err, &conflictErr) {
				continue
			}
			return "", err
		}
		if created {
			p.logger.Info("Provisioned Stalwart mailbox",
				zap.Int64("user_id", user.ID),
				zap.String("mailbox_login", mailboxLogin),
				zap.String("source_email", user.Email),
			)
			return mailboxLogin, nil
		}
	}

	return "", apperrors.Internal("failed to allocate unique mailbox login")
}

func (p *StalwartMailboxProvisioner) findDomainID(ctx context.Context, domainName string) (string, error) {
	responses, err := p.jmap(ctx, []any{
		"x:Domain/query",
		map[string]any{"filter": map[string]any{"name": domainName}, "limit": 1},
		"domain-query",
	})
	if err != nil {
		return "", err
	}
	if len(responses) == 0 {
		return "", apperrors.Internal("Stalwart returned no domain query response")
	}

	var result struct {
		IDs []string `json:"ids"`
	}
	if err := decodeStalwartMap(responses[0].Args, &result); err != nil {
		return "", apperrors.InternalWithErr("failed to decode Stalwart domain response", err)
	}
	if len(result.IDs) == 0 || strings.TrimSpace(result.IDs[0]) == "" {
		return "", apperrors.Internal("Stalwart domain is not configured").WithDetail("domain", domainName)
	}
	return result.IDs[0], nil
}

func (p *StalwartMailboxProvisioner) tryCreateMailbox(ctx context.Context, domainID, domainName, localPart, displayName string) (string, bool, error) {
	password, err := generateMailboxPassword()
	if err != nil {
		return "", false, apperrors.InternalWithErr("failed to generate mailbox password", err)
	}

	account := map[string]any{
		"@type":            "User",
		"aliases":          map[string]any{},
		"credentials":      map[string]any{"0": map[string]any{"@type": "Password", "secret": password}},
		"domainId":         domainID,
		"encryptionAtRest": map[string]any{"@type": "Disabled"},
		"memberGroupIds":   map[string]any{},
		"name":             localPart,
		"permissions":      map[string]any{"@type": "Inherit"},
		"quotas":           map[string]any{"maxDiskQuota": p.cfg.DefaultQuotaMB * 1024 * 1024},
		"roles":            map[string]any{"@type": "User"},
		"description":      displayName,
	}

	responses, err := p.jmap(ctx, []any{
		"x:Account/set",
		map[string]any{"create": map[string]any{"new-mailbox": account}},
		"account-set",
	})
	if err != nil {
		if looksLikeMailboxConflict(err.Error()) {
			return "", false, newStalwartConflictError(err.Error())
		}
		return "", false, err
	}
	if len(responses) == 0 {
		return "", false, apperrors.Internal("Stalwart returned no account creation response")
	}

	if notCreated, ok := responses[0].Args["notCreated"].(map[string]any); ok {
		if failure, exists := notCreated["new-mailbox"]; exists {
			message := flattenMailcowValue(failure)
			if looksLikeMailboxConflict(message) {
				return "", false, newStalwartConflictError(message)
			}
			return "", false, apperrors.Internal("Stalwart mailbox creation failed").WithDetail("response", message)
		}
	}

	created, ok := responses[0].Args["created"].(map[string]any)
	if !ok || created["new-mailbox"] == nil {
		return "", false, apperrors.Internal("Stalwart did not confirm mailbox creation")
	}
	return fmt.Sprintf("%s@%s", localPart, domainName), true, nil
}

func (p *StalwartMailboxProvisioner) jmap(ctx context.Context, calls ...[]any) ([]stalwartMethodResponse, error) {
	payload := map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:stalwart:jmap"},
		"methodCalls": calls,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to marshal Stalwart request", err)
	}

	url := strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/") + "/jmap/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to build Stalwart request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(p.cfg.AdminUser), p.cfg.AdminPassword)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, apperrors.InternalWithErr("Stalwart request failed", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to read Stalwart response", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, apperrors.Internal("Stalwart request returned unexpected status").
			WithDetail("status_code", resp.StatusCode).
			WithDetail("response", strings.TrimSpace(string(responseBody)))
	}

	var envelope struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, apperrors.InternalWithErr("failed to decode Stalwart response", err)
	}

	responses := make([]stalwartMethodResponse, 0, len(envelope.MethodResponses))
	for _, rawCall := range envelope.MethodResponses {
		if len(rawCall) < 2 {
			return nil, apperrors.Internal("Stalwart returned a malformed JMAP response")
		}
		var name string
		if err := json.Unmarshal(rawCall[0], &name); err != nil {
			return nil, apperrors.InternalWithErr("failed to decode Stalwart response method", err)
		}
		args := map[string]any{}
		if err := json.Unmarshal(rawCall[1], &args); err != nil {
			return nil, apperrors.InternalWithErr("failed to decode Stalwart response arguments", err)
		}
		if name == "error" || strings.HasSuffix(name, "/error") {
			return nil, apperrors.Internal("Stalwart management API returned an error").WithDetail("response", flattenMailcowValue(args))
		}
		responses = append(responses, stalwartMethodResponse{Name: name, Args: args})
	}
	return responses, nil
}

func decodeStalwartMap(value map[string]any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

type stalwartConflictError struct{ msg string }

func (e *stalwartConflictError) Error() string { return e.msg }

func newStalwartConflictError(msg string) error { return &stalwartConflictError{msg: msg} }

func AsStalwartConflictError(err error, target **stalwartConflictError) bool {
	if err == nil {
		return false
	}
	conflictErr, ok := err.(*stalwartConflictError)
	if !ok {
		return false
	}
	if target != nil {
		*target = conflictErr
	}
	return true
}
