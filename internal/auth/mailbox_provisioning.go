package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type MailboxRepository interface {
	GetPrimaryMailboxLogin(ctx context.Context, userID int64) (string, error)
	UpsertPrimaryMailboxLogin(ctx context.Context, userID int64, mailboxLogin string) error
}

type MailboxProvisioner interface {
	ProvisionPrimaryMailbox(ctx context.Context, user *domain.UserResponse) (string, error)
}

type MailcowProvisionerConfig struct {
	BaseURL             string
	APIKey              string
	DefaultDomain       string
	DefaultQuotaMB      int
	TLSEnforceIn        bool
	TLSEnforceOut       bool
	ForcePasswordUpdate bool
	InsecureSkipVerify  bool
	RequestTimeout      time.Duration
	MaxSuffixAttempts   int
}

type MailcowMailboxProvisioner struct {
	cfg        MailcowProvisionerConfig
	httpClient *http.Client
	logger     *zap.Logger
}

type mailcowMailboxRequest struct {
	Active          string   `json:"active"`
	Domain          string   `json:"domain"`
	LocalPart       string   `json:"local_part"`
	Name            string   `json:"name"`
	Password        string   `json:"password"`
	Password2       string   `json:"password2"`
	Quota           string   `json:"quota"`
	ForcePWUpdate   string   `json:"force_pw_update"`
	TLSEnforceIn    string   `json:"tls_enforce_in"`
	TLSEnforceOut   string   `json:"tls_enforce_out"`
	Tags            []string `json:"tags,omitempty"`
	ActiveInt       string   `json:"-"` // keep payload explicit and stable
	ForcePWUpdateV2 string   `json:"-"`
}

func NewMailcowMailboxProvisioner(cfg MailcowProvisionerConfig, logger *zap.Logger) *MailcowMailboxProvisioner {
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

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &MailcowMailboxProvisioner{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:   cfg.RequestTimeout,
			Transport: transport,
		},
		logger: logger.With(zap.String("component", "mailcow_mailbox_provisioner")),
	}
}

func (p *MailcowMailboxProvisioner) Enabled() bool {
	if p == nil {
		return false
	}
	return strings.TrimSpace(p.cfg.BaseURL) != "" &&
		strings.TrimSpace(p.cfg.APIKey) != "" &&
		strings.TrimSpace(p.cfg.DefaultDomain) != ""
}

func (p *MailcowMailboxProvisioner) ProvisionPrimaryMailbox(ctx context.Context, user *domain.UserResponse) (string, error) {
	if user == nil {
		return "", apperrors.Internal("mailbox provisioning requires a user")
	}
	if !p.Enabled() {
		return "", apperrors.Internal("mailcow mailbox provisioning is not configured")
	}

	baseLocalPart := candidateLocalPart(user)
	displayName := mailboxDisplayName(user)

	for suffix := 0; suffix <= p.cfg.MaxSuffixAttempts; suffix++ {
		localPart := mailboxCandidate(baseLocalPart, suffix)
		mailboxLogin, created, err := p.tryCreateMailbox(ctx, localPart, displayName)
		if err != nil {
			var conflictErr *mailcowConflictError
			if ok := AsMailcowConflictError(err, &conflictErr); ok {
				continue
			}
			return "", err
		}
		if created {
			p.logger.Info("Provisioned mailcow mailbox",
				zap.Int64("user_id", user.ID),
				zap.String("mailbox_login", mailboxLogin),
				zap.String("source_email", user.Email),
			)
			return mailboxLogin, nil
		}
	}

	return "", apperrors.Internal("failed to allocate unique mailbox login")
}

