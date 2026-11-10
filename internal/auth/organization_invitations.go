package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"math/big"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type invitationMailer interface {
	SendEmail(context.Context, string, string, string, string) error
}
type invitationIdentityAdmin interface {
	EnsureInvitationProfileAttributes(context.Context, string) error
	GetUsersByEmail(context.Context, string, string) ([]keycloak.UserRepresentation, error)
	GetUserByID(context.Context, string, string) (*keycloak.UserRepresentation, error)
	CreateUser(context.Context, string, keycloak.CreateUserRequest) (string, error)
	SetPassword(context.Context, string, string, string, bool) error
	SetUserRealmRoles(context.Context, string, string, []string) error
	UpdateUser(context.Context, string, string, keycloak.UserUpdateRequest) error
}

type InvitationService struct {
	repo         *repository.OrganizationRepository
	admin        invitationIdentityAdmin
	realm        string
	mailer       invitationMailer
	joinURL      string
	logger       *zap.Logger
	profileMu    sync.Mutex
	profileReady bool
}

func (s *InvitationService) ensureProfile(ctx context.Context) error {
	s.profileMu.Lock()
	defer s.profileMu.Unlock()
	if s.profileReady {
		return nil
	}
	if err := s.admin.EnsureInvitationProfileAttributes(ctx, s.realm); err != nil {
		return apperrors.InternalWithErr("Failed to configure invitation identity attributes", err)
	}
	s.profileReady = true
	return nil
}

func NewInvitationService(repo *repository.OrganizationRepository, identity *Service, mailer invitationMailer, publicURL string, logger *zap.Logger) *InvitationService {
	s := &InvitationService{repo: repo, mailer: mailer, logger: logger}
	if identity != nil && identity.kcAdmin != nil {
		s.admin = identity.kcAdmin
		s.realm = identity.realm
	}
	// Only the configured public origin is trusted; never use Host/Origin supplied by a request.
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		u.Path = "/web/join"
		u.RawPath = ""
		u.RawQuery = ""
		u.Fragment = ""
		s.joinURL = u.String()
	}
	return s
}

func invitationHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func invitationTokenHash(token string) (string, error) {
	value, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(value) != 32 {
		return "", apperrors.NotFound("invitation")
	}
	return invitationHash(token), nil
}
func invitationCodeHash(token, code string) (string, error) {
	if len(code) != 6 {
		return "", apperrors.Validation("Введите шестизначный код")
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return "", apperrors.Validation("Введите шестизначный код")
		}
	}
	return invitationHash(token + ":" + code), nil
}

type IssuedInvitation struct {
	Invitation *domain.OrganizationInvitation `json:"invitation"`
	URL        string                         `json:"url"`
}

