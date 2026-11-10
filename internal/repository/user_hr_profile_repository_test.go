package repository

import (
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestBuildHRProfileSummaryResponseFallsBackToUserData(t *testing.T) {
	t.Parallel()

	iin := "123456789012"
	createdAt := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Hour)
	user := &domain.User{
		ID:        15,
		IIN:       &iin,
		FirstName: "Alem",
		LastName:  "User",
		Position:  ptr("Analyst"),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	resp := buildHRProfileSummaryResponse(user, nil, "Digital Office", "Ministry of Testing")
	if resp == nil || resp.HRProfile == nil {
		t.Fatal("expected fallback hr_profile to be present")
	}
	if resp.HRProfile.UserID != user.ID {
		t.Fatalf("hr_profile.user_id = %d, want %d", resp.HRProfile.UserID, user.ID)
	}
	if resp.HRProfile.IIN != iin {
		t.Fatalf("hr_profile.iin = %q, want %q", resp.HRProfile.IIN, iin)
	}
	if resp.HRProfile.FirstName != user.FirstName {
		t.Fatalf("hr_profile.first_name = %q, want %q", resp.HRProfile.FirstName, user.FirstName)
	}
	if resp.HRProfile.LastName != user.LastName {
		t.Fatalf("hr_profile.last_name = %q, want %q", resp.HRProfile.LastName, user.LastName)
	}
	if resp.HRProfile.JobName != "Analyst" {
		t.Fatalf("hr_profile.job_name = %q, want %q", resp.HRProfile.JobName, "Analyst")
	}
	if resp.HRProfile.SubName != "Digital Office" {
		t.Fatalf("hr_profile.sub_name = %q, want %q", resp.HRProfile.SubName, "Digital Office")
	}
	if resp.HRProfile.NameGO != "Ministry of Testing" {
		t.Fatalf("hr_profile.name_go = %q, want %q", resp.HRProfile.NameGO, "Ministry of Testing")
	}
	if !resp.HRProfile.CreatedAt.Equal(createdAt) {
		t.Fatalf("hr_profile.created_at = %v, want %v", resp.HRProfile.CreatedAt, createdAt)
	}
	if !resp.HRProfile.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("hr_profile.updated_at = %v, want %v", resp.HRProfile.UpdatedAt, updatedAt)
	}
	if resp.User == nil {
		t.Fatal("expected user response")
	}
	if resp.User.PositionName != "Analyst" {
		t.Fatalf("user.position_name = %q, want %q", resp.User.PositionName, "Analyst")
	}
	if resp.User.DepartmentName != "Digital Office" {
		t.Fatalf("user.department_name = %q, want %q", resp.User.DepartmentName, "Digital Office")
	}
	if resp.User.OrganizationName != "Ministry of Testing" {
		t.Fatalf("user.organization_name = %q, want %q", resp.User.OrganizationName, "Ministry of Testing")
	}
}

func TestBuildHRProfileSummaryResponsePreservesExistingProfile(t *testing.T) {
	t.Parallel()

	user := &domain.User{ID: 25, FirstName: "Backend", LastName: "User"}
	profile := &domain.UserHRProfileSummary{
		ID:        77,
		UserID:    25,
		FirstName: "HR",
		LastName:  "Profile",
	}

	resp := buildHRProfileSummaryResponse(user, profile, "Ignored Department", "Ignored Org")
	if resp == nil || resp.HRProfile == nil {
		t.Fatal("expected hr_profile to be present")
	}
	if resp.HRProfile.ID != profile.ID {
		t.Fatalf("hr_profile.id = %d, want %d", resp.HRProfile.ID, profile.ID)
	}
	if resp.HRProfile.FirstName != profile.FirstName {
		t.Fatalf("hr_profile.first_name = %q, want %q", resp.HRProfile.FirstName, profile.FirstName)
	}
}

func ptr(value string) *string {
	return &value
}
