package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestSDCounterReadContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       int64
		failure    int
	}{
		{"count", `{"open_count":27,"latest_tickets":[{"id":"not returned to browser"}]}`, 200, 27, 0},
		{"zero", `{"open_count":0}`, 200, 0, 0},
		{"missing", `{}`, 200, 0, 503},
		{"null", `{"open_count":null}`, 200, 0, 503},
		{"negative", `{"open_count":-1}`, 200, 0, 503},
		{"fraction", `{"open_count":1.5}`, 200, 0, 503},
		{"unsafe", `{"open_count":9007199254740992}`, 200, 0, 503},
		{"permission", `{"error":"private upstream details"}`, 403, 0, 403},
		{"session", `{"error":"private upstream details"}`, 401, 0, 503},
		{"failure", `{"error":"private upstream details"}`, 500, 0, 503},
		{"redirect", ``, 302, 0, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/dashboard/stats", r.URL.Path)
				require.Equal(t, "limit=1", r.URL.RawQuery)
				require.Equal(t, "sultan-corp.teamspace.kz", r.Host)
				require.Equal(t, "Bearer scoped-sd-user", r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("X-Internal-Token"))
				require.Empty(t, r.Header.Get("Cookie"))
				w.Header().Set("Location", "http://127.0.0.1:1/never-follow")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			count, err := readSDOpenTicketCount(context.Background(), upstream.URL, AIConnectorAuthorization{Token: "Bearer scoped-sd-user", Host: "sultan-corp.teamspace.kz"})
			if tc.failure == 0 {
				require.NoError(t, err)
				require.Equal(t, tc.want, count)
			} else {
				require.Equal(t, tc.failure, apperrors.ToAppError(err).StatusCode)
				require.NotContains(t, err.Error(), "private upstream details")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readSDOpenTicketCount(ctx, "http://127.0.0.1:1", AIConnectorAuthorization{Token: "Bearer scoped", Host: "company.test"})
	require.Equal(t, 503, apperrors.ToAppError(err).StatusCode)
}

func TestSDCounterCompanyIdentityIntegration(t *testing.T) {
	ctx, db, _, companyA, companyB := newODOFixture(t)
	admission, err := os.ReadFile("../../db/migrations/20261022120000_employment_admission.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(admission), "-- +goose Down")[0])
	require.NoError(t, err)
	sdA, sdB := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `CREATE TABLE organization_app_links(organization_id UUID,application TEXT,external_company_id UUID);
 INSERT INTO organization_modules SELECT id,'sd',true,true FROM organizations;`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_app_links VALUES($1,'sd',$2),($3,'sd',$4)`, companyA, sdA, companyB, sdB)
	require.NoError(t, err)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/api/internal/workspace/ai-read-token" {
			require.Equal(t, "internal-secret", r.Header.Get("X-Internal-Token"))
			var input struct {
				Profile domain.SDHandoffProfile `json:"profile"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
			wantCompany, host := sdA, "company-a.test"
			if input.Profile.UserID == 6 {
				wantCompany, host = sdB, "company-b.test"
			}
			require.Equal(t, wantCompany, input.Profile.CompanyID)
			require.Equal(t, "subject-"+map[int64]string{1: "1", 6: "6"}[input.Profile.UserID], input.Profile.Subject)
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "limited-for-" + host, "host": host})
			return
		}
		require.Equal(t, "/dashboard/stats", r.URL.Path)
		require.Equal(t, "Bearer limited-for-"+r.Host, r.Header.Get("Authorization"))
		count := 5
		if r.Host == "company-b.test" {
			count = 11
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"open_count": count})
	}))
	defer upstream.Close()
	t.Setenv("SD_ACCESS_API_URL", upstream.URL)
	t.Setenv("SD_ACCESS_INTERNAL_TOKEN", "internal-secret")
	svc := NewOrganizationService(repository.NewOrganizationRepository(db, zap.NewNop()), zap.NewNop())
	for actor, want := range map[int64]int64{1: 5, 6: 11} {
		count, err := svc.SDOpenTicketCount(ctx, actor)
		require.NoError(t, err)
		require.Equal(t, want, count)
	}
	before := calls
	for _, mutation := range []string{
		`UPDATE users SET is_active=false WHERE id=1`,
		`UPDATE users SET is_active=true WHERE id=1; UPDATE organization_modules SET enabled=false WHERE application='sd'`,
	} {
		_, err := db.Exec(ctx, mutation)
		require.NoError(t, err)
		_, err = svc.SDOpenTicketCount(ctx, 1)
		require.Error(t, err)
		require.Equal(t, before, calls, "revoked user/module must not reach SD")
	}
}

func TestSDCounterInvalidAuthorization(t *testing.T) {
	for _, host := range []string{"", "other.test/path", "other.test\r\nX-Extra: value"} {
		_, err := readSDOpenTicketCount(context.Background(), "http://127.0.0.1:1", AIConnectorAuthorization{Token: "Bearer scoped", Host: host})
		require.Error(t, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := readSDOpenTicketCount(ctx, "https://user:pass@invalid.test", AIConnectorAuthorization{Token: strings.Repeat("x", 10), Host: "company.test"})
	require.Error(t, err)
}
