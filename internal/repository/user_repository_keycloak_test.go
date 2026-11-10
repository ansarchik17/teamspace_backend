package repository

import (
	"context"
	"testing"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNormalizeKeycloakEmail(t *testing.T) {
	if got := normalizeKeycloakEmail(" TEST@GOV.KZ "); got != "test@gov.kz" {
		t.Fatalf("normalizeKeycloakEmail() = %q, want %q", got, "test@gov.kz")
	}
}

func TestNormalizeKeycloakIIN(t *testing.T) {
	if got := normalizeKeycloakIIN(" 123-456-789 012 "); got != "123456789012" {
		t.Fatalf("normalizeKeycloakIIN() = %q, want %q", got, "123456789012")
	}
	if got := normalizeKeycloakIIN("abc"); got != "" {
		t.Fatalf("normalizeKeycloakIIN() = %q, want empty string", got)
	}
}

func TestNormalizeKeycloakUsername(t *testing.T) {
	if got := normalizeKeycloakUsername(" Some User "); got != "someuser" {
		t.Fatalf("normalizeKeycloakUsername() = %q, want %q", got, "someuser")
	}
}

func TestKeycloakAutoCreateUsersEnabled(t *testing.T) {
	t.Setenv("KEYCLOAK_AUTO_CREATE_USERS", "")
	if !keycloakAutoCreateUsersEnabled() {
		t.Fatal("expected auto-create enabled by default")
	}

	t.Setenv("KEYCLOAK_AUTO_CREATE_USERS", "false")
	if keycloakAutoCreateUsersEnabled() {
		t.Fatal("expected auto-create disabled from KEYCLOAK_AUTO_CREATE_USERS=false")
	}

}

func TestSyncKeycloakIdentityFieldsPreservesManualDriveActivityFlag(t *testing.T) {
	existing := &domain.User{
		Email:                    "old@gov.kz",
		Username:                 "old.user",
		FirstName:                "Old",
		LastName:                 "User",
		IsActive:                 true,
		Role:                     domain.RoleUser,
		CanViewFullDriveActivity: true,
	}
	incoming := &domain.User{
		Email:                    "new@gov.kz",
		Username:                 "new.user",
		FirstName:                "New",
		LastName:                 "User",
		IsActive:                 true,
		Role:                     domain.RoleUser,
		CanViewFullDriveActivity: false,
	}

	repo := &UserRepository{}
	repo.syncKeycloakIdentityFields(existing, incoming)

	if !existing.CanViewFullDriveActivity {
		t.Fatal("expected manual can_view_full_drive_activity flag to survive keycloak sync")
	}
	if existing.Email != "new@gov.kz" {
		t.Fatalf("expected existing.Email to be updated to %q, got %q", "new@gov.kz", existing.Email)
	}
	if existing.Username != "new.user" {
		t.Fatalf("expected existing.Username to be updated to %q, got %q", "new.user", existing.Username)
	}
}

func TestShouldReplaceIdentityField(t *testing.T) {
	if !shouldReplaceIdentityField("old@gov.kz", "new@gov.kz") {
		t.Fatal("expected true when incoming email is different")
	}
	if shouldReplaceIdentityField("same@gov.kz", "same@gov.kz") {
		t.Fatal("expected false when incoming email is identical")
	}
	if !shouldReplaceIdentityField("", "new@gov.kz") {
		t.Fatal("expected true when current email is empty")
	}
	if shouldReplaceIdentityField("old@gov.kz", "") {
		t.Fatal("expected false when incoming email is empty")
	}
}

func TestInactiveEmployeeCannotBeReactivatedByKeycloakSync(t *testing.T) {
	repo := &UserRepository{}
	existing := &domain.User{ID: 77, IsActive: false}
	incoming := &domain.User{IsActive: true}
	if err := repo.persistLinkedKeycloakUser(context.Background(), existing, incoming, "keycloak_id"); err == nil {
		t.Fatal("inactive user was reactivated")
	}
	if existing.IsActive {
		t.Fatal("inactive user was mutated before rejecting sync")
	}
}
