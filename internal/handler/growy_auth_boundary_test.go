package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type growyBoundaryTransport func(*http.Request) (*http.Response, error)

func (f growyBoundaryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type growyBoundaryService struct {
	details      *domain.GrowyHandoffDetails
	accessErr    error
	lookups      int
	provisionLMS int
	provisionCRM int
}

func (s *growyBoundaryService) GrowyHandoffDetails(context.Context, int64) (*domain.GrowyHandoffDetails, error) {
	s.lookups++
	return s.details, s.accessErr
}

func (s *growyBoundaryService) GrowyCRMHandoffDetails(ctx context.Context, actor int64) (*domain.GrowyHandoffDetails, error) {
	return s.GrowyHandoffDetails(ctx, actor)
}

func (s *growyBoundaryService) ProvisionGrowyLMSTenant(context.Context, uuid.UUID, string, string, string) error {
	s.provisionLMS++
	return nil
}

func (s *growyBoundaryService) ProvisionGrowyTenant(context.Context, uuid.UUID, string, string) error {
	s.provisionCRM++
	return nil
}

func configureGrowyBoundary(t *testing.T) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", base64.StdEncoding.EncodeToString(der))
	t.Setenv("GROWY_SSO_ISSUER", "https://workspace.example.invalid")
	t.Setenv("GROWY_PUBLIC_URL", "https://crm.example.invalid")
	t.Setenv("GROWY_LMS_PUBLIC_URL", "https://workspace.example.invalid/growy")
	t.Setenv("GROWY_API_INTERNAL_URL", "")
	t.Setenv("GROWY_LMS_INTERNAL_URL", "")
	t.Setenv("GROWY_TENANT_PROVISION_URL", "")
	t.Setenv("TENANT_BASE_DOMAIN", "workspace.example.invalid")
}

func newGrowyBoundaryService() *growyBoundaryService {
	return &growyBoundaryService{details: &domain.GrowyHandoffDetails{
		OrganizationID:   uuid.New(),
		OrganizationName: "Boundary test",
		OrganizationSlug: "boundary-test",
		GrowyRole:        "employee",
	}}
}