func (p *MailcowMailboxProvisioner) tryCreateMailbox(ctx context.Context, localPart, displayName string) (string, bool, error) {
	password, err := generateMailboxPassword()
	if err != nil {
		return "", false, apperrors.InternalWithErr("failed to generate mailbox password", err)
	}

	payload := mailcowMailboxRequest{
		Active:        "1",
		Domain:        strings.TrimSpace(p.cfg.DefaultDomain),
		LocalPart:     localPart,
		Name:          displayName,
		Password:      password,
		Password2:     password,
		Quota:         fmt.Sprintf("%d", p.cfg.DefaultQuotaMB),
		ForcePWUpdate: boolToMailcowFlag(p.cfg.ForcePasswordUpdate),
		TLSEnforceIn:  boolToMailcowFlag(p.cfg.TLSEnforceIn),
		TLSEnforceOut: boolToMailcowFlag(p.cfg.TLSEnforceOut),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, apperrors.InternalWithErr("failed to marshal mailcow mailbox request", err)
	}

	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/api/v1/add/mailbox"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, apperrors.InternalWithErr("failed to build mailcow mailbox request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", strings.TrimSpace(p.cfg.APIKey))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", false, apperrors.InternalWithErr("mailcow mailbox request failed", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, apperrors.InternalWithErr("failed to read mailcow mailbox response", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", false, apperrors.Internal("mailcow mailbox request returned unexpected status").
			WithDetail("status_code", resp.StatusCode).
			WithDetail("response", strings.TrimSpace(string(respBody)))
	}

	resultType, resultMsg := parseMailcowResponse(respBody)
	if strings.EqualFold(resultType, "success") {
		return fmt.Sprintf("%s@%s", localPart, payload.Domain), true, nil
	}

	if looksLikeMailboxConflict(resultMsg) {
		return "", false, newMailcowConflictError(resultMsg)
	}

	return "", false, apperrors.Internal("mailcow mailbox creation failed").
		WithDetail("response_type", resultType).
		WithDetail("response", resultMsg)
}

type mailcowConflictError struct {
	msg string
}

func (e *mailcowConflictError) Error() string { return e.msg }

func newMailcowConflictError(msg string) error {
	return &mailcowConflictError{msg: msg}
}

func AsMailcowConflictError(err error, target **mailcowConflictError) bool {
	if err == nil {
		return false
	}
	conflictErr, ok := err.(*mailcowConflictError)
	if !ok {
		return false
	}
	if target != nil {
		*target = conflictErr
	}
	return true
}

func parseMailcowResponse(body []byte) (string, string) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", strings.TrimSpace(string(body))
	}

	switch typed := payload.(type) {
	case []any:
		parts := make([]string, 0, len(typed))
		responseType := ""
		for _, item := range typed {
			itemType, itemMsg := parseMailcowResponseObject(item)
			if responseType == "" && itemType != "" {
				responseType = itemType
			}
			if itemMsg != "" {
				parts = append(parts, itemMsg)
			}
		}
		return responseType, strings.Join(parts, "; ")
	default:
		return parseMailcowResponseObject(payload)
	}
}

func parseMailcowResponseObject(payload any) (string, string) {
	obj, ok := payload.(map[string]any)
	if !ok {
		return "", flattenMailcowValue(payload)
	}
	responseType, _ := obj["type"].(string)
	return responseType, flattenMailcowValue(obj["msg"])
}

func flattenMailcowValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			part := flattenMailcowValue(item)
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func looksLikeMailboxConflict(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	for _, token := range []string{"exists", "already", "duplicate", "in use", "taken"} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func mailboxDisplayName(user *domain.UserResponse) string {
	if user == nil {
		return "Mailbox User"
	}
	if name := strings.TrimSpace(user.FullName); name != "" {
		return name
	}
	if name := strings.TrimSpace(strings.TrimSpace(user.FirstName + " " + user.LastName)); name != "" {
		return name
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		return username
	}
	return strings.TrimSpace(user.Email)
}

func candidateLocalPart(user *domain.UserResponse) string {
	if user == nil {
		return "user"
	}

	if local := normalizeMailboxLocalPart(emailLocalPart(user.Email)); local != "" {
		return local
	}
	if local := normalizeMailboxLocalPart(strings.TrimSpace(user.Username)); local != "" {
		return local
	}
	return fmt.Sprintf("user%d", user.ID)
}

func mailboxCandidate(base string, suffix int) string {
	base = normalizeMailboxLocalPart(base)
	if base == "" {
		base = "user"
	}
	if suffix <= 0 {
		return trimMailboxLocalPart(base, 64)
	}
	suffixPart := fmt.Sprintf("_%d", suffix)
	maxBaseLen := 64 - len(suffixPart)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}
	return trimMailboxLocalPart(base, maxBaseLen) + suffixPart
}

func emailLocalPart(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))
	if idx := strings.Index(email, "@"); idx > 0 {
		return email[:idx]
	}
	return email
}

func normalizeMailboxLocalPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var b strings.Builder
	lastSep := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSep = false
		case r == '.' || r == '_' || r == '-':
			if !lastSep && b.Len() > 0 {
				b.WriteRune(r)
				lastSep = true
			}
		default:
			if !lastSep && b.Len() > 0 {
				b.WriteRune('_')
				lastSep = true
			}
		}
	}

	cleaned := strings.Trim(b.String(), "._-")
	return trimMailboxLocalPart(cleaned, 64)
}

func trimMailboxLocalPart(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return strings.Trim(value, "._-")
	}
	return strings.Trim(value[:maxLen], "._-")
}

func generateMailboxPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func boolToMailcowFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