func (s *InvitationService) List(ctx context.Context, actor int64, org *uuid.UUID, limit, offset int) (*domain.InvitationPage, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListInvitations(ctx, actor, org, limit, offset)
}
func (s *InvitationService) Issue(ctx context.Context, actor int64, org, id *uuid.UUID, req domain.CreateInvitationRequest) (*IssuedInvitation, error) {
	if s.joinURL == "" || s.mailer == nil {
		return nil, apperrors.Conflict("Не настроена отправка приглашений")
	}
	if id == nil {
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		parsed, err := mail.ParseAddress(req.Email)
		if err != nil || parsed.Address != req.Email || len(req.Email) > 254 {
			return nil, apperrors.Validation("Укажите корректный email")
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	link := s.joinURL + "#token=" + token
	item, err := s.repo.IssueInvitation(ctx, actor, org, id, req, invitationHash(token), func(i *domain.OrganizationInvitation) error {
		text := fmt.Sprintf("Вас приглашают в %s. Откройте ссылку и подтвердите почту: %s\nСсылка действует 7 дней. Если вы не ожидали приглашение, проигнорируйте письмо.", i.OrganizationName, link)
		body := "<p>Вас приглашают в " + html.EscapeString(i.OrganizationName) + ".</p><p><a href=\"" + html.EscapeString(link) + "\">Открыть приглашение</a></p><p>Ссылка действует 7 дней. Для подключения понадобится подтверждение этой почты.</p>"
		return s.send(ctx, i.Email, "Приглашение в организацию", body, text)
	})
	if err != nil {
		return nil, err
	}
	return &IssuedInvitation{Invitation: item, URL: link}, nil
}
func (s *InvitationService) Revoke(ctx context.Context, actor int64, org *uuid.UUID, id uuid.UUID) error {
	return s.repo.RevokeInvitation(ctx, actor, org, id)
}

type InvitationPreview struct {
	OrganizationName string    `json:"organization_name"`
	EmailHint        string    `json:"email_hint"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func (s *InvitationService) Preview(ctx context.Context, token string) (*InvitationPreview, error) {
	hash, err := invitationTokenHash(token)
	if err != nil {
		return nil, err
	}
	i, err := s.repo.InvitationPreview(ctx, hash)
	if err != nil {
		return nil, err
	}
	local, domainPart, _ := strings.Cut(i.Email, "@")
	prefix := []rune(local)
	hint := "***@" + domainPart
	if len(prefix) > 0 {
		hint = string(prefix[:1]) + hint
	}
	return &InvitationPreview{OrganizationName: i.OrganizationName, EmailHint: hint, ExpiresAt: i.ExpiresAt}, nil
}
func (s *InvitationService) SendCode(ctx context.Context, token string) error {
	hash, err := invitationTokenHash(token)
	if err != nil {
		return err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	codeHash, _ := invitationCodeHash(token, code)
	return s.repo.SendInvitationCode(ctx, hash, codeHash, func(i *domain.OrganizationInvitation) error {
		return s.send(ctx, i.Email, "Подтверждение приглашения", "<p>Код подтверждения: <strong>"+code+"</strong></p><p>Действует 10 минут. Никому не сообщайте код.</p>", "Код подтверждения: "+code+". Действует 10 минут. Никому не сообщайте код.")
	})
}
func (s *InvitationService) send(ctx context.Context, email, subject, body, text string) error {
	if s.mailer == nil {
		return apperrors.Conflict("Не настроена отправка писем")
	}
	if err := s.mailer.SendEmail(ctx, email, subject, body, text); err != nil {
		s.logger.Warn("Invitation email delivery failed", zap.Error(err))
		return apperrors.New("INVITATION_EMAIL_UNAVAILABLE", "Не удалось отправить письмо. Повторите попытку позже", 503)
	}
	return nil
}

type InvitationRegistrationRequest struct {
	Token     string `json:"token" binding:"required"`
	Code      string `json:"code" binding:"required,len=6"`
	Username  string `json:"username" binding:"required,min=3,max=50"`
	Password  string `json:"password" binding:"required,min=8,max=128"`
	FirstName string `json:"first_name" binding:"required,min=1,max=50"`
	LastName  string `json:"last_name" binding:"required,min=1,max=50"`
	IIN       string `json:"iin" binding:"required,len=12"`
}

func (s *InvitationService) Register(ctx context.Context, req InvitationRegistrationRequest) (*domain.InvitationResult, error) {
	hash, err := invitationTokenHash(req.Token)
	if err != nil {
		return nil, err
	}
	codeHash, err := invitationCodeHash(req.Token, req.Code)
	if err != nil {
		return nil, err
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	if len(req.Username) < 3 || len(req.Username) > 50 || strings.ContainsAny(req.Username, " \t\r\n") || req.FirstName == "" || req.LastName == "" || len([]rune(req.FirstName)) > 50 || len([]rune(req.LastName)) > 50 || len(req.Password) < 8 || len(req.Password) > 128 || len(req.IIN) != 12 {
		return nil, apperrors.Validation("Проверьте данные регистрации")
	}
	for _, c := range req.IIN {
		if c < '0' || c > '9' {
			return nil, apperrors.Validation("ИИН должен содержать 12 цифр")
		}
	}
	if s.admin == nil {
		return nil, apperrors.Conflict("Не настроена регистрация в системе входа")
	}
	user := &domain.User{Username: req.Username, FirstName: req.FirstName, LastName: req.LastName, IIN: &req.IIN, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	result, err := s.repo.AcceptInvitation(ctx, hash, codeHash, 0, user, func(i *domain.OrganizationInvitation, u *domain.User) error {
		if err := s.ensureProfile(ctx); err != nil {
			return err
		}
		// An unfinished disabled identity belongs to this invitation only when its server-set marker matches.
		// Never adopt a pre-existing account merely because its email or IIN matches.
		existing, e := s.admin.GetUsersByEmail(ctx, s.realm, u.Email)
		if e != nil {
			return apperrors.InternalWithErr("Failed to inspect invitation identity", e)
		}
		kcID := ""
		for _, item := range existing {
			marker := item.Attributes["workspace_invitation_id"]
			if len(marker) != 1 || !slices.Contains(i.ResumableIDs, marker[0]) || item.Enabled {
				return apperrors.InvitationIdentityConflict()
			}
			if item.Username != u.Username {
				return apperrors.New("INVITATION_REGISTRATION_LOGIN", "Для продолжения регистрации используйте первоначальный логин", 409).WithDetails(map[string]any{"username": item.Username})
			}
			kcID = item.ID
		}
		attributes := map[string][]string{"iin": {req.IIN}, "workspace_invitation_id": {i.ID.String()}}
		if kcID == "" {
			kcID, e = s.admin.CreateUser(ctx, s.realm, keycloak.CreateUserRequest{Username: u.Username, Email: u.Email, Enabled: false, EmailVerified: true, FirstName: u.FirstName, LastName: u.LastName, Attributes: attributes})
			if e != nil {
				if strings.Contains(e.Error(), "status=409") {
					return apperrors.InvitationIdentityConflict()
				}
				return apperrors.InternalWithErr("Failed to provision invitation identity", e)
			}
		} else {
			// This is still an unfinished, disabled identity, never an existing member.
			verified, disabled := true, false
			if e = s.admin.UpdateUser(ctx, s.realm, kcID, keycloak.UserUpdateRequest{Username: &u.Username, Email: &u.Email, EmailVerified: &verified, Enabled: &disabled, FirstName: &u.FirstName, LastName: &u.LastName, Attributes: attributes}); e != nil {
				return apperrors.InternalWithErr("Failed to resume invitation identity", e)
			}
		}
		if e = s.admin.SetPassword(ctx, s.realm, kcID, req.Password, false); e != nil {
			if strings.Contains(e.Error(), "status=400") {
				return apperrors.New(apperrors.ErrCodeInvitationPasswordPolicy, "password policy rejected", 400)
			}
			return apperrors.InternalWithErr("Failed to set invitation password", e)
		}
		if e = s.admin.SetUserRealmRoles(ctx, s.realm, kcID, []string{"user"}); e != nil {
			return apperrors.InternalWithErr("Failed to assign invitation role", e)
		}
		u.KeycloakID = &kcID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = s.activate(ctx, hash, result.UserID); err != nil {
		return nil, apperrors.New("INVITATION_ACTIVATION_PENDING", "Аккаунт создан. Сервис входа временно недоступен; активация будет повторена автоматически. Попробуйте войти позже", 503)
	}
	return result, nil
}
func (s *InvitationService) Accept(ctx context.Context, token, code string, actor int64) (*domain.InvitationResult, error) {
	if actor <= 0 {
		return nil, apperrors.Unauthorized("Войдите в аккаунт")
	}
	hash, err := invitationTokenHash(token)
	if err != nil {
		return nil, err
	}
	codeHash, err := invitationCodeHash(token, code)
	if err != nil {
		return nil, err
	}
	return s.repo.AcceptInvitation(ctx, hash, codeHash, actor, nil, nil)
}
func (s *InvitationService) activate(ctx context.Context, hash string, id int64) error {
	return s.repo.ActivateInvitationAccount(ctx, hash, id, func(kcID string) error {
		user, err := s.admin.GetUserByID(ctx, s.realm, kcID)
		if err != nil {
			return err
		}
		enabled := true
		// Keycloak's user PUT can clear an omitted email. Preserve the complete identity.
		return s.admin.UpdateUser(ctx, s.realm, kcID, keycloak.UserUpdateRequest{Enabled: &enabled, Username: &user.Username, Email: &user.Email, EmailVerified: &user.EmailVerified, FirstName: &user.FirstName, LastName: &user.LastName, Attributes: user.Attributes})
	})
}
func (s *InvitationService) StartActivationWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				workCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				pending, err := s.repo.PendingInvitationActivations(workCtx)
				if err == nil && s.admin != nil {
					for hash, id := range pending {
						if err = s.activate(workCtx, hash, id); err != nil {
							s.logger.Warn("Invitation activation retry failed", zap.Int64("user_id", id), zap.Error(err))
						}
					}
				}
				cancel()
			}
		}
	}()
}
