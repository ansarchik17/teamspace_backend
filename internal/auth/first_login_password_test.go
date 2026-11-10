package auth

import (
	"context"
	"testing"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNativeHireFirstLoginSurvivesEveryCookieAndProviderRenewalContext(t *testing.T) {
	for _, client := range []LoginClient{LoginClientWeb, LoginClientMobile} {
		for _, allowLegacy := range []bool{false, true} {
			user := &domain.User{ID: 48, Username: "onb.0675ab3a6ec147d5837c2a81a6e3c85a", IsFirstLogin: true, IsActive: true}
			// No provider/repository is configured: native renewal must neither
			// probe a hard-coded password nor mark onboarding complete.
			svc := &Service{}
			for attempt := 0; attempt < 3; attempt++ {
				mustChange, cannotSkip := svc.firstLoginPasswordRequirement(WithLoginClient(context.Background(), client), user, allowLegacy)
				if !mustChange || !cannotSkip || !user.IsFirstLogin {
					t.Fatalf("native first login lost after renewal: client=%s legacy=%v attempt=%d", client, allowLegacy, attempt)
				}
			}
			user.IsFirstLogin = false // Password-change endpoint confirmed completion.
			if mustChange, cannotSkip := svc.firstLoginPasswordRequirement(WithLoginClient(context.Background(), client), user, allowLegacy); mustChange || cannotSkip {
				t.Fatal("completed first login was required again")
			}
		}
	}
}

func TestFirstLoginRenewalRetainsLegacyNonWebAndDisabledPolicy(t *testing.T) {
	svc := &Service{}
	user := &domain.User{Username: "existing.employee", IsFirstLogin: true, IsActive: true}
	for _, ctx := range []context.Context{context.Background(), WithLoginClient(context.Background(), LoginClientMobile)} {
		if mustChange, cannotSkip := svc.firstLoginPasswordRequirement(ctx, user, true); mustChange || cannotSkip || !user.IsFirstLogin {
			t.Fatal("unrelated first-login behavior changed")
		}
	}
	if mustChange, cannotSkip := svc.firstLoginPasswordRequirement(WithLoginClient(context.Background(), LoginClientWeb), user, false); mustChange || cannotSkip || !user.IsFirstLogin {
		t.Fatal("disabled legacy policy changed")
	}
	if mustChange, cannotSkip := svc.firstLoginPasswordRequirement(context.Background(), nil, true); mustChange || cannotSkip {
		t.Fatal("missing user requires password change")
	}
}

type firstLoginCompletionRepo struct {
	UserRepository
	user        *domain.User
	completions int
}

func (r *firstLoginCompletionRepo) GetByID(context.Context, int64) (*domain.User, error) {
	return r.user, nil
}
func (r *firstLoginCompletionRepo) CompleteOnboarding(context.Context, int64) error {
	r.completions++
	r.user.IsFirstLogin = false
	return nil
}

func TestWelcomeCompletionCannotBypassNativeHirePasswordChange(t *testing.T) {
	repo := &firstLoginCompletionRepo{user: &domain.User{ID: 48, Username: "onb.0675ab3a6ec147d5837c2a81a6e3c85a", IsFirstLogin: true, IsActive: true}}
	svc := &Service{userRepo: repo}
	for attempt := 0; attempt < 2; attempt++ {
		if err := svc.CompleteOnboarding(context.Background(), 48); err == nil {
			t.Fatal("native hire skipped password change through welcome completion")
		}
	}
	if repo.completions != 0 || !repo.user.IsFirstLogin {
		t.Fatal("welcome completion mutated native first-login state")
	}
	repo.user.IsFirstLogin = false
	if err := svc.CompleteOnboarding(context.Background(), 48); err != nil || repo.completions != 1 {
		t.Fatal("completed native first login was rejected")
	}
	repo.user.Username = "existing.employee"
	repo.user.IsFirstLogin = true
	if err := svc.CompleteOnboarding(context.Background(), 48); err != nil || repo.completions != 2 {
		t.Fatal("legacy welcome completion changed")
	}
	repo.user = nil
	if err := svc.CompleteOnboarding(context.Background(), 48); err == nil {
		t.Fatal("missing profile completed onboarding")
	}
}
