package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

var directoryAttributePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9;-]{0,127}$`)

func normalizeDirectorySettings(settings domain.DirectorySettings) (domain.DirectorySettings, error) {
	settings.Type = strings.ToLower(strings.TrimSpace(settings.Type))
	settings.URL = strings.TrimSpace(settings.URL)
	settings.BaseDN = strings.TrimSpace(settings.BaseDN)
	settings.BindDN = strings.TrimSpace(settings.BindDN)
	settings.Filter = strings.TrimSpace(settings.Filter)
	if settings.Type != "ldap" && settings.Type != "ad" {
		return settings, apperrors.Validation("directory type must be ldap or ad")
	}
	parsed, err := url.Parse(settings.URL)
	if err != nil || (parsed.Scheme != "ldaps" && parsed.Scheme != "ldap") || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return settings, apperrors.Validation("directory URL must be ldaps://host:port or ldap://host:port (StartTLS)")
	}
	port := parsed.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return settings, apperrors.Validation("invalid directory port")
		}
	}
	if settings.BaseDN == "" || settings.BindDN == "" || len(settings.BaseDN) > 4096 || len(settings.BindDN) > 4096 {
		return settings, apperrors.Validation("base_dn and bind_dn are required, maximum 4096 bytes")
	}
	if _, err = ldap.ParseDN(settings.BaseDN); err != nil {
		return settings, apperrors.Validation("invalid base_dn")
	}
	if settings.Filter == "" {
		if settings.Type == "ad" {
			settings.Filter = "(&(objectClass=user)(objectCategory=person))"
		} else {
			settings.Filter = "(objectClass=inetOrgPerson)"
		}
	}
	if len(settings.Filter) > 4096 {
		return settings, apperrors.Validation("directory filter is too long")
	}
	if _, err = ldap.CompileFilter(settings.Filter); err != nil {
		return settings, apperrors.Validation("invalid LDAP filter")
	}
	defaults := map[string]string{"external_id": "entryUUID", "username": "uid", "email": "mail", "first_name": "givenName", "last_name": "sn", "position": "title", "department": "department", "phone": "telephoneNumber", "manager_dn": "manager"}
	if settings.Type == "ad" {
		defaults["external_id"] = "objectGUID"
		defaults["username"] = "sAMAccountName"
		defaults["account_control"] = "userAccountControl"
	}
	for field, attribute := range settings.Attributes {
		if _, ok := defaults[field]; !ok {
			return settings, apperrors.Validation("unknown directory attribute mapping")
		}
		if !directoryAttributePattern.MatchString(attribute) {
			return settings, apperrors.Validation("invalid directory attribute name")
		}
		defaults[field] = attribute
	}
	settings.Attributes = defaults
	if len(settings.CACertPEM) > 256<<10 {
		return settings, apperrors.Validation("CA certificate is too large")
	}
	if settings.CACertPEM != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(settings.CACertPEM)) {
			return settings, apperrors.Validation("invalid CA certificate")
		}
	}
	return settings, nil
}

func directorySecretCipher() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("CONTACTS_DIRECTORY_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, apperrors.Internal("directory encryption key is not configured")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func encryptDirectoryPassword(password, organization string) ([]byte, error) {
	aead, err := directorySecretCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(password), []byte(organization)), nil
}
func decryptDirectoryPassword(encrypted []byte, organization string) (string, error) {
	aead, err := directorySecretCipher()
	if err != nil {
		return "", err
	}
	if len(encrypted) < aead.NonceSize() {
		return "", apperrors.Internal("directory secret is unavailable")
	}
	data, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(organization))
	if err != nil {
		return "", apperrors.Internal("directory secret cannot be decrypted")
	}
	return string(data), nil
}

func (s *OrganizationService) ContactDirectoryConfig(ctx context.Context, actor int64) (*domain.DirectoryConfig, error) {
	config, _, err := s.repo.ContactDirectoryConfig(ctx, actor)
	return config, err
}
func (s *OrganizationService) SaveContactDirectoryConfig(ctx context.Context, actor int64, req domain.DirectoryConfigRequest) (*domain.DirectoryConfig, error) {
	org, err := s.repo.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	settings, err := normalizeDirectorySettings(req.DirectorySettings)
	if err != nil {
		return nil, err
	}
	var encrypted []byte
	if req.BindPassword != nil {
		if *req.BindPassword == "" || len(*req.BindPassword) > 4096 {
			return nil, apperrors.Validation("bind_password must be non-empty, maximum 4096 bytes")
		}
		encrypted, err = encryptDirectoryPassword(*req.BindPassword, org.String())
		if err != nil {
			return nil, err
		}
	}
	return s.repo.SaveContactDirectoryConfig(ctx, actor, settings, encrypted)
}

func (s *OrganizationService) ImportContacts(ctx context.Context, actor int64, filename string, reader io.Reader, dryRun bool) (*domain.ContactImportResult, error) {
	if _, err := s.repo.ContactImportScope(ctx, actor); err != nil {
		return nil, err
	}
	rows, issues, err := ParseContactImport(filename, reader)
	if err != nil {
		return nil, err
	}
	issues = append(issues, ValidateContactImport(rows)...)
	if len(issues) > 0 {
		return &domain.ContactImportResult{DryRun: dryRun, Total: len(rows), Issues: issues}, nil
	}
	return s.repo.ApplyContactImport(ctx, actor, "file", 0, rows, dryRun)
}

func (s *OrganizationService) SyncContactDirectory(ctx context.Context, actor int64, dryRun bool) (*domain.ContactImportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	org, err := s.repo.ContactImportScope(ctx, actor)
	if err != nil {
		return nil, err
	}
	config, encrypted, err := s.repo.ContactDirectoryConfig(ctx, actor)
	if err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, apperrors.Conflict("directory connector is disabled")
	}
	password, err := decryptDirectoryPassword(encrypted, org.String())
	if err != nil {
		return nil, err
	}
	fetch := s.directoryFetch
	if fetch == nil {
		fetch = fetchLDAPContacts
	}
	rows, err := fetch(ctx, config.DirectorySettings, password)
	if err != nil {
		return nil, err
	}
	issues := ValidateContactImport(rows)
	if len(issues) > 0 {
		return &domain.ContactImportResult{DryRun: dryRun, Total: len(rows), Issues: issues}, nil
	}
	return s.repo.ApplyContactImport(ctx, actor, "ldap", config.Revision, rows, dryRun)
}

// fetchLDAPContacts uses encrypted transport, bounded paged reads and no referrals.
func fetchLDAPContacts(ctx context.Context, settings domain.DirectorySettings, password string) ([]domain.ContactImportRow, error) {
	parsed, err := url.Parse(settings.URL)
	if err != nil {
		return nil, apperrors.Validation("invalid directory URL")
	}
	allowed := false
	for _, host := range strings.Split(os.Getenv("CONTACTS_LDAP_ALLOWED_HOSTS"), ",") {
		if strings.EqualFold(strings.TrimSpace(host), parsed.Host) {
			allowed = true
		}
	}
	if !allowed {
		return nil, apperrors.Forbidden("directory host is not in CONTACTS_LDAP_ALLOWED_HOSTS")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if settings.CACertPEM != "" && !roots.AppendCertsFromPEM([]byte(settings.CACertPEM)) {
		return nil, apperrors.Validation("invalid directory CA")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: parsed.Hostname()}
	conn, err := ldap.DialURL(settings.URL, ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}), ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, apperrors.Conflict("directory connection failed")
	}
	defer conn.Close()
	conn.SetTimeout(15 * time.Second)
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopped:
		}
	}()
	if parsed.Scheme == "ldap" {
		if err = conn.StartTLS(tlsConfig); err != nil {
			return nil, apperrors.Conflict("directory TLS negotiation failed")
		}
	}
	if err = conn.Bind(settings.BindDN, password); err != nil {
		return nil, apperrors.Conflict("directory bind failed")
	}
	attrs := []string{}
	for _, attribute := range settings.Attributes {
		attrs = append(attrs, attribute)
	}
	entries := []*ldap.Entry{}
	page := ldap.NewControlPaging(250)
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		request := ldap.NewSearchRequest(settings.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, MaxContactImportRows+1, 15, false, settings.Filter, attrs, []ldap.Control{page})
		response, e := conn.Search(request)
		if e != nil {
			return nil, apperrors.Conflict("directory search incomplete or failed; no employees applied")
		}
		if len(response.Referrals) > 0 {
			return nil, apperrors.Conflict("directory returned referrals; select a complete search base")
		}
		entries = append(entries, response.Entries...)
		if len(entries) > MaxContactImportRows {
			return nil, apperrors.Validation("directory exceeds 5000 employees per sync")
		}
		control := ldap.FindControl(response.Controls, ldap.ControlTypePaging)
		if control == nil {
			return nil, apperrors.Conflict("directory does not support paged results")
		}
		paging, ok := control.(*ldap.ControlPaging)
		if !ok {
			return nil, apperrors.Conflict("invalid directory paging response")
		}
		if len(paging.Cookie) == 0 {
			break
		}
		page.SetCookie(paging.Cookie)
	}
	return directoryEntriesToContacts(entries, settings)
}

func directoryEntriesToContacts(entries []*ldap.Entry, settings domain.DirectorySettings) ([]domain.ContactImportRow, error) {
	usernames := map[string]string{}
	for _, entry := range entries {
		dn, err := ldap.ParseDN(entry.DN)
		if err != nil {
			return nil, apperrors.Validation("invalid employee DN")
		}
		usernames[strings.ToLower(dn.String())] = entry.GetAttributeValue(settings.Attributes["username"])
	}
	rows := []domain.ContactImportRow{}
	for i, entry := range entries {
		get := func(field string) string {
			return strings.TrimSpace(entry.GetAttributeValue(settings.Attributes[field]))
		}
		optional := func(field string) *string { value := get(field); return &value }
		external := get("external_id")
		if settings.Type == "ad" && strings.EqualFold(settings.Attributes["external_id"], "objectGUID") {
			raw := entry.GetRawAttributeValue(settings.Attributes["external_id"])
			if len(raw) != 16 {
				return nil, apperrors.Validation("AD entry has no valid objectGUID")
			}
			external = hex.EncodeToString(raw)
		}
		row := domain.ContactImportRow{Row: i + 1, ExternalID: external, Username: get("username"), Email: get("email"), FirstName: get("first_name"), LastName: get("last_name"), Position: optional("position"), Department: optional("department"), Phone: optional("phone")}
		manager := ""
		if raw := get("manager_dn"); raw != "" {
			dn, e := ldap.ParseDN(raw)
			if e != nil {
				return nil, apperrors.Validation("invalid manager DN")
			}
			manager = usernames[strings.ToLower(dn.String())]
			if manager == "" {
				return nil, apperrors.Validation("manager is outside directory search results")
			}
		}
		row.ManagerUsername = &manager
		if settings.Type == "ad" {
			flags, e := strconv.Atoi(get("account_control"))
			if e != nil {
				return nil, apperrors.Validation("invalid AD userAccountControl")
			}
			active := flags&2 == 0
			row.IsActive = &active
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *OrganizationService) ContactImportScope(ctx context.Context, actor int64) (uuid.UUID, error) {
	return s.repo.ContactImportScope(ctx, actor)
}
