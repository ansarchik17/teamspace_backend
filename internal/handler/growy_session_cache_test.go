package handler

import (
	"context"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGrowySessionConcurrentRequestsShareOneExchange(t *testing.T) {
	var cache growySessionCache
	var calls atomic.Int32
	var wg sync.WaitGroup
	started, release := make(chan struct{}), make(chan struct{})
	load := func() (string, time.Time, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return "token", time.Now().Add(time.Minute), nil
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, _, err := cache.get(context.Background(), "identity", load)
			if err != nil || token != "token" {
				t.Errorf("unexpected exchange result")
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("got %d exchanges for one identity", calls.Load())
	}
}

func TestGrowySessionIsolationExpiryAndInvalidation(t *testing.T) {
	var cache growySessionCache
	calls := 0
	load := func() (string, time.Time, error) { calls++; return "token", time.Now().Add(time.Minute), nil }
	for _, key := range []string{"user1-org1-role-admin", "user2-org1-role-admin", "user1-org2-role-admin", "user1-org1-role-employee"} {
		_, hit, err := cache.get(context.Background(), key, load)
		if hit || err != nil {
			t.Fatal("identity context reused")
		}
	}
	_, hit, _ := cache.get(context.Background(), "user1-org1-role-admin", load)
	if !hit || calls != 4 {
		t.Fatal("valid credential not reused")
	}
	cache.invalidate("user1-org1-role-admin", "old-token")
	_, hit, _ = cache.get(context.Background(), "user1-org1-role-admin", load)
	if !hit {
		t.Fatal("old response evicted current token")
	}
	cache.invalidate("user1-org1-role-admin", "token")
	_, hit, _ = cache.get(context.Background(), "user1-org1-role-admin", load)
	if hit {
		t.Fatal("revoked token reused")
	}
	cache.entries["user1-org1-role-admin"].expires = time.Now().Add(-time.Second)
	_, hit, _ = cache.get(context.Background(), "user1-org1-role-admin", load)
	if hit {
		t.Fatal("expired token reused")
	}
}

func TestGrowySessionFailuresAndCancellationNotCached(t *testing.T) {
	var cache growySessionCache
	fail := func() (string, time.Time, error) { return "", time.Time{}, errors.New("upstream unavailable") }
	if _, _, err := cache.get(context.Background(), "user", fail); err == nil {
		t.Fatal("failure lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	load := func() (string, time.Time, error) { return "token", time.Now().Add(time.Minute), nil }
	cache.get(ctx, "user", load)
	if len(cache.entries) != 0 {
		t.Fatal("failed/canceled exchange cached")
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		cache.get(context.Background(), "user", func() (string, time.Time, error) { close(started); <-release; return load() })
	}()
	<-started
	if _, _, err := cache.get(ctx, "user", load); !errors.Is(err, context.Canceled) {
		t.Fatal("waiting cancellation ignored")
	}
	close(release)
	<-done
}

func TestGrowySessionExpiryHonorsUpstreamAndBoundsReuse(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	token := func(exp time.Time) string {
		value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": exp.Unix()}).SignedString([]byte("test-only"))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, tc := range []struct {
		seconds int
		exp     time.Time
		want    time.Time
	}{
		{300, now.Add(300 * time.Second), now.Add(time.Minute)},
		{300, now.Add(30 * time.Second), now.Add(20 * time.Second)},
		{20, now.Add(time.Minute), now.Add(10 * time.Second)},
	} {
		if got := growySessionExpiry(token(tc.exp), tc.seconds, now); !got.Equal(tc.want) {
			t.Fatalf("expiry %s, want %s", got, tc.want)
		}
	}
	if !growySessionExpiry("malformed", 300, now).IsZero() || !growySessionExpiry(token(now.Add(time.Minute)), 0, now).IsZero() {
		t.Fatal("invalid lifetime cached")
	}
}

func TestGrowySessionFingerprintChangesWithAuthorizationContext(t *testing.T) {
	claims := auth.Claims{UserID: 1, TokenID: "session1", Email: "user@example.test"}
	details := domain.GrowyHandoffDetails{OrganizationID: uuid.New(), GrowyRole: "tenant_admin"}
	original := growySessionKey("https://growy.test", &claims, &details)
	check := func(c auth.Claims, d domain.GrowyHandoffDetails) {
		if growySessionKey("https://growy.test", &c, &d) == original {
			t.Fatal("changed authorization context reused credential")
		}
	}
	changedClaims := claims
	changedClaims.UserID = 2
	check(changedClaims, details)
	changedClaims = claims
	changedClaims.TokenID = "session2"
	check(changedClaims, details)
	changedDetails := details
	changedDetails.OrganizationID = uuid.New()
	check(claims, changedDetails)
	changedDetails = details
	changedDetails.GrowyRole = "employee"
	check(claims, changedDetails)
	t.Setenv("GROWY_SSO_PRIVATE_KEY_DER_B64", "rotated-test-key")
	check(claims, details)
}

func TestGrowySessionSeparatesCRMAndLMSAtSamePublicURL(t *testing.T) {
	claims := &auth.Claims{UserID: 1, TokenID: "session1", Email: "user@example.test"}
	details := &domain.GrowyHandoffDetails{OrganizationID: uuid.New(), OrganizationSlug: "company", GrowyRole: "employee"}
	crm := growySessionKey("lms=false:https://teamspace.kz/growy", claims, details)
	lms := growySessionKey("lms=true:https://teamspace.kz/growy", claims, details)
	if crm == lms {
		t.Fatal("CRM and LMS shared a session")
	}
	for _, name := range []string{"GROWY_API_INTERNAL_URL", "GROWY_LMS_INTERNAL_URL", "TENANT_BASE_DOMAIN"} {
		t.Setenv(name, "")
		before := growySessionKey("lms=true:https://teamspace.kz/growy", claims, details)
		t.Setenv(name, "changed-configuration")
		if before == growySessionKey("lms=true:https://teamspace.kz/growy", claims, details) {
			t.Fatalf("changed %s reused upstream credentials", name)
		}
	}
}
