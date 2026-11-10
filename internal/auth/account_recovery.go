package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type recoveryAdmin interface {
	SetPassword(context.Context, string, string, string, bool) error
	LogoutUser(context.Context, string, string) error
}
type recoveryPasswordClient interface {
	Password(context.Context, string, string) (*keycloak.TokenResponse, error)
}
type RecoveryService struct {
	clientIDs     []string
	repo          *repository.RecoveryRepository
	admin         recoveryAdmin
	client        recoveryPasswordClient
	realm         string
	mailer        invitationMailer
	url           string
	tokens        *TokenManager
	closeSessions func(int64)
	logger        *zap.Logger
}

func NewRecoveryService(repo *repository.RecoveryRepository, identity *Service, mailer invitationMailer, publicURL string, logger *zap.Logger) *RecoveryService {
	s := &RecoveryService{repo: repo, mailer: mailer, logger: logger}
	if identity != nil {
		if identity.kcAdmin != nil {
			s.admin = identity.kcAdmin
		}
		if identity.kc != nil {
			s.client = identity.kc
		}
		s.realm = identity.realm
		for id := range identity.kcClients {
			s.clientIDs = append(s.clientIDs, id)
		}
		sort.Strings(s.clientIDs)
		s.tokens = identity.tokenManager
		identity.recovery = s
	}
	if u, e := url.Parse(strings.TrimSpace(publicURL)); e == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		u.Path = "/web/recover"
		u.RawPath = ""
		u.RawQuery = ""
		u.Fragment = ""
		s.url = u.String()
	}
	if s.logger == nil {
		s.logger = zap.NewNop()
	}
	return s
}
func (s *RecoveryService) SetSessionCloser(fn func(int64)) { s.closeSessions = fn }
func (s *RecoveryService) ready() error {
	if s.repo == nil || s.admin == nil || s.client == nil || s.tokens == nil || len(s.tokens.secret) == 0 {
		return apperrors.RecoveryUnavailable()
	}
	return nil
}
func (s *RecoveryService) mailReady() error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.mailer == nil || s.url == "" {
		return apperrors.RecoveryUnavailable()
	}
	if m, ok := s.mailer.(interface{ Enabled() bool }); ok && !m.Enabled() {
		return apperrors.RecoveryUnavailable()
	}
	return nil
}
func recoveryPassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 8 || utf8.RuneCountInString(password) > 128 {
		return apperrors.New("RECOVERY_PASSWORD_POLICY", "Use a password of 8–128 characters that meets your organization's password policy", 400)
	}
	return nil
}
func (s *RecoveryService) verify(ctx context.Context, u *domain.User, password string) error {
	if password == "" || len(password) > 1024 {
		return apperrors.New("RECOVERY_REAUTH_FAILED", "Current password is incorrect", 400)
	}
	tok, err := s.client.Password(ctx, u.Username, password)
	if err != nil {
		var e *keycloak.RequestError
		if errors.As(err, &e) && e.IsInvalidGrant() {
			return apperrors.New("RECOVERY_REAUTH_FAILED", "Current password is incorrect", 400)
		}
		return apperrors.RecoveryUnavailable()
	}
	if tok == nil || u.KeycloakID == nil || extractSubFromJWT(tok.AccessToken) != *u.KeycloakID {
		return apperrors.RecoveryUnavailable()
	}
	// The password grant is used only as proof. Do not leave an additional SSO
	// session behind: log out its refresh token (never log out all user sessions).
	if c, ok := s.client.(interface {
		Logout(context.Context, string) error
	}); ok && tok.RefreshToken != "" {
		_ = c.Logout(ctx, tok.RefreshToken)
	}
	return nil
}
func (s *RecoveryService) State(ctx context.Context, id int64) (*domain.RecoveryEmailState, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.repo.State(ctx, id)
}
func (s *RecoveryService) BeginEmail(ctx context.Context, id int64, email, purpose, password, locale string) (*domain.RecoveryEmailChallenge, error) {
	if err := s.mailReady(); err != nil {
		return nil, err
	}
	if purpose != "bind" && purpose != "remove" {
		return nil, apperrors.Validation("Invalid email action")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if purpose == "bind" {
		p, e := mail.ParseAddress(email)
		if e != nil || p.Address != email || len(email) > 254 {
			return nil, apperrors.Validation("Invalid email")
		}
	}
	if err := s.repo.Rate(ctx, invitationHash("email-reauth:"+strconv.FormatInt(id, 10)), 10, 15*time.Minute); err != nil {
		return nil, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return nil, err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	challenge := uuid.New()
	var out *domain.RecoveryEmailChallenge
	err = s.repo.WithUser(ctx, id, func(c *pgxpool.Conn, u *domain.User) error {
		if err := s.verify(ctx, u, password); err != nil {
			return err
		}
		var err error
		out, err = s.repo.BeginEmail(ctx, c, u, challenge, email, purpose, s.codeHash(challenge, code), func(to string) error {
			text := "Код подтверждения резервной почты: " + code + ". Действует 10 минут. Не сообщайте код другим людям. Если вы не запрашивали изменение, проигнорируйте письмо."
			subject := "Подтверждение резервной почты"
			if locale == "kk" {
				subject = "Резервтік поштаны растау"
				text = "Резервтік поштаны растау коды: " + code + ". 10 минут жарамды. Кодты ешкімге айтпаңыз. Өзгеріс сұратпасаңыз, хатты елемеңіз."
			}
			if e := s.mailer.SendEmail(ctx, to, subject, "<p>"+html.EscapeString(text)+"</p>", text); e != nil {
				return apperrors.RecoveryUnavailable()
			}
			return nil
		})
		return err
	})
	return out, err
}
func (s *RecoveryService) ConfirmEmail(ctx context.Context, userID int64, id uuid.UUID, code, password, locale string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if id == uuid.Nil || len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return apperrors.RecoveryInvalid()
	}
	if err := s.repo.Rate(ctx, invitationHash("email-reauth:"+strconv.FormatInt(userID, 10)), 10, 15*time.Minute); err != nil {
		return err
	}
	return s.repo.WithUser(ctx, userID, func(c *pgxpool.Conn, u *domain.User) error {
		if err := s.verify(ctx, u, password); err != nil {
			return err
		}
		return s.repo.ConfirmEmail(ctx, c, u, id, s.codeHash(id, code), locale)
	})
}
func (s *RecoveryService) RequestPassword(ctx context.Context, login, ip, locale string) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" || len(login) > 254 {
		return apperrors.Validation("Enter your login or email")
	}
	// Both counters are updated for existing and absent accounts alike. A login
	// limit silently suppresses mail; an IP limit applies to the whole endpoint.
	ipErr := s.repo.Rate(ctx, invitationHash("reset-ip:"+ip), 30, 15*time.Minute)
	loginErr := s.repo.Rate(ctx, invitationHash("reset-login:"+login), 5, 15*time.Minute)
	if ipErr != nil {
		return ipErr
	}
	if loginErr != nil {
		var a *apperrors.AppError
		if errors.As(loginErr, &a) && a.Code == "RECOVERY_LIMITED" {
			return nil
		}
		return loginErr
	}
	return s.repo.RequestPassword(ctx, login, locale)
}
func recoveryTokenHash(token string) (string, error) {
	b, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != token {
		return "", apperrors.RecoveryInvalid()
	}
	return invitationHash(token), nil
}
func (s *RecoveryService) Preview(ctx context.Context, token, ip string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.repo.Rate(ctx, invitationHash("reset-proof-ip:"+ip), 60, 15*time.Minute); err != nil {
		return err
	}
	hash, err := recoveryTokenHash(token)
	if err != nil {
		return err
	}
	_, err = s.repo.FindReset(ctx, hash)
	return err
}
func (s *RecoveryService) Complete(ctx context.Context, token, password, ip, locale string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := recoveryPassword(password); err != nil {
		return err
	}
	if err := s.repo.Rate(ctx, invitationHash("reset-proof-ip:"+ip), 60, 15*time.Minute); err != nil {
		return err
	}
	hash, err := recoveryTokenHash(token)
	if err != nil {
		return err
	}
	if err = s.repo.Rate(ctx, invitationHash("reset-proof:"+hash), 10, 15*time.Minute); err != nil {
		return err
	}
	p, err := s.repo.FindReset(ctx, hash)
	if err != nil {
		return err
	}
	p.Locale = locale
	return s.repo.WithUser(ctx, p.UserID, func(c *pgxpool.Conn, u *domain.User) error {
		if err := s.repo.ClaimReset(ctx, c, u, p); err != nil {
			return err
		}
		if s.closeSessions != nil {
			s.closeSessions(u.ID)
		}
		if p.State != "password_set" {
			setErr := s.admin.SetPassword(ctx, s.realm, *u.KeycloakID, password, false)
			if setErr != nil {
				var kcErr *keycloak.RequestError
				policy := errors.As(setErr, &kcErr) && kcErr.StatusCode == 400
				if policy && p.Attempts == 0 {
					if err := s.repo.AbortPolicyFailure(ctx, c, p); err != nil {
						return apperrors.RecoveryPending()
					}
					return apperrors.New("RECOVERY_PASSWORD_POLICY", "Password does not meet your organization's password policy", 400)
				}
				// A previous transport failure may have changed the password already.
				// Password history can reject the retry; successful reauthentication then
				// proves the desired result without persisting a plaintext password.
				if !policy || s.verify(ctx, u, password) != nil {
					return apperrors.RecoveryPending()
				}
			}
			if err := s.repo.PasswordSet(ctx, c, p); err != nil {
				return apperrors.RecoveryPending()
			}
		}
		return s.finish(ctx, c, u, p, locale)
	})
}
func (s *RecoveryService) finish(ctx context.Context, c *pgxpool.Conn, u *domain.User, p *domain.PasswordRecovery, locale string) error {
	var err error
	if admin, ok := s.admin.(interface {
		InvalidateUserSessions(context.Context, string, string, []string) error
	}); ok {
		ids := append([]string(nil), s.clientIDs...)
		if client, ok := s.client.(interface{ ClientID() string }); ok {
			ids = append(ids, client.ClientID())
		}
		err = admin.InvalidateUserSessions(ctx, s.realm, *u.KeycloakID, ids)
	} else {
		err = s.admin.LogoutUser(ctx, s.realm, *u.KeycloakID)
	}
	if err != nil {
		s.logger.Warn("Recovery session invalidation deferred", zap.Int64("user_id", u.ID), zap.Error(err))
		return apperrors.RecoveryPending()
	}
	if s.tokens != nil {
		if err := s.tokens.RevokeAllUserTokens(ctx, u.ID); err != nil {
			s.logger.Warn("Recovery local token revocation deferred", zap.Int64("user_id", u.ID), zap.Error(err))
			return apperrors.RecoveryPending()
		}
	}
	if s.closeSessions != nil {
		s.closeSessions(u.ID)
	}
	if err := s.repo.FinishReset(ctx, c, p, locale); err != nil {
		s.logger.Warn("Recovery completion commit deferred", zap.Int64("user_id", u.ID), zap.Error(err))
		return apperrors.RecoveryPending()
	}
	// JWT timestamps have second precision. Do not report completion before
	// the cutoff: an immediate password login must work on its first attempt.
	if delay := time.Until(p.InvalidBefore); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	return nil
}
func (s *RecoveryService) StartWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		lastCleanup := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			func() {
				jobCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()
				s.work(jobCtx)
				if time.Since(lastCleanup) > time.Hour {
					if err := s.repo.Cleanup(jobCtx); err == nil {
						lastCleanup = time.Now()
					} else {
						s.logger.Warn("Recovery cleanup failed", zap.Error(err))
					}
				}
			}()
		}
	}()
}
func (s *RecoveryService) work(ctx context.Context) {
	if s.ready() != nil {
		return
	}
	pending, err := s.repo.PendingResets(ctx)
	if err != nil {
		s.logger.Warn("Recovery completion lookup failed", zap.Error(err))
		return
	}
	for _, item := range pending {
		p := item
		if err := s.repo.WithUser(ctx, p.UserID, func(c *pgxpool.Conn, u *domain.User) error { return s.finish(ctx, c, u, &p, p.Locale) }); err != nil {
			s.logger.Warn("Recovery completion deferred", zap.Int64("user_id", p.UserID))
		}
	}
	if s.mailReady() != nil {
		return
	}
	ids, err := s.repo.MailJobIDs(ctx)
	if err != nil {
		s.logger.Warn("Recovery mail lookup failed", zap.Error(err))
		return
	}
	for _, id := range ids {
		err = s.repo.WithUser(ctx, id, func(c *pgxpool.Conn, u *domain.User) error {
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				return err
			}
			token := base64.RawURLEncoding.EncodeToString(secret)
			link := s.url + "#token=" + token
			return s.repo.DeliverPassword(ctx, c, u, uuid.New(), invitationHash(token), func(email, locale string) error {
				subject := "Восстановление пароля"
				text := "Для смены пароля откройте ссылку: " + link + "\nСсылка действует 30 минут и используется один раз. Если вы не запрашивали восстановление, проигнорируйте письмо."
				if locale == "kk" {
					subject = "Құпиясөзді қалпына келтіру"
					text = "Құпиясөзді өзгерту үшін сілтемені ашыңыз: " + link + "\nСілтеме 30 минут жарамды және бір рет қолданылады. Қалпына келтіруді сұратпасаңыз, хатты елемеңіз."
				}
				return s.mailer.SendEmail(ctx, email, subject, "<p>"+html.EscapeString(subject)+"</p><p><a href=\""+html.EscapeString(link)+"\">"+html.EscapeString(subject)+"</a></p><p>"+html.EscapeString(text)+"</p>", text)
			})
		})
		if err != nil {
			var appErr *apperrors.AppError
			if errors.As(err, &appErr) && appErr.Code == "RECOVERY_PENDING" {
				continue
			}
			_ = s.repo.FailMailJob(ctx, id)
			s.logger.Warn("Recovery mail delivery deferred", zap.Int64("user_id", id))
		}
	}
	for i := 0; i < 10; i++ {
		if err := s.repo.Notify(ctx, func(email, event, locale string) error {
			subject := "Безопасность аккаунта"
			text := "Резервная почта вашего аккаунта изменена. Если это сделали не вы, обратитесь к администратору."
			if event == "recovery_email_removed" {
				text = "Резервная почта вашего аккаунта отвязана. Если это сделали не вы, обратитесь к администратору."
			}
			if event == "password_recovered" {
				text = "Пароль вашего аккаунта восстановлен через резервную почту. Старые сеансы завершены. Если это сделали не вы, срочно обратитесь к администратору."
			}
			if locale == "kk" {
				subject = "Аккаунт қауіпсіздігі"
				text = "Аккаунтыңыздың резервтік поштасы өзгертілді. Мұны сіз жасамасаңыз, әкімшіге хабарласыңыз."
				if event == "recovery_email_removed" {
					text = "Аккаунтыңыздың резервтік поштасы ажыратылды. Мұны сіз жасамасаңыз, әкімшіге хабарласыңыз."
				}
				if event == "password_recovered" {
					text = "Аккаунтыңыздың құпиясөзі резервтік пошта арқылы қалпына келтірілді. Ескі сеанстар аяқталды. Мұны сіз жасамасаңыз, дереу әкімшіге хабарласыңыз."
				}
			}
			return s.mailer.SendEmail(ctx, email, subject, "<p>"+html.EscapeString(text)+"</p>", text)
		}); err != nil {
			s.logger.Warn("Security notification deferred", zap.Error(err))
			break
		}
	}
}

// Provider tokens may be exchanged for a fresh local JWT only when the original
// authentication session predates no credential cutoff. Parsing here is safe:
// this token was just returned by the trusted Keycloak token endpoint.
func (s *Service) checkProviderCredentialState(ctx context.Context, raw string, userID int64) error {
	if s.tokenManager == nil {
		return nil
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return ErrInvalidToken
	}
	return s.tokenManager.CheckCredentialClaims(ctx, userID, claims)
}

func providerAuthenticationContext(ctx context.Context, raw string) context.Context {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err == nil {
		if at, err := AuthenticationTime(claims); err == nil {
			return WithAuthenticationTime(ctx, at)
		}
	}
	return ctx
}

func (s *RecoveryService) codeHash(id uuid.UUID, code string) string {
	mac := hmac.New(sha256.New, s.tokens.secret)
	_, _ = mac.Write([]byte("recovery-email:" + id.String() + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}
