package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestCreateOrganizationRequestNormalizesSlug(t *testing.T) {
	req, err := (CreateOrganizationRequest{Name: "Acme Labs"}).Normalize()
	if err != nil {
		t.Fatalf("normalize organization: %v", err)
	}
	if req.Slug != "acme-labs" {
		t.Fatalf("slug = %q, want acme-labs", req.Slug)
	}
}

func TestCreateOrganizationUnitRequestDefaultsType(t *testing.T) {
	req, err := (CreateOrganizationUnitRequest{Name: "Product"}).Normalize()
	if err != nil {
		t.Fatalf("normalize unit: %v", err)
	}
	if req.UnitType != "team" {
		t.Fatalf("unit type = %q, want team", req.UnitType)
	}
}

func TestUpdateOrganizationUnitRejectsInvalidParentSelf(t *testing.T) {
	id := uuid.New()
	req := UpdateOrganizationUnitRequest{ParentUnitID: &id}
	if err := req.Validate(); err != nil {
		t.Fatalf("request validation should not inspect database relationships: %v", err)
	}
}

func TestOrganizationUnitUpdateRejectsAmbiguousChanges(t *testing.T) {
	id := uuid.New()
	manager := int64(1)
	empty := " "
	for _, req := range []UpdateOrganizationUnitRequest{
		{ParentUnitID: &id, ClearParent: true},
		{ManagerID: &manager, ClearManager: true},
		{UnitType: &empty},
	} {
		if err := req.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", req)
		}
	}
}