func serveGrowyBoundary(svc *growyBoundaryService, h *OrganizationHandler, claims *auth.Claims, method, path string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if claims != nil {
			c.Set("claims", claims)
		}
	})
	router.Any("/growy/api/*path", func(c *gin.Context) { h.proxyGrowyAPI(c, svc) })
	request := httptest.NewRequest(method, "/growy/api/"+path, strings.NewReader(`{"name":"synthetic"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer synthetic-workspace-token")
	request.Header.Set("Cookie", "access_token=synthetic-workspace-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestGrowyProxyUpstreamAuthenticationBoundary(t *testing.T) {
	configureGrowyBoundary(t)
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}).SignedString([]byte("synthetic-growy-test-key"))
	require.NoError(t, err)
	claims := &auth.Claims{UserID: 7, Email: "employee@example.invalid", TokenID: "boundary-test"}

	// The real HTTP clients use DefaultTransport. Replace it only in these serial
	// subtests, so the actual proxy can run without sockets, credentials or a DB.
	for _, route := range []struct {
		name, method, path, upstream string
		lms                          bool
	}{
		{"learning", http.MethodGet, "training-programs/my-unfinished/", "/growy/api/training-programs/my-unfinished/", true},
		{"crm_mutation", http.MethodPost, "crm/leads/", "/api/platform/v1/crm/leads/", false},
	} {
		for _, phase := range []string{"exchange", "api"} {
			for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict} {
				t.Run(route.name+"/"+phase+"/"+strconv.Itoa(status), func(t *testing.T) {
					svc := newGrowyBoundaryService()
					h := NewOrganizationHandler(nil, zap.NewNop())
					var exchanges, requests int
					previous := http.DefaultTransport
					t.Cleanup(func() { http.DefaultTransport = previous })
					http.DefaultTransport = growyBoundaryTransport(func(request *http.Request) (*http.Response, error) {
						require.Empty(t, request.Header.Get("Cookie"))
						responseStatus := status
						body := `{"code":"token_not_valid","message":"private upstream diagnostic","access":"synthetic-private-token"}`
						if strings.HasSuffix(request.URL.Path, "/api/token/workspace/") {
							exchanges++
							require.Empty(t, request.Header.Get("Authorization"))
							if phase == "api" {
								responseStatus = http.StatusOK
								body = fmt.Sprintf(`{"access":%q,"expires_in":60}`, access)
							}
						} else {
							requests++
							require.Equal(t, route.upstream, request.URL.Path)
							require.Equal(t, route.method, request.Method)
							require.Equal(t, "Bearer "+access, request.Header.Get("Authorization"))
						}
						return &http.Response{
							StatusCode: responseStatus,
							Header: http.Header{
								"Content-Type":     {"application/json"},
								"Set-Cookie":       {"access_token=synthetic-private-token"},
								"Www-Authenticate": {`Bearer realm="growy"`},
							},
							Body: io.NopCloser(strings.NewReader(body)), Request: request,
						}, nil
					})
					response := serveGrowyBoundary(svc, h, claims, route.method, route.path)
					wantStatus := status
					if status == http.StatusUnauthorized {
						wantStatus = http.StatusBadGateway
					}
					require.Equal(t, wantStatus, response.Code)
					require.Empty(t, response.Header().Values("Set-Cookie"))
					require.Empty(t, response.Header().Get("WWW-Authenticate"))
					require.NotContains(t, response.Body.String(), "private upstream diagnostic")
					require.NotContains(t, response.Body.String(), "synthetic-private-token")
					var payload struct {
						Code        string `json:"code"`
						Message     string `json:"message"`
						UserMessage string `json:"user_message"`
					}
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
					require.NotEmpty(t, payload.Message)
					require.NotEmpty(t, payload.UserMessage)
					wantCode := "growy_exchange_failed"
					if phase == "api" {
						wantCode = "token_not_valid"
					}
					require.Equal(t, wantCode, payload.Code)
					require.Equal(t, 1, exchanges)
					if phase == "exchange" {
						require.Zero(t, requests, "failed exchange must not reach the business API")
					} else {
						require.Equal(t, 1, requests, "a mutation must never be replayed after upstream rejection")
					}
					require.Equal(t, 1, svc.lookups)
					if route.lms {
						require.Equal(t, 1, svc.provisionLMS)
						require.Zero(t, svc.provisionCRM)
					} else {
						require.Equal(t, 1, svc.provisionCRM)
						require.Zero(t, svc.provisionLMS)
					}
					if phase == "exchange" || status == http.StatusUnauthorized || status == http.StatusForbidden {
						require.Empty(t, h.growySessions.entries, "rejected credentials must not remain cached")
					} else {
						require.Len(t, h.growySessions.entries, 1, "a business conflict must not revoke credentials")
					}
					t.Logf("GROWY_AUTH_BOUNDARY %s %s %d %s", route.name, phase, response.Code, response.Body.String())
				})
			}
		}
	}
}

func TestGrowyProxyKeepsWorkspaceAccessFailures(t *testing.T) {
	configureGrowyBoundary(t)
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = growyBoundaryTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("local access failure must not contact Growy")
		return nil, errors.New("unexpected upstream call")
	})
	for _, tc := range []struct {
		name   string
		claims *auth.Claims
		want   int
		code   string
	}{
		{"anonymous", nil, http.StatusUnauthorized, "workspace_identity_unavailable"},
		{"missing_email", &auth.Claims{UserID: 7}, http.StatusUnauthorized, "workspace_identity_unavailable"},
		{"company_denied", &auth.Claims{UserID: 7, Email: "employee@example.invalid"}, http.StatusForbidden, "growy_company_access_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGrowyBoundaryService()
			svc.accessErr = errors.New("company access denied")
			response := serveGrowyBoundary(svc, NewOrganizationHandler(nil, zap.NewNop()), tc.claims, http.MethodGet, "training-programs/my-unfinished/")
			require.Equal(t, tc.want, response.Code)
			require.Contains(t, response.Body.String(), tc.code)
			require.Zero(t, svc.provisionLMS)
			require.Zero(t, svc.provisionCRM)
			if tc.claims == nil || tc.claims.Email == "" {
				require.Zero(t, svc.lookups)
			} else {
				require.Equal(t, 1, svc.lookups)
			}
		})
	}
}
